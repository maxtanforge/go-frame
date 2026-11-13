// Package categoryhttp adapts the category feature to Frame's HTTP boundary.
package categoryhttp

import (
	"context"

	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
	"github.com/maxtanforge/go-frame/pagination"
)

// Handler maps validated transport inputs to business use cases.
type Handler struct {
	service  category.Service
	catalog  *category.Catalog
	creation *creation
}

func actor(ctx context.Context) string {
	p, ok := principal.FromContext(ctx)
	if !ok {
		return ""
	}
	return p.ID
}

func (h Handler) Detail(ctx context.Context, path *detailPath) (*CategoryVO, error) {
	id, err := category.ParseID(path.ID)
	if err != nil {
		return nil, err
	}
	model, err := h.service.Find(ctx, actor(ctx), id)
	if err != nil {
		return nil, err
	}
	return public(model), nil
}

func (h Handler) Patch(ctx context.Context, path *detailPath, body *patchDTO) (*CategoryVO, error) {
	id, err := category.ParseID(path.ID)
	if err != nil {
		return nil, err
	}
	model, err := h.service.Patch(ctx, actor(ctx), body.command(id))
	if err != nil {
		return nil, err
	}
	return public(model), nil
}

func (h Handler) List(ctx context.Context, path *createPath, q *listDTO) (*pagination.Page[*CategoryVO], error) {
	id, err := category.ParseID(path.ParentID)
	if err != nil {
		return nil, err
	}
	result, err := h.catalog.List(ctx, actor(ctx), category.ListQuery{ParentID: id, Current: q.Current, Size: q.Size})
	if err != nil {
		return nil, err
	}
	records := make([]*CategoryVO, len(result.Records))
	for i, m := range result.Records {
		records[i] = public(m)
	}
	window, err := pagination.NewWindow(result.Current, result.Size, pagination.DefaultLimits())
	if err != nil {
		return nil, err
	}
	page, err := pagination.NewPage(records, window, result.Total)
	return &page, err
}

func (h Handler) Delete(ctx context.Context, path *detailPath) (*DeleteVO, error) {
	id, err := category.ParseID(path.ID)
	if err != nil {
		return nil, err
	}
	if err := h.catalog.Delete(ctx, actor(ctx), id); err != nil {
		return nil, err
	}
	return &DeleteVO{Deleted: true}, nil
}
