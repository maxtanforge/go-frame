package entdb_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/stretchr/testify/require"
)

// Local sentinel errors stand in for generated Ent error checks: the classifier
// consumes func(error) bool predicates, so the contract under test (ordering,
// wrapping and cancellation priority) is identical without coupling the
// library's tests to consumer-generated code.
var (
	errNotFound   = errors.New("not found")
	errConstraint = errors.New("constraint")
	errValidation = errors.New("validation")
	errUnique     = errors.New("unique conflict")
)

func fullClassifier() entdb.ErrorClassifier {
	return entdb.ErrorClassifier{
		IsNotFound:         func(err error) bool { return errors.Is(err, errNotFound) },
		IsConstraint:       func(err error) bool { return errors.Is(err, errConstraint) },
		IsValidation:       func(err error) bool { return errors.Is(err, errValidation) },
		IsUniqueConstraint: func(err error) bool { return errors.Is(err, errUnique) },
	}
}

func TestErrorClassifier(t *testing.T) {
	c := fullClassifier()
	for _, tt := range []struct {
		name string
		err  error
		want entdb.ErrorKind
	}{
		{"nil", nil, entdb.ErrorNone},
		{"unknown", errors.New("duplicate unique constraint"), entdb.ErrorUnknown},
		{"not found", fmt.Errorf("wrapped: %w", errNotFound), entdb.ErrorNotFound},
		{"constraint", fmt.Errorf("wrapped: %w", errConstraint), entdb.ErrorConstraint},
		{"validation", fmt.Errorf("wrapped: %w", errValidation), entdb.ErrorValidation},
		{"unique precedes constraint", errors.Join(errUnique, errConstraint), entdb.ErrorUniqueConstraint},
		{"canceled", errors.Join(errNotFound, context.Canceled), entdb.ErrorCanceled},
		{"deadline", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), entdb.ErrorDeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.want, c.Classify(tt.err)) })
	}
	// A missing unique predicate must fall through to the general constraint.
	partial := fullClassifier()
	partial.IsUniqueConstraint = nil
	require.Equal(t, entdb.ErrorConstraint, partial.Classify(errors.Join(errUnique, errConstraint)))
	require.Equal(t, entdb.ErrorUnknown, (entdb.ErrorClassifier{}).Classify(errUnique))
	require.Equal(t, entdb.ErrorConstraint, (entdb.ErrorClassifier{IsConstraint: fullClassifier().IsConstraint}).Classify(errConstraint))
}

func TestErrorClassifierCancellationSkipsPredicates(t *testing.T) {
	check := func(error) bool { t.Fatal("predicate called"); return true }
	c := entdb.ErrorClassifier{IsNotFound: check, IsConstraint: check, IsValidation: check, IsUniqueConstraint: check}
	require.Equal(t, entdb.ErrorNone, c.Classify(nil))
	require.Equal(t, entdb.ErrorCanceled, c.Classify(fmt.Errorf("wrapped: %w", context.Canceled)))
	require.Equal(t, entdb.ErrorDeadlineExceeded, c.Classify(context.DeadlineExceeded))
}
