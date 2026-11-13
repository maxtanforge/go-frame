package httpclient_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/maxtanforge/go-frame/http/client"
)

func ExampleDoJSON() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"name":"frame"}`)
	}))
	defer server.Close()
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		panic(err)
	}
	defer client.CloseIdleConnections()
	var result struct {
		Name string `json:"name"`
	}
	if err := httpclient.DoJSON(context.Background(), client, http.MethodGet, server.URL, nil, &result); err != nil {
		panic(err)
	}
	fmt.Println(result.Name)
	// Output: frame
}
