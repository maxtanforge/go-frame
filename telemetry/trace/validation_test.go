package trace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidID(t *testing.T) {
	for _, s := range []string{"a", "aB9._-", strings.Repeat("a", 128)} {
		require.True(t, ValidID(s))
	}
	for _, s := range []string{"", strings.Repeat("a", 129), "x y", "x\ny", "链路", "a/b"} {
		require.False(t, ValidID(s))
	}
}
