package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	entdb "github.com/maxtanforge/go-frame/database/ent"
)

// Events deliberately have no product foreign key: hard deletion retains history.
type ProductEvent struct{ ent.Schema }

func (ProductEvent) Mixin() []ent.Mixin { return []ent.Mixin{entdb.IDMixin{}, entdb.CreatedMixin{}} }
func (ProductEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("product_id").Positive().Immutable(),
		field.Enum("action").Values("created", "updated", "deleted").Immutable(),
	}
}
