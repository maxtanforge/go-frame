package id_test

import (
	"fmt"

	"github.com/maxtanforge/go-frame/id"
)

// ExampleSnowflake_Next 生成 64 位雪花 ID(41 时间戳位 + 10 节点位 + 12 序列位)。
func ExampleSnowflake_Next() {
	sf, err := id.NewSnowflake(1)
	if err != nil {
		fmt.Println("init:", err)
		return
	}
	n, err := sf.Next()
	if err != nil {
		fmt.Println("next:", err)
		return
	}
	fmt.Println(n > 0)
	// Output: true
}
