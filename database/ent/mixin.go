package entdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
	"github.com/maxtanforge/go-frame/database/audit"
	auditprincipal "github.com/maxtanforge/go-frame/database/audit/principal"
	"github.com/maxtanforge/go-frame/internal/isnil"
)

// IDMixin declares application-generated positive int64 IDs. Install NewIDHook
// or NewMutationHook on the entity client; database auto-increment is disabled.
type IDMixin struct{ mixin.Schema }

// Fields declares an immutable ID serialized as a JSON decimal string.
func (IDMixin) Fields() []ent.Field {
	incremental := false
	return []ent.Field{field.Int64("id").Positive().Immutable().StructTag(`json:"id,string"`).Annotations(entsql.Annotation{Incremental: &incremental})}
}

// CreatedMixin declares immutable creation metadata. Register NewAuditHook once.
// Do not combine it with AuditMixin. It does not make the entire entity immutable.
type CreatedMixin struct{ mixin.Schema }

// Fields declares creation time and explicit actor identity.
func (CreatedMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").SchemaType(auditTimeTypes()).Immutable().StructTag(`json:"created_at"`),
		field.String("created_by").MaxLen(128).Immutable().StructTag(`json:"created_by"`),
		field.String("created_by_kind").MaxLen(16).Immutable().StructTag(`json:"created_by_kind"`),
		field.String("created_by_name").MaxLen(128).Immutable().StructTag(`json:"created_by_name"`),
	}
}

// AuditMixin stores creation and last-update metadata. Register NewAuditHook once. Actor identity and kind preserve explicit system,
// subject and anonymous identities; a missing actor is never inferred.
type AuditMixin struct{ mixin.Schema }

// Fields declares creation metadata immutable and update metadata mutable.
func (AuditMixin) Fields() []ent.Field {
	created, updated := (CreatedMixin{}).Fields(), updatedFields()
	return []ent.Field{created[0], updated[0], created[1], updated[1], created[2], updated[2], created[3], updated[3]}
}

func updatedFields() []ent.Field {
	return []ent.Field{
		field.Time("updated_at").SchemaType(auditTimeTypes()).StructTag(`json:"updated_at"`),
		field.String("updated_by").MaxLen(128).StructTag(`json:"updated_by"`),
		field.String("updated_by_kind").MaxLen(16).StructTag(`json:"updated_by_kind"`),
		field.String("updated_by_name").MaxLen(128).StructTag(`json:"updated_by_name"`),
	}
}

func auditTimeTypes() map[string]string {
	return map[string]string{dialect.MySQL: "datetime(3)", dialect.Postgres: "timestamptz(3)"}
}

// IDGenerator supplies application-generated IDs and must support concurrent use.
type IDGenerator interface {
	NextContext(context.Context) (int64, error)
}

// AuditProfile explicitly selects the schema's audit field set.
type AuditProfile uint8

const (
	// AuditFull pairs with AuditMixin and fills creation and update metadata.
	AuditFull AuditProfile = iota + 1
	// AuditCreateOnly pairs with CreatedMixin. Updates preserve creation metadata
	// without resolving an actor or adding update fields; deletes pass through.
	AuditCreateOnly
)

// AuditOptions supplies an optional field profile, actor resolver and clock.
// Actor defaults to the verified Principal; Now defaults to time.Now.
// Dependencies must be concurrency safe. Jobs supply an explicit system resolver.
type AuditOptions struct {
	Profile AuditProfile
	Actor   func(context.Context) (audit.Actor, error)
	Now     func() time.Time
}

// MutationOptions supplies dependencies for the IDMixin + AuditMixin convenience
// hook. IDs are required; Actor defaults to Principal and Now to time.Now.
type MutationOptions struct {
	IDs   IDGenerator
	Actor func(context.Context) (audit.Actor, error)
	Now   func() time.Time
}

// NewMutationHook composes NewAuditHook(AuditFull) and NewIDHook. Install only on
// explicitly selected entity clients containing IDMixin and AuditMixin.
// Register last among runtime hooks; subsequent schema hooks must not overwrite
// system metadata. Raw SQL and upsert conflict-update paths are not covered.
func NewMutationHook(opts MutationOptions) (ent.Hook, error) {
	ids, err := NewIDHook(opts.IDs)
	if err != nil {
		return nil, err
	}
	auditHook, err := NewAuditHook(AuditOptions{Profile: AuditFull, Actor: opts.Actor, Now: opts.Now})
	if err != nil {
		return nil, err
	}
	return func(next ent.Mutator) ent.Mutator { return ids(auditHook(next)) }, nil
}

// NewIDHook fills only the int64 ID on create, always replacing caller input.
// Other operations preserve the ID (including UpdateOne's selector). It requires
// no audit fields or identity. Generated IDs can be consumed by failed writes.
func NewIDHook(ids IDGenerator) (ent.Hook, error) {
	if isnil.Check(ids) {
		return nil, errors.New("entdb: ID generator is required")
	}
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if err := validateMutation(ctx, m); err != nil {
				return nil, err
			}
			if m.Op() == ent.OpCreate {
				setter, ok := m.(interface{ SetID(int64) })
				if !ok {
					return nil, errors.New("entdb: mutation requires an int64 ID")
				}
				id, err := ids.NextContext(ctx)
				if err != nil {
					return nil, err
				}
				if id <= 0 {
					return nil, errors.New("entdb: generated ID must be positive")
				}
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				setter.SetID(id)
			}
			return next.Mutate(ctx, m)
		})
	}, nil
}

// NewAuditHook fills standard audit fields; it never sets IDs.
// A zero Profile identifies CreatedMixin/AuditMixin through generated mutation
// interfaces, so the hook can be registered once with client.Use. Other schemas
// pass through. Explicit profiles require matching fields.
// Creates replace supplied metadata. Updates discard attempted creation metadata
// changes and, in AuditFull mode, replace update metadata, including bulk updates.
// AuditCreateOnly permits updates and deletes: use separate policies for append-only
// entities. Delete operations resolve neither actors nor clocks.
//
// Timestamps are UTC milliseconds. Metadata uses the current mutation/transaction;
// no extra transaction or I/O is performed. Register last among runtime hooks and
// ensure subsequent schema hooks do not overwrite system fields. Raw SQL and upsert
// conflict-update paths bypass these guarantees. This is not authorization,
// chronological-history enforcement, optimistic locking or a deletion history.
func NewAuditHook(opts AuditOptions) (ent.Hook, error) {
	if opts.Profile != 0 && opts.Profile != AuditFull && opts.Profile != AuditCreateOnly {
		return nil, errors.New("entdb: invalid audit profile")
	}
	if opts.Actor == nil {
		opts.Actor = auditprincipal.FromContext
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if err := validateMutation(ctx, m); err != nil {
				return nil, err
			}
			if m.Op() == ent.OpDelete || m.Op() == ent.OpDeleteOne {
				return next.Mutate(ctx, m)
			}
			profile := opts.Profile
			if profile == 0 {
				profile = mutationAuditProfile(m)
				if profile == 0 {
					return next.Mutate(ctx, m)
				}
			}
			if m.Op() != ent.OpCreate {
				for _, name := range []string{"created_at", "created_by", "created_by_kind", "created_by_name"} {
					if err := m.ResetField(name); err != nil {
						return nil, fmt.Errorf("entdb: reset %s: %w", name, err)
					}
				}
				if profile == AuditCreateOnly {
					return next.Mutate(ctx, m)
				}
			}
			actor, err := opts.Actor(ctx)
			if err != nil {
				return nil, err
			}
			metadata, err := audit.New(actor, opts.Now())
			if err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if m.Op() == ent.OpCreate {
				if err := setAuditFields(m, "created", metadata.CreatedAt, actor); err != nil {
					return nil, err
				}
			}
			if profile == AuditFull {
				if err := setAuditFields(m, "updated", metadata.UpdatedAt, actor); err != nil {
					return nil, err
				}
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return next.Mutate(ctx, m)
		})
	}, nil
}

// Generated Ent mutations expose these methods even before values are set.
type createdAuditMutation interface {
	CreatedAt() (time.Time, bool)
	CreatedBy() (string, bool)
	CreatedByKind() (string, bool)
	CreatedByName() (string, bool)
}
type updatedAuditMutation interface {
	UpdatedAt() (time.Time, bool)
	UpdatedBy() (string, bool)
	UpdatedByKind() (string, bool)
	UpdatedByName() (string, bool)
}

func mutationAuditProfile(m ent.Mutation) AuditProfile {
	if _, ok := m.(createdAuditMutation); !ok {
		return 0
	}
	if _, ok := m.(updatedAuditMutation); ok {
		return AuditFull
	}
	return AuditCreateOnly
}

func setAuditFields(m ent.Mutation, prefix string, at time.Time, actor audit.Actor) error {
	for _, f := range []struct {
		name  string
		value any
	}{
		{prefix + "_at", at}, {prefix + "_by", actor.ID}, {prefix + "_by_kind", string(actor.Kind)}, {prefix + "_by_name", actor.Name},
	} {
		if err := m.SetField(f.name, f.value); err != nil {
			return fmt.Errorf("entdb: set %s: %w", f.name, err)
		}
	}
	return nil
}

// HookClient is implemented by generated Ent entity clients.
type HookClient interface{ Use(...ent.Hook) }

// InstallHooks registers hooks, in order, on explicitly listed entity clients.
// All nil hooks and clients (including typed nil clients) are rejected before
// registration starts. Call during startup before concurrent client use; list only
// schemas matching the hooks. This helper does not discover schemas or global clients.
func InstallHooks(hooks []ent.Hook, clients ...HookClient) error {
	for _, h := range hooks {
		if h == nil {
			return errors.New("entdb: hook is required")
		}
	}
	for _, c := range clients {
		if isnil.Check(c) {
			return errors.New("entdb: entity client is required")
		}
	}
	for _, c := range clients {
		c.Use(hooks...)
	}
	return nil
}

func validateMutation(ctx context.Context, m ent.Mutation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if isnil.Check(m) {
		return errors.New("entdb: mutation is required")
	}
	switch m.Op() {
	case ent.OpCreate, ent.OpUpdate, ent.OpUpdateOne, ent.OpDelete, ent.OpDeleteOne:
		return nil
	default:
		return errors.New("entdb: unsupported mutation operation")
	}
}
