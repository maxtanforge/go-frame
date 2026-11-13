package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/maxtanforge/go-frame/examples/ent/ent"
	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/httperror"
	"github.com/maxtanforge/go-frame/http/respond"
	"github.com/maxtanforge/go-frame/http/router"
	"github.com/maxtanforge/go-frame/pagination"
)

type detailInput struct {
	ID int64 `v:"gt=0" label:"商品 ID"`
}
type deleteResponse struct {
	Deleted bool `json:"deleted"`
}

func Register(r chi.Router, s Service) error {
	if s.DB == nil || s.sorter == nil {
		return errors.New("product service: use NewService before registration")
	}
	pathOptions := binding.Options{MapError: httpError, PathParams: func(r *http.Request) map[string]string { return map[string]string{"id": chi.URLParam(r, "id")} }}
	inputOptions := binding.Options{MapError: httpError}
	declarations, err := router.Collect("products",
		router.Route{Method: "POST", Path: "/products"}.Bind(binding.JSON(func(ctx context.Context, in *CreateProduct) (*productResponse, error) {
			p, err := s.Create(ctx, *in)
			if err != nil {
				return nil, err
			}
			out := response(p)
			return &out, nil
		}, inputOptions)),
		router.Route{Method: "GET", Path: "/products"}.Bind(binding.Query(func(ctx context.Context, in *ListProducts) (*pagination.Page[productResponse], error) {
			page, err := s.List(ctx, *in)
			if err != nil {
				return nil, err
			}
			records := make([]productResponse, len(page.Records))
			for i, p := range page.Records {
				records[i] = response(p)
			}
			return &pagination.Page[productResponse]{Current: page.Current, Size: page.Size, Total: page.Total, Pages: page.Pages, Records: records}, nil
		}, inputOptions)),
		router.Route{Method: "GET", Path: "/products/{id}"}.Bind(binding.Path(func(ctx context.Context, in *detailInput) (*productResponse, error) {
			p, err := s.Find(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			out := response(p)
			return &out, nil
		}, pathOptions)),
		router.Route{Method: "PATCH", Path: "/products/{id}"}.Bind(binding.JSONWithPath(func(ctx context.Context, path *detailInput, in *PatchProduct) (*productResponse, error) {
			p, err := s.Patch(ctx, path.ID, *in)
			if err != nil {
				return nil, err
			}
			out := response(p)
			return &out, nil
		}, pathOptions)),
		router.Route{Method: "DELETE", Path: "/products/{id}"}.Bind(binding.Path(func(ctx context.Context, in *detailInput) (*deleteResponse, error) {
			if err := s.Delete(ctx, in.ID); err != nil {
				return nil, err
			}
			return &deleteResponse{Deleted: true}, nil
		}, pathOptions)),
	)
	if err != nil {
		return err
	}
	for _, declaration := range declarations {
		r.Method(declaration.Method, declaration.Path, declaration.Handler)
	}
	return nil
}
func httpError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return &httperror.Error{Status: 404, Message: "product not found", Err: err}
	case errors.Is(err, ErrInvalid):
		return &httperror.Error{Status: 400, Message: "invalid product parameters", Err: err}
	default:
		return err
	}
}

type productResponse struct {
	ID            int64              `json:"id,string"`
	Name          string             `json:"name"`
	Price         int64              `json:"price"`
	Active        bool               `json:"active"`
	Note          *string            `json:"note"`
	CreatedAt     respond.MillisTime `json:"created_at"`
	UpdatedAt     respond.MillisTime `json:"updated_at"`
	CreatedBy     string             `json:"created_by"`
	UpdatedBy     string             `json:"updated_by"`
	CreatedByName string             `json:"created_by_name"`
	UpdatedByName string             `json:"updated_by_name"`
}

func response(p *ent.Product) productResponse {
	return productResponse{ID: p.ID, Name: p.Name, Price: p.Price, Active: p.Active, Note: p.Note, CreatedAt: respond.MillisTime(p.CreatedAt), UpdatedAt: respond.MillisTime(p.UpdatedAt), CreatedBy: p.CreatedBy, UpdatedBy: p.UpdatedBy, CreatedByName: p.CreatedByName, UpdatedByName: p.UpdatedByName}
}
