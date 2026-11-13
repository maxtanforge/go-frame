package optional_test

import (
	"encoding/json"
	"testing"

	"github.com/maxtanforge/go-frame/optional"
	"github.com/stretchr/testify/require"
)

func TestFieldStates(t *testing.T) {
	type input struct {
		Flag  optional.Field[bool]   `json:"flag"`
		Count optional.Field[int]    `json:"count"`
		Name  optional.Field[string] `json:"name"`
	}
	var missing input
	require.NoError(t, json.Unmarshal([]byte(`{}`), &missing))
	require.False(t, missing.Flag.Present)
	var null input
	require.NoError(t, json.Unmarshal([]byte(`{"flag":null}`), &null))
	require.True(t, null.Flag.Present)
	require.True(t, null.Flag.Null)
	var zero input
	require.NoError(t, json.Unmarshal([]byte(`{"flag":false,"count":0,"name":""}`), &zero))
	require.Equal(t, optional.Field[bool]{Present: true, Value: false}, zero.Flag)
	require.Equal(t, optional.Field[int]{Present: true, Value: 0}, zero.Count)
	require.Equal(t, optional.Field[string]{Present: true, Value: ""}, zero.Name)
}

func TestFailedDecodeClearsStateAndRejectsUnknownOrTrailingData(t *testing.T) {
	field := optional.Field[int]{Present: true, Value: 42}
	require.Error(t, field.UnmarshalJSON([]byte(`"private"`)))
	require.Equal(t, optional.Field[int]{}, field)
	require.Error(t, field.UnmarshalJSON([]byte(`1 2`)))
	require.Equal(t, optional.Field[int]{}, field)
	type child struct {
		Name string `json:"name"`
	}
	var nested optional.Field[child]
	require.Error(t, json.Unmarshal([]byte(`{"name":"a","secret":1}`), &nested))
	require.False(t, nested.Present)
	require.NoError(t, json.Unmarshal([]byte(`{"name":"a"}`), &nested))
	require.True(t, nested.Present)
}
