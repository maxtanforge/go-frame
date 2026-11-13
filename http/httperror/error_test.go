package httperror

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorExposesCauseAndMessage(t *testing.T) {
	cause := errors.New("internal cause")
	e := &Error{Status: 409, Code: 1001, Message: "conflict", Err: cause}
	require.Equal(t, "internal cause", e.Error())
	require.ErrorIs(t, e, cause)
	var target *Error
	require.True(t, errors.As(e, &target))
	require.Same(t, e, target)
}

func TestErrorWithoutCauseUsesMessage(t *testing.T) {
	e := &Error{Status: 400, Message: "bad request"}
	require.Equal(t, "bad request", e.Error())
	require.Nil(t, e.Unwrap())
}

// 空接收者安全:errors.As 匹配到类型化 nil 时调用方仍会读取 Error/Unwrap。
func TestNilReceiverIsInert(t *testing.T) {
	var e *Error
	require.Empty(t, e.Error())
	require.Nil(t, e.Unwrap())
}
