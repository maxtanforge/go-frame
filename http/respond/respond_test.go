package respond

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/telemetry/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decode 统一解响应体。
func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestAdapterSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return map[string]string{"hello": "frame"}, nil
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body := decode(t, rec)
	assert.EqualValues(t, 200, body["code"])
	assert.True(t, body["success"].(bool))
	assert.Equal(t, "成功", body["message"])
	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "frame", data["hello"])
	assert.Positive(t, body["timestamp"])
	assert.Equal(t, "", body["traceId"]) // 无 Trace 中间件时空串,不省略
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
}

func TestAdapterNilData(t *testing.T) {
	rec := httptest.NewRecorder()
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return nil, nil
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code, "204 语义已移除,恒 200")
	body := decode(t, rec)
	require.Contains(t, body, "data")
	assert.Nil(t, body["data"])
}

func TestAdapterWithMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return WithMessage(map[string]any{"k": "v"}, "已保存"), nil
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := decode(t, rec)
	assert.Equal(t, "已保存", body["message"])
	assert.Equal(t, map[string]any{"k": "v"}, body["data"])
}

func TestAdapterAppError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   int
		wantMsg    string
	}{
		{"full", &httperror.Error{Status: 404, Code: 1001, Message: "未找到"}, 404, 1001, "未找到"},
		{"bare_message_is_internal", &httperror.Error{Message: "参数错误"}, 500, 500, msgProgram},
		{"code_only_is_internal", &httperror.Error{Code: 1001, Message: "余额不足"}, 500, 500, msgProgram},
		{"invalid_success", &httperror.Error{Status: 200, Message: "internal detail"}, 500, 500, msgProgram},
		{"invalid_high", &httperror.Error{Status: 600, Code: 1001, Message: "internal detail"}, 500, 500, msgProgram},
		{"status_only_code_falls_back", &httperror.Error{Status: 409, Message: "冲突"}, 409, 409, "冲突"},
		{"empty_message_falls_back", &httperror.Error{Status: 400}, 400, 400, "失败"},
		{"wrapped", fmt.Errorf("wrap: %w", &httperror.Error{Status: 400, Code: 1002, Message: "bad request"}), 400, 1002, "bad request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
				return nil, tc.err
			}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Equal(t, tc.wantStatus, rec.Code)
			body := decode(t, rec)
			assert.EqualValues(t, tc.wantCode, body["code"])
			assert.Equal(t, tc.wantMsg, body["message"])
			assert.False(t, body["success"].(bool))
			assert.Nil(t, body["data"])
		})
	}
}

func TestAdapterUnknownErrorNoLeak(t *testing.T) {
	rec := httptest.NewRecorder()
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return nil, errors.New("db password is hunter2")
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	body := decode(t, rec)
	assert.EqualValues(t, 500, body["code"])
	assert.Equal(t, "服务处理异常", body["message"], "内部错误信息不得外泄")
	assert.Nil(t, body["data"])
}

func TestAdapterPanicReturns500(t *testing.T) {
	rec := httptest.NewRecorder()
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		panic("boom")
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	body := decode(t, rec)
	assert.EqualValues(t, 500, body["code"])
	assert.Equal(t, "服务处理异常", body["message"])
}

func TestAdapterUnmarshalableDataReturns500(t *testing.T) {
	rec := httptest.NewRecorder()
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return make(chan int), nil
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusInternalServerError, rec.Code, "不可序列化数据应 500,而非裸 200")
	body := decode(t, rec)
	assert.EqualValues(t, 500, body["code"])
	assert.Equal(t, "服务处理异常", body["message"])
}

func TestAdapterTraceID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(trace.IntoContext(req.Context(), "tid-42"))
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return "ok", nil
	}).ServeHTTP(rec, req)

	assert.Equal(t, "tid-42", decode(t, rec)["traceId"])
}

// WithMessage preserves standard time.Time JSON encoding.
func TestAdapterWithMessageStandardTime(t *testing.T) {
	rec := httptest.NewRecorder()
	ts := time.Unix(1700000000, 0).UTC()
	Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return WithMessage(struct {
			T time.Time `json:"t"`
		}{T: ts}, "x"), nil
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := decode(t, rec)
	assert.EqualValues(t, ts.Format(time.RFC3339), body["data"].(map[string]any)["t"])
}
