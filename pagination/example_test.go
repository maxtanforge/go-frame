package pagination_test

import (
	"fmt"

	"github.com/maxtanforge/go-frame/pagination"
)

func ExampleNewPage() {
	window, err := pagination.NewWindow(3, 20, pagination.DefaultLimits())
	if err != nil {
		fmt.Println(err)
		return
	}
	total := int64(42)
	page, err := pagination.NewPage([]string{"order-41", "order-42"}, window, total)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(page.Current, page.Size, page.Total, page.Pages, page.Records)
	// Output: 3 20 42 3 [order-41 order-42]
}
