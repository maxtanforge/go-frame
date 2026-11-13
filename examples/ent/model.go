package main

import (
	"github.com/maxtanforge/go-frame/examples/ent/internal/productvalue"
	"github.com/maxtanforge/go-frame/optional"
	"github.com/maxtanforge/go-frame/pagination"
	"github.com/maxtanforge/go-frame/validation"
)

type ListProducts struct {
	pagination.Input
	Active *bool
	Sort   string `d:"id" v:"required,max=32" label:"排序字段"`
	Desc   bool
}

type CreateProduct struct {
	Name   string  `json:"name" v:"present" label:"商品名称"`
	Price  int64   `json:"price" v:"present" label:"商品价格"`
	Active bool    `json:"active"`
	Note   *string `json:"note"`
}
type PatchProduct struct {
	Name   optional.Field[string] `json:"name"`
	Price  optional.Field[int64]  `json:"price"`
	Active optional.Field[bool]   `json:"active"`
	Note   optional.Field[string] `json:"note" nullable:"true"`
}

func (p CreateProduct) Validate(c *validation.Context) {
	validation.Check(c, "Name", p.Name, productvalue.Name)
	validation.Check(c, "Price", p.Price, productvalue.Price)
	if p.Note != nil {
		validation.Check(c, "Note", *p.Note, productvalue.Note)
	}
}
func (p PatchProduct) Validate(c *validation.Context) {
	if !p.Name.Present && !p.Price.Present && !p.Active.Present && !p.Note.Present {
		c.Add("", "required", "至少提供一个修改字段")
	}
	for _, name := range []string{"Name", "Price", "Active"} {
		if c.Null(name) && !c.Invalid(name) {
			c.Add(name, "required", "该字段不能为 null")
		}
	}
	validation.Check(c, "Name", p.Name.Value, productvalue.Name)
	validation.Check(c, "Price", p.Price.Value, productvalue.Price)
	validation.Check(c, "Note", p.Note.Value, productvalue.Note)
}

// Service callers supply typed values; request presence is not inferred from zeros.
func validateCreate(p CreateProduct) error {
	c := validation.NewContext(map[string]validation.FieldState{
		"Name":   {Path: validation.Path{"name"}, Present: true, Decoded: true},
		"Price":  {Path: validation.Path{"price"}, Present: true, Decoded: true},
		"Active": {Path: validation.Path{"active"}, Present: true, Decoded: true},
		"Note":   {Path: validation.Path{"note"}, Present: true, Null: p.Note == nil, Decoded: p.Note != nil},
	}, 4096)
	p.Validate(c)
	return c.Err()
}
func validatePatch(p PatchProduct) error {
	state := func(name string, present, null bool) validation.FieldState {
		return validation.FieldState{Path: validation.Path{name}, Present: present, Null: null, Decoded: present && !null}
	}
	c := validation.NewContext(map[string]validation.FieldState{
		"Name": state("name", p.Name.Present, p.Name.Null), "Price": state("price", p.Price.Present, p.Price.Null),
		"Active": state("active", p.Active.Present, p.Active.Null), "Note": state("note", p.Note.Present, p.Note.Null),
	}, 4096)
	p.Validate(c)
	return c.Err()
}
