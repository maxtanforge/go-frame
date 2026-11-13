// Package background illustrates consumer-owned queues and task registration.
// These types are example business code, not a universal queue contract.
package background

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/maxtanforge/go-frame/app"
	"github.com/maxtanforge/go-frame/concurrency"
	"github.com/maxtanforge/go-frame/telemetry/logger"
)

// Message is one queue delivery; Body format belongs to the business.
type Message struct {
	ID   string
	Body []byte
}

// Queue must honor contexts. Nack determines broker redelivery/dead-lettering;
// it must not implement an unconditional immediate requeue loop in production.
type Queue interface {
	Receive(context.Context) (Message, error)
	Ack(context.Context, Message) error
	Nack(context.Context, Message) error
}

// NewWorker processes one message at a time. Register named workers in Group
// for concurrency. Queue creation/Close belong to an application resource hook,
// so the client is closed only after workers finish Shutdown.
func NewWorker(queue Queue, budget *concurrency.Executor, process func(context.Context, Message) error) (*app.Task, error) {
	missingQueue := queue == nil
	if !missingQueue {
		value := reflect.ValueOf(queue)
		switch value.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
			missingQueue = value.IsNil()
		}
	}
	if missingQueue || budget == nil || process == nil {
		return nil, errors.New("worker requires queue, execution budget and processor")
	}
	return app.NewTask(func(ctx context.Context) error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			message, err := queue.Receive(ctx)
			if err != nil {
				return err
			}
			err = budget.Do(ctx, func(ctx context.Context) error { return process(ctx, message) })
			// Settlement must still work after processing was canceled, but its cleanup
			// budget is finite. Fit this budget inside App's shutdown/cleanup reserve.
			settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
			var settleErr error
			if err == nil {
				settleErr = queue.Ack(settleCtx, message)
			} else {
				settleErr = queue.Nack(settleCtx, message)
			}
			cancel()
			if settleErr != nil {
				return errors.Join(err, fmt.Errorf("settle delivery: %w", settleErr))
			}
			if ctx.Err() != nil {
				// Successful settlement does not erase processing failures. Task
				// treats only a pure cancellation chain as normal shutdown.
				return err
			}
			if err != nil {
				logger.FromContext(ctx).ErrorContext(ctx, "message processing failed", "message_id", message.ID, "error", err)
			}
		}
	})
}
