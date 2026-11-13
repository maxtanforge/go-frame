package app

import (
	"errors"
	"fmt"
	"strings"
)

func compileHooks(hooks []Hook) ([]Hook, error) {
	if len(hooks) > 4096 {
		return nil, errors.New("app: too many hooks")
	}
	indices := make(map[string]int, len(hooks))
	for i, h := range hooks {
		if strings.TrimSpace(h.Name) != h.Name || h.Name == "" || len(h.Name) > 128 {
			return nil, errors.New("app: invalid hook name")
		}
		if _, exists := indices[h.Name]; exists {
			return nil, fmt.Errorf("app: duplicate hook %q", h.Name)
		}
		if h.Start == nil && h.Stop == nil {
			return nil, fmt.Errorf("app: hook %q has no callbacks", h.Name)
		}
		indices[h.Name] = i
	}
	indegree := make([]int, len(hooks))
	edges := make([][]int, len(hooks))
	for i, h := range hooks {
		if len(h.DependsOn) > len(hooks) {
			return nil, errors.New("app: too many hook dependencies")
		}
		seen := map[string]bool{}
		for _, name := range h.DependsOn {
			dependency, exists := indices[name]
			if !exists || seen[name] || dependency == i {
				return nil, fmt.Errorf("app: invalid dependency %q for hook %q", name, h.Name)
			}
			seen[name] = true
			indegree[i]++
			edges[dependency] = append(edges[dependency], i)
		}
	}
	ordered := make([]Hook, 0, len(hooks))
	done := make([]bool, len(hooks))
	for range hooks {
		selected := -1
		for i := range hooks {
			if !done[i] && indegree[i] == 0 {
				selected = i
				break
			}
		}
		if selected < 0 {
			return nil, errors.New("app: hook dependency cycle")
		}
		done[selected] = true
		h := hooks[selected]
		h.DependsOn = append([]string(nil), h.DependsOn...)
		ordered = append(ordered, h)
		for _, next := range edges[selected] {
			indegree[next]--
		}
	}
	return ordered, nil
}
