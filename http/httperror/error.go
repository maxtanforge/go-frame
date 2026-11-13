// Package httperror defines explicit public HTTP errors shared by request and response adapters.
package httperror

// Error exposes Message to clients. Keep sensitive details in Err.
// Error() may expose the wrapped cause; never forward err.Error() to clients —
// map through respond.Adapter or read Message directly.
// Status outside 400..599 is a programming error and produces a safe 500 response.
// For valid Status, Code defaults to Status.
type Error struct {
	Status  int
	Code    int
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
