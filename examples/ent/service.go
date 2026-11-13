package main

import (
	"context"
	"errors"

	entsql "entgo.io/ent/dialect/sql"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/examples/ent/ent"
	"github.com/maxtanforge/go-frame/examples/ent/ent/product"
	"github.com/maxtanforge/go-frame/examples/ent/ent/productevent"
	"github.com/maxtanforge/go-frame/pagination"
)

var ErrNotFound = errors.New("product not found")
var ErrInvalid = errors.New("invalid product input")

type Service struct {
	DB     *ent.Client
	sorter *entdb.Sorter[product.OrderOption]
}

func NewService(db *ent.Client) (Service, error) {
	if db == nil {
		return Service{}, errors.New("product service: client is required")
	}
	sorter, err := entdb.NewSorter(map[string]entdb.SortOptions[product.OrderOption]{
		"id":    {Asc: product.ByID(entsql.OrderAsc()), Desc: product.ByID(entsql.OrderDesc())},
		"name":  {Asc: product.ByName(entsql.OrderAsc()), Desc: product.ByName(entsql.OrderDesc())},
		"price": {Asc: product.ByPrice(entsql.OrderAsc()), Desc: product.ByPrice(entsql.OrderDesc())},
	}, []pagination.Sort{{Field: "id"}})
	if err != nil {
		return Service{}, err
	}
	return Service{DB: db, sorter: sorter}, nil
}

var productErrors = entdb.ErrorClassifier{
	IsNotFound: ent.IsNotFound, IsConstraint: ent.IsConstraintError, IsValidation: ent.IsValidationError,
}

const systemActor = "ent-example"

func (s Service) Create(ctx context.Context, in CreateProduct) (*ent.Product, error) {
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	var p *ent.Product
	err := entdb.WithTx(ctx, s.DB.Tx, func(tx *ent.Tx) error {
		var err error
		p, err = tx.Product.Create().SetName(in.Name).SetPrice(in.Price).SetActive(in.Active).SetNillableNote(in.Note).Save(ctx)
		if err != nil {
			return err
		}
		return writeEvent(ctx, tx.Client(), p.ID, productevent.ActionCreated)
	})
	if err != nil {
		return nil, err
	}
	return p.Unwrap(), nil
}
func (s Service) Patch(ctx context.Context, id int64, in PatchProduct) (*ent.Product, error) {
	if id <= 0 {
		return nil, ErrInvalid
	}
	if err := validatePatch(in); err != nil {
		return nil, err
	}
	var p *ent.Product
	err := entdb.WithTx(ctx, s.DB.Tx, func(tx *ent.Tx) error {
		// Lock before changing fields; event writes and the update share this transaction.
		if _, err := tx.Product.Query().Where(product.IDEQ(id)).ForUpdate().Only(ctx); err != nil {
			return mapNotFound(err)
		}
		update := tx.Product.UpdateOneID(id)
		if in.Name.Present {
			update.SetName(in.Name.Value)
		}
		if in.Price.Present {
			update.SetPrice(in.Price.Value)
		}
		if in.Active.Present {
			update.SetActive(in.Active.Value)
		}
		if in.Note.Present {
			if in.Note.Null {
				update.ClearNote()
			} else {
				update.SetNote(in.Note.Value)
			}
		}
		var err error
		p, err = update.Save(ctx)
		if err != nil {
			return mapNotFound(err)
		}
		return writeEvent(ctx, tx.Client(), id, productevent.ActionUpdated)
	})
	if err != nil {
		return nil, err
	}
	return p.Unwrap(), nil
}
func (s Service) Find(ctx context.Context, id int64) (*ent.Product, error) {
	if id <= 0 {
		return nil, ErrInvalid
	}
	p, err := s.DB.Product.Get(ctx, id)
	return p, mapNotFound(err)
}
func (s Service) Delete(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalid
	}
	return entdb.WithTx(ctx, s.DB.Tx, func(tx *ent.Tx) error {
		if err := tx.Product.DeleteOneID(id).Exec(ctx); err != nil {
			return mapNotFound(err)
		}
		return writeEvent(ctx, tx.Client(), id, productevent.ActionDeleted)
	})
}
func (s Service) List(ctx context.Context, in ListProducts) (pagination.Page[*ent.Product], error) {
	w, err := pagination.NewWindow(in.Current, in.Size, pagination.DefaultLimits())
	if err != nil {
		return pagination.Page[*ent.Product]{}, ErrInvalid
	}
	sort := in.Sort
	if sort == "" {
		sort = "id"
	}
	order, err := s.sorter.Resolve([]pagination.Sort{{Field: sort, Desc: in.Desc}})
	if errors.Is(err, pagination.ErrInvalid) {
		return pagination.Page[*ent.Product]{}, ErrInvalid
	}
	if err != nil {
		return pagination.Page[*ent.Product]{}, err
	}
	query := s.DB.Product.Query().Order(order...)
	if in.Active != nil {
		query.Where(product.ActiveEQ(*in.Active))
	}
	return entdb.QueryPage(ctx, query, w)
}
func writeEvent(ctx context.Context, client *ent.Client, id int64, action productevent.Action) error {
	return client.ProductEvent.Create().SetProductID(id).SetAction(action).Exec(ctx)
}
func mapNotFound(err error) error {
	if productErrors.Classify(err) == entdb.ErrorNotFound {
		return errors.Join(ErrNotFound, err)
	}
	return err
}
