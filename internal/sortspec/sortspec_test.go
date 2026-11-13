package sortspec

import (
	"testing"

	"github.com/maxtanforge/go-frame/pagination"
	"github.com/stretchr/testify/require"
)

func sort(field string, desc bool) pagination.Sort { return pagination.Sort{Field: field, Desc: desc} }

func TestCombineKeepsRequestOrderAndAppendsMissingTies(t *testing.T) {
	// 请求内重复字段由 ValidateSort 拒绝;这里验证显式命中的 tie 保留请求方向与位置,
	// 缺失的 tie 按声明序补齐。
	terms, err := Combine(
		[]pagination.Sort{sort("title", true), sort("id", false)},
		[]pagination.Sort{sort("id", true), sort("created", false)},
		[]string{"id", "title", "created"},
	)
	require.NoError(t, err)
	require.Equal(t, []pagination.Sort{sort("title", true), sort("id", false), sort("created", false)}, terms)
}

func TestCombineEmptyRequestReturnsTies(t *testing.T) {
	terms, err := Combine(nil, []pagination.Sort{sort("id", false)}, []string{"id"})
	require.NoError(t, err)
	require.Equal(t, []pagination.Sort{sort("id", false)}, terms)
}

func TestCombineRejectsUnknownFields(t *testing.T) {
	_, err := Combine([]pagination.Sort{sort("secret", false)}, nil, []string{"id"})
	require.ErrorIs(t, err, pagination.ErrInvalid)
}

func TestCombineCapsCombinedTerms(t *testing.T) {
	// 请求侧 7 项通过 ValidateSort(≤8),叠加 2 个缺失 tie 后共 9 项,合并上限拒绝。
	requested := make([]pagination.Sort, 0, 7)
	for _, f := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		requested = append(requested, sort(f, false))
	}
	_, err := Combine(requested, []pagination.Sort{sort("h", false), sort("i", false)},
		[]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"})
	require.ErrorIs(t, err, pagination.ErrInvalid)
}
