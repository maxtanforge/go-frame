package background

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/maxtanforge/go-frame/concurrency"
)

func workerBudget(t *testing.T) *concurrency.Executor {
	t.Helper()
	budget, err := concurrency.NewExecutor(concurrency.ExecutorOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = budget.Close() })
	return budget
}

type fakeQueue struct {
	received  bool
	ack, nack atomic.Int32
	done      chan struct{}
	settleErr error
	bounded   bool
}

func TestWorkerRejectsTypedNilQueue(t *testing.T) {
	var queue *fakeQueue
	worker, err := NewWorker(queue, workerBudget(t), func(context.Context, Message) error { return nil })
	if worker != nil || err == nil {
		t.Fatalf("accepted typed nil queue: worker=%v err=%v", worker, err)
	}
}

func (q *fakeQueue) Receive(ctx context.Context) (Message, error) {
	if q.received {
		<-ctx.Done()
		return Message{}, ctx.Err()
	}
	q.received = true
	return Message{ID: "1", Body: []byte("hello")}, nil
}
func (q *fakeQueue) Ack(ctx context.Context, _ Message) error {
	q.ack.Add(1)
	close(q.done)
	return ctx.Err()
}
func (q *fakeQueue) Nack(ctx context.Context, _ Message) error {
	q.nack.Add(1)
	_, q.bounded = ctx.Deadline()
	close(q.done)
	return errors.Join(ctx.Err(), q.settleErr)
}

func TestWorkerSettlesBeforeStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, fail := range []bool{false, true} {
			q := &fakeQueue{done: make(chan struct{})}
			want := errors.New("business failed")
			worker, e := NewWorker(q, workerBudget(t), func(context.Context, Message) error {
				if fail {
					return want
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			finished := make(chan error, 1)
			go func() { finished <- worker.Run() }()
			select {
			case <-q.done:
			case <-time.After(time.Second):
				t.Fatal("not settled")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			e = worker.Shutdown(ctx)
			cancel()
			if e != nil {
				t.Fatal(e)
			}
			if e = <-finished; e != nil {
				t.Fatal(e)
			}
			if fail && q.nack.Load() != 1 || !fail && q.ack.Load() != 1 {
				t.Fatalf("ack=%v nack=%v", q.ack.Load(), q.nack.Load())
			}
		}
	})
}
func TestWorkerSettlesWithFreshBoundedContextOnStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := &fakeQueue{done: make(chan struct{})}
		started := make(chan struct{})
		worker, e := NewWorker(q, workerBudget(t), func(ctx context.Context, _ Message) error { close(started); <-ctx.Done(); return ctx.Err() })
		if e != nil {
			t.Fatal(e)
		}
		finished := make(chan error, 1)
		go func() { finished <- worker.Run() }()
		<-started
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e = worker.Shutdown(ctx); e != nil {
			t.Fatal(e)
		}
		if e = <-finished; e != nil {
			t.Fatal(e)
		}
		if q.nack.Load() != 1 {
			t.Fatal("unsettled canceled delivery")
		}
		if !q.bounded {
			t.Fatal("settlement context must have a deadline")
		}
	})
}

func TestWorkerRetainsProcessingFailureOnStop(t *testing.T) {
	processingFailure := errors.New("processing failed during stop")
	settlementFailure := errors.New("nack failed")
	for _, tc := range []struct {
		name       string
		processErr func(context.Context) error
		settleErr  error
		want       error
	}{
		{"pure wrapped cancellation", func(ctx context.Context) error { return fmt.Errorf("stopped: %w", ctx.Err()) }, nil, nil},
		{"joined failure", func(ctx context.Context) error { return errors.Join(ctx.Err(), processingFailure) }, nil, processingFailure},
		{"wrapped joined failure", func(ctx context.Context) error {
			return fmt.Errorf("process: %w", errors.Join(ctx.Err(), processingFailure))
		}, nil, processingFailure},
		{"independent failure", func(context.Context) error { return processingFailure }, nil, processingFailure},
		{"processing and settlement failure", func(ctx context.Context) error { return errors.Join(ctx.Err(), processingFailure) }, settlementFailure, processingFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				q := &fakeQueue{done: make(chan struct{}), settleErr: tc.settleErr}
				started := make(chan struct{})
				worker, err := NewWorker(q, workerBudget(t), func(ctx context.Context, _ Message) error {
					close(started)
					<-ctx.Done()
					return tc.processErr(ctx)
				})
				if err != nil {
					t.Fatal(err)
				}
				finished := make(chan error, 1)
				go func() { finished <- worker.Run() }()
				<-started
				shutdownErr := worker.Shutdown(context.Background())
				runErr := <-finished
				for _, err := range []error{shutdownErr, runErr} {
					if tc.want == nil && err != nil {
						t.Fatalf("pure cancellation must stop normally: %v", err)
					}
					if tc.want != nil && !errors.Is(err, tc.want) {
						t.Fatalf("lost processing diagnostic: %v", err)
					}
					if tc.settleErr != nil && !errors.Is(err, tc.settleErr) {
						t.Fatalf("lost settlement diagnostic: %v", err)
					}
				}
				if q.nack.Load() != 1 || !q.bounded {
					t.Fatalf("nack=%d bounded=%v", q.nack.Load(), q.bounded)
				}
			})
		})
	}
}
