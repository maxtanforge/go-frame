// Package transactionhttp is an example-only comparison of explicit and typed
// route composition for an independent transaction pagination contract.
package transactionhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/examples/contracts/endpoint"
	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/internal/isnil"
	"github.com/maxtanforge/go-frame/pagination"
	operationhttp "github.com/maxtanforge/go-frame/telemetry/operation/http"
	"github.com/maxtanforge/go-frame/validation"
)

// Business errors are mapped by cause, never by their diagnostic text.
var (
	ErrInvalid   = errors.New("transaction: invalid input")
	ErrForbidden = errors.New("transaction: access denied")
	ErrNotFound  = errors.New("transaction: account not found")
)

// Query contains business values, without HTTP DTOs or request objects.
type Query struct {
	Account int64
	Window  pagination.Window
	Sort    string
}

// Transaction is a business snapshot; the HTTP response selects public fields.
type Transaction struct {
	ID     int64
	Amount int64
	Owner  string
}

// Result includes an explicitly obtained total, without assuming a shared
// database snapshot between the count and records queries.
type Result struct {
	Records []Transaction
	Total   int64
}

// Service owns account access checks and explicitly chooses its query executor
// and transaction scope. This HTTP example supplies no persistence implementation.
type Service interface {
	List(context.Context, string, Query) (Result, error)
}

type accountPath struct{ Account string }

func parseAccount(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, ErrInvalid
	}
	return id, nil
}

func (p accountPath) Validate(c *validation.Context) {
	if c.Valid("Account") {
		if _, err := parseAccount(p.Account); err != nil {
			c.Add("Account", "canonical_id", "account must be a canonical positive integer")
		}
	}
}

type pageQuery struct {
	Current int64  `d:"1" v:"gte=1"`
	Size    int64  `d:"20" v:"gte=1,lte=100"`
	Sort    string `d:"created_at"`
}

func (q pageQuery) Validate(c *validation.Context) {
	if c.Valid("Current") && c.Valid("Size") {
		if _, err := pagination.NewWindow(q.Current, q.Size, pagination.DefaultLimits()); err != nil {
			c.Add("Current", "window", "pagination exceeds allowed window")
		}
	}
	if c.Valid("Sort") && q.Sort != "created_at" && q.Sort != "amount" {
		c.Add("Sort", "sort", "sort must be created_at or amount")
	}
}

// transactionResponse is the explicit public whitelist. Owner stays private;
// transaction IDs remain exact decimal strings even above JavaScript precision.
type transactionResponse struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
}

// Routes prepares an explicit binding declaration. Pass the resulting routes to
// router.NewAuthenticated with the application's verifier to enforce protection.
func Routes(service Service) ([]router.Route, error) { return routes(service, false) }

// TypedRoutes uses the example-only helper with the same callback and policy.
func TypedRoutes(service Service) ([]router.Route, error) { return routes(service, true) }

func routes(service Service, typed bool) ([]router.Route, error) {
	if isnil.Check(service) {
		return nil, errors.New("transaction service required")
	}
	opts := binding.Options{MapError: httpError, PathParams: func(r *http.Request) map[string]string {
		return map[string]string{"account": chi.URLParam(r, "account")}
	}}
	list := func(ctx context.Context, path *accountPath, input *pageQuery) (*pagination.Page[transactionResponse], error) {
		p, err := principal.Require(ctx)
		if err != nil {
			return nil, err
		}
		account, err := parseAccount(path.Account)
		if err != nil {
			return nil, err
		}
		window, err := pagination.NewWindow(input.Current, input.Size, pagination.DefaultLimits())
		if err != nil {
			return nil, ErrInvalid
		}
		result, err := service.List(ctx, p.ID, Query{Account: account, Window: window, Sort: input.Sort})
		if err != nil {
			return nil, err
		}
		rows := make([]transactionResponse, len(result.Records))
		for i, record := range result.Records {
			rows[i] = transactionResponse{ID: strconv.FormatInt(record.ID, 10), Amount: record.Amount}
		}
		page, err := pagination.NewPage(rows, window, result.Total)
		if err != nil {
			return nil, err
		}
		return &page, nil
	}
	policy := router.Route{Method: http.MethodGet, Path: "/accounts/{account}/transactions", Operation: operationhttp.Metadata{Action: "transaction.list", ResourceType: "transaction"}}
	if typed {
		return router.Collect("transactions", endpoint.PathWithQuery(policy, list, opts))
	}
	return router.Collect("transactions", policy.Bind(binding.PathWithQuery(list, opts)))
}

func httpError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	status, code, message := 500, 50001, "internal server error"
	switch {
	case errors.Is(err, ErrInvalid):
		status, code, message = 400, 40001, "invalid transaction input"
	case errors.Is(err, principal.ErrUnauthenticated):
		status, code, message = 401, 40101, "authentication required"
	case errors.Is(err, ErrForbidden):
		status, code, message = 403, 40301, "transaction access denied"
	case errors.Is(err, ErrNotFound):
		status, code, message = 404, 40401, "account not found"
	}
	return &httperror.Error{Status: status, Code: code, Message: message, Err: err}
}
