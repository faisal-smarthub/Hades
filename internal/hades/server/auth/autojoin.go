package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/alipourhabibi/Hades/config"
	"github.com/alipourhabibi/Hades/internal/hades/constants"
	"github.com/alipourhabibi/Hades/utils/connerr"
)

// joinAutoJoinOrgs adds a just-created account to every organisation in
// auth.autoJoinOrgs. ctx must carry the account-creation transaction, so the
// account and its memberships are committed or rolled back together.
//
// The membership row and the OPA binding are written as a pair, exactly as
// OrgService.AddOrgMember writes them: the binding is what grants access, and
// the row is what the org's member list shows.
//
// An organisation that does not exist is logged and skipped; any other failure
// fails the signup, so a database error cannot quietly produce an account
// without the access the operator configured.
func (s *Server) joinAutoJoinOrgs(ctx context.Context, userID, username, procedure string) error {
	for _, o := range s.authCfg.AutoJoinOrgs {
		org, err := s.orgStorage.GetByName(ctx, o.Org)
		switch {
		case err == nil:
		case errors.Is(err, connerr.ErrNotFound), errors.Is(err, sql.ErrNoRows), errors.Is(err, pgx.ErrNoRows):
			s.logger.Error("auth.autoJoinOrgs names an organisation that does not exist; skipped",
				"procedure", procedure, "org", o.Org, "username", username)
			continue
		default:
			s.logger.Error("failed to look up an auto-join organisation", "error", err,
				"procedure", procedure, "org", o.Org)
			return connerr.FromDB(err)
		}

		role := o.EffectiveRole()
		opaRole := constants.RoleReader
		if role == config.AutoJoinRoleMember {
			opaRole = constants.RoleContributor
		}
		if err := s.orgStorage.AddMember(ctx, org.Id, userID, role); err != nil {
			s.logger.Error("failed to add auto-join org member", "error", err,
				"procedure", procedure, "org", o.Org, "username", username)
			return connerr.Internal("failed to set up organisation membership")
		}
		if err := s.authorizationService.AddOrgMemberBinding(ctx, username, opaRole, org.Username); err != nil {
			s.logger.Error("failed to add auto-join org binding", "error", err,
				"procedure", procedure, "org", o.Org, "username", username)
			return connerr.Internal("failed to set up organisation membership")
		}
	}
	return nil
}
