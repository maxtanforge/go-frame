package operationhttp

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/maxtanforge/go-frame/telemetry/operation"
)

const maxRequestBytes = 16 << 10

var keyNormalizer = strings.NewReplacer("_", "", "-", "")

// Capture only bytes the business handler reads; never drain or pre-read bodies.
type bodyCapture struct {
	io.ReadCloser
	mu     sync.Mutex
	data   []byte
	length int64
	total  int64
	eof    bool
	failed bool
}

func (b *bodyCapture) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(n)
	if remaining := maxRequestBytes - len(b.data); remaining > 0 {
		b.data = append(b.data, p[:min(n, remaining)]...)
	}
	b.eof = b.eof || err == io.EOF
	b.failed = b.failed || err != nil && err != io.EOF
	return n, err
}

// Called with the operation state lock held, before business processing.
func captureRequest(r *http.Request, s *state) {
	if s.metadata.Mode == Disabled {
		return
	}
	if s.metadata.Request.Query == Enabled {
		// Do not retain an unbounded attacker-supplied query.
		s.queryTooLarge = len(r.URL.RawQuery) > maxRequestBytes
		if !s.queryTooLarge {
			s.query = r.URL.RawQuery
		} else {
			s.query = ""
		}
	}
	if s.metadata.Request.Body != Enabled || s.body != nil {
		return
	}
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	s.bodyPresent = true
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	mediaType = strings.ToLower(mediaType)
	isJSON := mediaType == "application/json" || strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")
	if err != nil || !isJSON {
		s.bodyUnsupported = true
		return
	}
	s.bodyUnsupported = false
	if r.Body != nil && r.Body != http.NoBody {
		s.body = &bodyCapture{ReadCloser: r.Body, length: r.ContentLength}
		r.Body = s.body
	}
}

func requestSnapshot(s *state, options RequestOptions, params map[string]string, paramsTooLarge bool) json.RawMessage {
	values := map[string]any{}
	custom := make(map[string]bool, len(options.SensitiveFields))
	for _, key := range options.SensitiveFields {
		custom[normalizeKey(key)] = true
	}
	if options.Params == Enabled {
		if paramsTooLarge {
			values["params_omitted"] = "too_large"
		} else if len(params) > 0 {
			selected := make(map[string]string)
			if options.ParamsFields == nil {
				selected = params
			} else {
				for _, key := range options.ParamsFields {
					if value, ok := params[key]; ok {
						selected[key] = value
					}
				}
			}
			for key := range selected {
				if sensitiveKey(key, custom) {
					selected[key] = "***"
				}
			}
			values["params"] = selected
		}
	}
	if options.Query == Enabled {
		if s.queryTooLarge {
			values["query_omitted"] = "too_large"
		} else if query, err := url.ParseQuery(s.query); err != nil {
			values["query_omitted"] = "invalid"
		} else {
			if options.QueryFields != nil {
				selected := make(url.Values, len(options.QueryFields))
				for _, key := range options.QueryFields {
					if value, ok := query[key]; ok {
						selected[key] = value
					}
				}
				query = selected
			}
			for key := range query {
				if sensitiveKey(key, custom) {
					query[key] = []string{"***"}
				}
			}
			values["query"] = query
		}
	}
	if options.Body == Enabled && s.bodyPresent {
		switch {
		case s.bodyUnsupported:
			values["body_omitted"] = "unsupported_content_type"
		case s.body == nil:
			values["body_omitted"] = "not_read"
		default:
			body, reason := s.body.snapshot(options.BodyFields, custom)
			if reason != "" {
				values["body_omitted"] = reason
			} else {
				values["body"] = body
			}
		}
	}
	if len(values) == 0 {
		return nil
	}
	data, err := json.Marshal(values)
	if err != nil || len(data) > maxRequestBytes {
		return json.RawMessage(`{"omitted":"too_large"}`)
	}
	return data
}

func (b *bodyCapture) snapshot(fields []string, custom map[string]bool) (any, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total > maxRequestBytes || b.length > maxRequestBytes {
		return nil, "too_large"
	}
	if b.failed {
		return nil, "read_error"
	}
	if b.total == 0 {
		return nil, "not_read"
	}
	if !b.eof && (b.length <= 0 || b.total != b.length) {
		return nil, "incomplete"
	}
	if !utf8.Valid(b.data) || !json.Valid(b.data) {
		return nil, "invalid_json"
	}
	decoder := json.NewDecoder(bytes.NewReader(b.data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, "invalid_json"
	}
	if fields != nil {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, "not_object"
		}
		selected := make(map[string]any, len(fields))
		for _, key := range fields {
			if child, ok := object[key]; ok {
				selected[key] = child
			}
		}
		value = selected
	}
	if !redact(value, 0, custom) {
		return nil, "too_deep"
	}
	return value, ""
}

func normalizeKey(key string) string { return keyNormalizer.Replace(strings.ToLower(key)) }

func sensitiveKey(key string, custom map[string]bool) bool {
	key = normalizeKey(key)
	if custom[key] {
		return true
	}
	return operation.SensitiveKey(key)
}

func redact(value any, depth int, custom map[string]bool) bool {
	if depth > 10 {
		return false
	}
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if sensitiveKey(key, custom) {
				item[key] = "***"
			} else if !redact(child, depth+1, custom) {
				return false
			}
		}
	case []any:
		for _, child := range item {
			if !redact(child, depth+1, custom) {
				return false
			}
		}
	}
	return true
}
