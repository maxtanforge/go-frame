package app

import (
	"fmt"
	"runtime/debug"
)

// PanicError retains a panic recovered at an App/Group Run/Shutdown/Close,
// Task, Hook, Cleanup, or OnShutdown boundary. Use
// errors.As to obtain the diagnostic through service names and joined errors.
// Stack and Value may contain application data; choose an appropriate log sink.
type PanicError struct {
	value any
	stack []byte
}

// Error reports the panic value without automatically including the stack.
func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.value) }

// Value returns the original panic value. Treat any referenced data as read-only.
func (e *PanicError) Value() any { return e.value }

// Stack returns a copy of the stack captured at recovery, including the panic site.
func (e *PanicError) Stack() []byte { return append([]byte(nil), e.stack...) }

// Unwrap preserves errors.Is when the recovered value implements error.
func (e *PanicError) Unwrap() error {
	cause, _ := e.value.(error)
	return cause
}

func runSafely(run func() error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &PanicError{value: value, stack: debug.Stack()}
		}
	}()
	return run()
}
