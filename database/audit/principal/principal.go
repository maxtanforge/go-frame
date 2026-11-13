// Package principal converts trusted request identities to explicit audit actors.
package principal

import (
	"context"
	"github.com/maxtanforge/go-frame/auth/principal"
	"github.com/maxtanforge/go-frame/database/audit"
)

// FromContext never falls back to a system or anonymous actor.
func FromContext(ctx context.Context) (audit.Actor, error) {
	p, err := principal.Require(ctx)
	if err != nil {
		return audit.Actor{}, err
	}
	actor := audit.Actor{Kind: audit.Subject, ID: p.ID, Name: p.Name}
	if err := actor.Validate(); err != nil {
		return audit.Actor{}, err
	}
	return actor, nil
}
