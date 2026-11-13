package trace_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/maxtanforge/go-frame/telemetry/trace"
)

// ExampleMiddleware 演示上游 x-trace-id 透传:ctx、响应头两处同源。
// 未传上游头时自动雪花生成(traceId 非确定,此处演示透传路径)。
func ExampleMiddleware() {
	h := trace.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		fmt.Println("ctx:", trace.FromContext(r.Context()))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(trace.HeaderTraceID, "demo-trace-id")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	fmt.Println("header:", rec.Header().Get(trace.HeaderTraceID))
	// Output:
	// ctx: demo-trace-id
	// header: demo-trace-id
}
