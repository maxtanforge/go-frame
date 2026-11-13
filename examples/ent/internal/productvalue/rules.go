// Package productvalue contains field-independent rules shared by DTOs and Ent.
package productvalue

import (
	"strings"
	"unicode/utf8"

	"github.com/maxtanforge/go-frame/validation"
)

const NameMax = 200
const NoteMax = 1000

func Name(value string) error {
	var issues []validation.Issue
	if strings.TrimSpace(value) == "" {
		issues = append(issues, validation.Issue{Code: "required", Message: "商品名称不能为空"})
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		issues = append(issues, validation.Issue{Code: "format", Message: "商品名称包含非法字符"})
	}
	if len(value) > NameMax {
		issues = append(issues, validation.Issue{Code: "max", Message: "商品名称不能超过 200 字节"})
	}
	if len(issues) > 0 {
		return validation.NewErrors(issues...)
	}
	return nil
}
func Note(value string) error {
	var issues []validation.Issue
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		issues = append(issues, validation.Issue{Code: "format", Message: "备注包含非法字符"})
	}
	if len(value) > NoteMax {
		issues = append(issues, validation.Issue{Code: "max", Message: "备注不能超过 1000 字节"})
	}
	if len(issues) > 0 {
		return validation.NewErrors(issues...)
	}
	return nil
}
func Price(value int64) error {
	if value < 0 {
		return validation.NewErrors(validation.Issue{Code: "min", Message: "商品价格不能小于 0"})
	}
	return nil
}
