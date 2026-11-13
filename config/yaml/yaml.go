// Package yaml supplies a strict, bounded, optional YAML configuration source.
package yaml

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	config "github.com/maxtanforge/go-frame/config"
	"go.yaml.in/yaml/v3"
)

const maxBytes = 1 << 20

// Parse reads one mapping document without opening resources or changing globals.
// Keys are path segments (no dots). Nulls, aliases, custom tags, nested lists and
// empty nested mappings are rejected. An empty root mapping is allowed.
// Unknown paths are rejected by config.Decode against the consumer's schema.
func Parse(r io.Reader) (config.Source, error) {
	if r == nil {
		return config.Source{}, errors.New("config yaml: reader is required")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return config.Source{}, errors.New("config yaml: cannot read input")
	}
	if len(data) > maxBytes {
		return config.Source{}, errors.New("config yaml: document exceeds 1 MiB")
	}
	d := yaml.NewDecoder(strings.NewReader(string(data)))
	var doc yaml.Node
	if err = d.Decode(&doc); err != nil {
		return config.Source{}, errors.New("config yaml: invalid document")
	}
	var extra yaml.Node
	if err = d.Decode(&extra); !errors.Is(err, io.EOF) {
		return config.Source{}, errors.New("config yaml: expected exactly one document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return config.Source{}, errors.New("config yaml: root must be a mapping")
	}
	p := parser{values: make(map[string]string)}
	if err = p.walk(doc.Content[0], "", 0); err != nil {
		return config.Source{}, err
	}
	return config.Source{Name: "yaml", Values: p.values}, nil
}

type parser struct {
	values map[string]string
	nodes  int
}

func invalid(n *yaml.Node, reason string) error {
	return fmt.Errorf("config yaml: line %d: %s", n.Line, reason)
}

func (p *parser) walk(n *yaml.Node, path string, depth int) error {
	p.nodes++
	if depth > 32 || p.nodes > 4096 {
		return invalid(n, "structure limit exceeded")
	}
	if n.Anchor != "" {
		return invalid(n, "anchors are not supported")
	}
	if n.Kind == yaml.MappingNode {
		if n.Tag != "!!map" {
			return invalid(n, "unsupported mapping tag")
		}
		if path != "" && len(n.Content) == 0 {
			return invalid(n, "empty nested mapping is not supported")
		}
		seen := make(map[string]bool)
		for i := 0; i < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || k.Anchor != "" || !segment(k.Value) {
				return invalid(k, "invalid mapping key")
			}
			if seen[k.Value] {
				return invalid(k, "duplicate mapping key")
			}
			seen[k.Value] = true
			key := k.Value
			if path != "" {
				key = path + "." + key
			}
			if err := p.walk(v, key, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	var raw string
	if n.Kind == yaml.SequenceNode {
		if n.Tag != "!!seq" {
			return invalid(n, "unsupported sequence tag")
		}
		items := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			p.nodes++
			if p.nodes > 4096 {
				return invalid(item, "structure limit exceeded")
			}
			v, err := scalar(item)
			if err != nil {
				return err
			}
			items = append(items, v)
		}
		b, err := json.Marshal(items)
		if err != nil {
			return invalid(n, "invalid list")
		}
		raw = string(b)
	} else {
		v, err := scalar(n)
		if err != nil {
			return err
		}
		switch v := v.(type) {
		case string:
			raw = v
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return invalid(n, "invalid scalar")
			}
			raw = string(b)
		}
	}
	if len(raw) > 64<<10 {
		return invalid(n, "value exceeds 64 KiB")
	}
	p.values[path] = raw
	return nil
}

func scalar(n *yaml.Node) (any, error) {
	if n.Kind != yaml.ScalarNode || n.Anchor != "" {
		return nil, invalid(n, "expected scalar value without anchors")
	}
	if len(n.Value) > 64<<10 {
		return nil, invalid(n, "value exceeds 64 KiB")
	}
	switch n.Tag {
	case "!!str":
		return n.Value, nil
	case "!!bool":
		v, err := strconv.ParseBool(strings.ToLower(n.Value))
		if err == nil {
			return v, nil
		}
	case "!!int", "!!float":
		// YAML resolves an overflowing plain decimal integer as float64. Do not
		// silently round an identifier before the consumer has selected its type.
		if n.Tag == "!!float" && n.Style&yaml.TaggedStyle == 0 && decimalInteger(n.Value) {
			return nil, invalid(n, "integer exceeds supported range; quote it for text")
		}
		var v any
		if err := n.Decode(&v); err == nil {
			switch x := v.(type) {
			case int, int64, uint64:
				return x, nil
			case float64:
				if n.Tag == "!!float" && !math.IsInf(x, 0) && !math.IsNaN(x) {
					return x, nil
				}
			}
		}
	}
	return nil, invalid(n, "unsupported or invalid scalar type")
}

func decimalInteger(s string) bool {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "+"), "-")
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func segment(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
