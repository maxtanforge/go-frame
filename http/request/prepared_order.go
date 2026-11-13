package request

import (
	"sort"

	"github.com/maxtanforge/go-frame/validation"
)

// OrderIssues returns an independent, stable snapshot in DTO declaration and
// collection-index order. Rule order for one path is preserved. Call this for
// binding and tag diagnostics before appending custom validation diagnostics.
func (p *Prepared[T]) OrderIssues(issues []validation.Issue) []validation.Issue {
	out := append([]validation.Issue{}, issues...)
	for i := range out {
		out[i].Path = append(validation.Path{}, out[i].Path...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := issueRank(p.fields, out[i].Path), issueRank(p.fields, out[j].Path)
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return out
}
func issueRank(fs []Field, path validation.Path) []int {
	rank := []int{}
	for _, segment := range path {
		if n, ok := segment.(int); ok {
			rank = append(rank, n)
			continue
		}
		name, ok := segment.(string)
		if !ok {
			return append(rank, len(fs))
		}
		found := false
		for i, f := range fs {
			if f.Path[0] == name {
				rank = append(rank, i)
				fs = f.Children
				found = true
				break
			}
		}
		if !found {
			return append(rank, len(fs))
		}
	}
	return rank
}
