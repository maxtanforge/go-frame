package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/examples/ent/internal/productvalue"
)

type Product struct{ ent.Schema }

func (Product) Mixin() []ent.Mixin { return []ent.Mixin{entdb.IDMixin{}, entdb.AuditMixin{}} }
func (Product) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(productvalue.NameMax).Validate(productvalue.Name),
		field.Int64("price").Validate(productvalue.Price),
		field.Bool("active"),
		field.String("note").MaxLen(productvalue.NoteMax).Optional().Nillable().Validate(productvalue.Note),
	}
}
func (Product) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Checks: map[string]string{"products_price_nonnegative": "price >= 0"}}}
}
