package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	entdb "github.com/maxtanforge/go-frame/database/ent"
	"github.com/maxtanforge/go-frame/examples/business/internal/category"
)

// Category selects persistence fields independently of HTTP response DTOs.
type Category struct{ ent.Schema }

func (Category) Mixin() []ent.Mixin { return []ent.Mixin{entdb.IDMixin{}} }
func (Category) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("parent_id").Positive().Immutable(),
		field.String("owner").NotEmpty().MaxLen(128).Immutable(),
		field.String("name").MaxLen(100).Validate(category.ValidateName),
		field.String("note").MaxLen(1000).Optional().Nillable().Validate(category.ValidateNote),
		field.Bool("system").Default(false).Immutable(),
	}
}
func (Category) Edges() []ent.Edge {
	return []ent.Edge{edge.From("book", Book.Type).Ref("categories").Field("parent_id").Unique().Required().Immutable()}
}
func (Category) Indexes() []ent.Index { return []ent.Index{index.Fields("parent_id", "owner", "id")} }
func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "business_categories"}}
}
