package isnil

import "testing"

func TestCheck(t *testing.T) {
	var nilPtr *int
	var nilFunc func()
	var nilMap map[string]int
	var nilSlice []int
	var nilChan chan int
	var anyPtr any = (*struct{})(nil)
	var anyIface = any(nil)

	x := 1
	if !Check(nil) || !Check(nilPtr) || !Check(nilFunc) || !Check(nilMap) ||
		!Check(nilSlice) || !Check(nilChan) || !Check(anyPtr) || !Check(anyIface) {
		t.Fatal("nil 或 typed-nil 必须判真")
	}
	if Check(&x) || Check(map[string]int{}) || Check([]int{}) || Check(0) || Check("") || Check(struct{}{}) {
		t.Fatal("非 nil 值（含空集合）必须判假")
	}
}
