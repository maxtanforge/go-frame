package record

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fixedIDs struct {
	value int64
	err   error
}

func (g fixedIDs) NextContext(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return g.value, g.err
}

var testIDs = fixedIDs{value: 9007199254740993}

func TestCreateUsesInjectedIDWithoutJSONPrecisionLoss(t *testing.T) {
	f := &fakeStore{}
	r, err := (Service{Transactions: f, IDs: testIDs}).Create(context.Background(), "hello", testActor)
	if err != nil || r.ID != testIDs.value {
		t.Fatal(r, err)
	}
	data, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(data), `"id":"9007199254740993"`) {
		t.Fatal(string(data), err)
	}
}

func TestIDFailureDoesNotStartTransaction(t *testing.T) {
	failure := errors.New("clock moved backwards")
	for _, ids := range []IDGenerator{nil, fixedIDs{err: failure}, fixedIDs{value: 0}, fixedIDs{value: -1}} {
		f := &fakeStore{}
		_, err := (Service{Transactions: f, IDs: ids}).Create(context.Background(), "hello", testActor)
		if err == nil || len(f.events) != 0 {
			t.Fatal("ID failure entered transaction", err, f.events)
		}
		if g, ok := ids.(fixedIDs); ok && g.err != nil && !errors.Is(err, failure) {
			t.Fatal("cause lost", err)
		}
	}
}
