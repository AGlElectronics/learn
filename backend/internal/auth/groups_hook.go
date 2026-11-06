package auth

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// GroupsHook is told the IdP groups of the authenticated user. Learn uses
// it to refresh its group cache and group-synced enrolments, so leaving or
// joining a group applies on the person's next request.
type GroupsHook func(ctx context.Context, userID uuid.UUID, groups []string) error

// SetGroupsHook installs the hook. Call before serving requests.
func (r *Resolver) SetGroupsHook(h GroupsHook) {
	r.groupsHook = h
}

// applyGroups runs the hook. A failure is logged, never fatal: it must not
// lock people out of the app.
func (r *Resolver) applyGroups(ctx context.Context, userID uuid.UUID, groups []string) {
	if r.groupsHook == nil {
		return
	}
	if err := r.groupsHook(ctx, userID, groups); err != nil {
		log.Ctx(ctx).Warn().Err(err).Msg("refresh user groups failed")
	}
}
