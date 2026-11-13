package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	entdb "github.com/maxtanforge/go-frame/database/ent"
)

// Book is an existing account boundary; this example does not expose book CRUD.
type Book struct{ ent.Schema }

func (Book) Mixin() []ent.Mixin { return []ent.Mixin{entdb.IDMixin{}} }
func (Book) Fields() []ent.Field {
	return []ent.Field{field.String("owner").NotEmpty().MaxLen(128).Immutable()}
}
func (Book) Edges() []ent.Edge {
	return []ent.Edge{edge.To("categories", Category.Type)}
}
func (Book) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "business_books"}}
}
