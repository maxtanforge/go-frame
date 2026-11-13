package logger_test

import (
	"errors"
	"fmt"

	"github.com/maxtanforge/go-frame/telemetry/logger"
)

// ExampleErrAttrs 错误的结构化字段约定:error_name(具体类型)+ error_message。
// 全库 error 级日志统一用此惯例,配 StackOnError 时堆栈自动携带。
func ExampleErrAttrs() {
	fmt.Println(logger.ErrAttrs(nil))
	for _, a := range logger.ErrAttrs(errors.New("db down")) {
		fmt.Println(a.Key, a.Value)
	}
	// Output:
	// []
	// error_name *errors.errorString
	// error_message db down
}
