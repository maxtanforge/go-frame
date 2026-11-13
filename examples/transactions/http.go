package transactionexample

import (
	"context"
	"errors"
	"net/http"

	"github.com/maxtanforge/go-frame/http/binding"
	"github.com/maxtanforge/go-frame/http/httperror"
)

type createDTO struct {
	SKU  string `json:"sku" v:"required"`
	Name string `json:"name" v:"required"`
}
type productResponse struct {
	SKU  string `json:"sku"`
	Name string `json:"name"`
}

// CreateHandler maps transport input and business errors explicitly.
func CreateHandler(service Service) (http.Handler, error) {
	return binding.JSON(func(ctx context.Context, in *createDTO) (*productResponse, error) {
		c := CreateCommand{SKU: in.SKU, Name: in.Name}
		if err := service.Create(ctx, c); err != nil {
			return nil, err
		}
		return &productResponse{SKU: c.SKU, Name: c.Name}, nil
	}, binding.Options{MapError: productError})
}

func productError(err error) error {
	if errors.Is(err, ErrInvalidProduct) {
		return &httperror.Error{Status: 400, Message: "Invalid product", Err: err}
	}
	return err
}

type categoryPath struct {
	Category string `v:"required"`
}
type listDTO struct {
	Limit int `d:"20" v:"gte=1,lte=100"`
}
type listResponse struct {
	Products []productResponse `json:"products"`
}

// ListHandler accepts a use-case function and the application's router extractor.
func ListHandler(list func(context.Context, ListQuery) ([]Product, error), pathParams func(*http.Request) map[string]string) (http.Handler, error) {
	if list == nil {
		return nil, errors.New("list use case is required")
	}
	return binding.PathWithQuery(func(ctx context.Context, p *categoryPath, q *listDTO) (*listResponse, error) {
		products, err := list(ctx, ListQuery{Category: p.Category, Limit: q.Limit})
		if err != nil {
			return nil, err
		}
		out := &listResponse{Products: make([]productResponse, 0, len(products))}
		for _, product := range products {
			out.Products = append(out.Products, productResponse{SKU: product.SKU, Name: product.Name}) //nolint:staticcheck // Keep an explicit response field whitelist as the business model evolves.
		}
		return out, nil
	}, binding.Options{PathParams: pathParams})
}
