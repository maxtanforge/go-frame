package middleware

import "net/http"

// MetricMethod returns one of the nine standard HTTP methods or _OTHER.
// HTTP methods are case-sensitive. Keep the original value in logs, not labels.
func MetricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return method
	default:
		return "_OTHER"
	}
}
