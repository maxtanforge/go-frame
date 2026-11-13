package respond_test

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/validation"
	"github.com/stretchr/testify/require"
)

func TestValidationLimitUsesSafeInputError(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{validation.ErrLimit, 400},
		{fmt.Errorf("private workload detail: %w", validation.ErrLimit), 400},
		{errors.Join(validation.ErrLimit, validation.ErrSchema), 500},
		{errors.Join(validation.ErrLimit, validation.NewErrors(validation.Issue{Code: "required", Message: "partial result"})), 400},
	} {
		w := httptest.NewRecorder()
		respond.WriteError(w, httptest.NewRequest("GET", "/", nil), tc.err)
		require.Equal(t, tc.status, w.Code)
		require.NotContains(t, w.Body.String(), "private workload detail")
		require.NotContains(t, w.Body.String(), "partial result")
	}
}

func TestValidationCollectionFirstMessage(t *testing.T) {
	err := validation.NewErrors(validation.Issue{Path: validation.Path{"name"}, Code: "required", Message: "请填写名称"}, validation.Issue{Path: validation.Path{"price"}, Code: "min", Message: "价格不合法"})
	w := httptest.NewRecorder()
	respond.WriteError(w, httptest.NewRequest("GET", "/", nil), err)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "请填写名称")
	require.Contains(t, w.Body.String(), `"data":null`)
	require.NotContains(t, w.Body.String(), "价格不合法")
	require.Len(t, err.Issues(), 2)
}

// 业务用 %w 包装集合后仍须映射 400,不得因裸类型断言失败掉 500。
func TestWrappedValidationCollectionStillMapsTo400(t *testing.T) {
	inner := validation.NewErrors(validation.Issue{Path: validation.Path{"name"}, Code: "required", Message: "请填写名称"})
	w := httptest.NewRecorder()
	respond.WriteError(w, httptest.NewRequest("GET", "/", nil), fmt.Errorf("update record: %w", inner))
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "请填写名称")
	require.NotContains(t, w.Body.String(), "update record")
}
