package concurrency_test

import (
	"context"
	"fmt"

	"github.com/maxtanforge/go-frame/concurrency"
)

func ExampleMap() {
	values, err := concurrency.Map(context.Background(), []int{3, 1, 2}, 2,
		func(ctx context.Context, value int) (int, error) { return value * value, ctx.Err() })
	if err != nil {
		panic(err)
	}
	fmt.Println(values)
	// Output: [9 1 4]
}
