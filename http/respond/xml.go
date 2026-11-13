package respond

import (
	"encoding/xml"
	"fmt"
	"net/http"
)

// XML writes a native XML response without the JSON envelope. Serialization
// finishes before headers are changed; callers can pass errors to WriteError.
func XML(w http.ResponseWriter, r *http.Request, status int, value any) error {
	payload, err := xml.Marshal(value)
	if err != nil {
		return err
	}
	return writeDirect(w, r, status, "application/xml; charset=utf-8", payload)
}

// Text writes plain text without an envelope, for example a callback challenge.
func Text(w http.ResponseWriter, r *http.Request, status int, value string) error {
	return writeDirect(w, r, status, "text/plain; charset=utf-8", []byte(value))
}
func writeDirect(w http.ResponseWriter, r *http.Request, status int, media string, payload []byte) error {
	if err := requestContextError(r.Context()); err != nil {
		return err
	}
	if status < 200 || status > 599 {
		return fmt.Errorf("respond: invalid final status %d", status)
	}
	w.Header().Set("Content-Type", media)
	w.Header().Del("Content-Length")
	w.WriteHeader(status)
	_, err := w.Write(payload)
	return err
}
