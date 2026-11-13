package requestmeta

import "net/http"

// Path captures the encoded request path without query parameters. Encoding
// keeps control characters out of logs; oversized paths use a bounded marker.
func Path(r *http.Request) string {
	path := r.URL.EscapedPath()
	if len(path) > 2048 {
		return "<path>"
	}
	return path
}
