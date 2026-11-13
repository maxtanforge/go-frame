package respond_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/respond"
)

// ExampleAdapter 演示统一包络:成功恒 200,message 可用 WithMessage 自定义。
// timestamp 非确定,打印字段级断言。
func ExampleAdapter() {
	h := respond.Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return respond.WithMessage(map[string]int{"n": 1}, "已保存"), nil
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		fmt.Println("unmarshal:", err)
		return
	}
	fmt.Println(rec.Code, m["code"], m["success"], m["message"], m["data"])
	// Output: 200 200 true 已保存 map[n:1]
}

// ExampleAdapter_appError 业务错误:AppError 映射 HTTP 状态与业务码,失败 data 恒 null。
func ExampleAdapter_appError() {
	h := respond.Adapter(func(_ http.ResponseWriter, _ *http.Request) (any, error) {
		return nil, &httperror.Error{Status: 404, Code: 1001, Message: "未找到"}
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		fmt.Println("unmarshal:", err)
		return
	}
	fmt.Println(rec.Code, m["code"], m["success"], m["message"], m["data"])
	// Output: 404 1001 false 未找到 <nil>
}
