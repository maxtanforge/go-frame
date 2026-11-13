package greetinghttp

import (
	"errors"

	"example.com/service/internal/greeting"
	"github.com/maxtanforge/go-frame/http/httperror"
)

func httpError(err error) error {
	if errors.Is(err, greeting.ErrName) {
		return &httperror.Error{Status: 400, Message: "姓名必须为 1–80 个字符", Err: err}
	}
	return err
}
