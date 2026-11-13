// Package txcleanup shares SQL and Ent transaction cleanup error policy.
package txcleanup

import "database/sql"

// OnlyCompleted recognizes a single wrapping chain ending in sql.ErrTxDone.
// Compound errors remain diagnostic failures, even when one branch is completed.
func OnlyCompleted(err error) bool {
	for err != nil {
		if err == sql.ErrTxDone { //nolint:errorlint // Exact sentinel identity prevents hiding independent joined failures.
			return true
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = wrapped.Unwrap()
	}
	return false
}
