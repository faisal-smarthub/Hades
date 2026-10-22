// Package admin implements AdminService: instance-wide account management for
// superadmins.
package admin

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	identityv1 "github.com/alipourhabibi/Hades/api/gen/api/identity/v1"
	"github.com/alipourhabibi/Hades/api/gen/api/identity/v1/identityv1connect"
	"github.com/alipourhabibi/Hades/internal/hades/constants"
	"github.com/alipourhabibi/Hades/internal/hades/server"
	authorizationsvc "github.com/alipourhabibi/Hades/internal/hades/server/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/storage/db"
	moduledb "github.com/alipourhabibi/Hades/internal/hades/storage/db/module"
	userdb "github.com/alipourhabibi/Hades/internal/hades/storage/db/user"
	"github.com/alipourhabibi/Hades/utils/connerr"
	"github.com/alipourhabibi/Hades/utils/log"
)

// Handler implements identityv1connect.AdminServiceHandler.
type Handler struct {
	identityv1connect.UnimplementedAdminServiceHandler

	logger   *log.LoggerWrapper
	userDB   userdb.Storage
	moduleDB moduledb.Storage
	authz    *authorizationsvc.Server
	uow      db.UnitOfWork
}

func NewHandler(deps *server.Dependencies) *Handler {
	return &Handler{
		logger:   deps.Logger,
		userDB:   deps.UserDB,
		moduleDB: deps.ModuleDB,
		authz:    deps.Authorization,
		uow:      deps.UoW,
	}
}

// requireSuperAdmin returns the caller if they are a superadmin.
//
// The engine error is reported as Unavailable rather than folded into
// PermissionDenied, so an outage is not mistaken for a missing grant.
func (h *Handler) requireSuperAdmin(ctx context.Context, procedure string) (*identityv1.User, error) {
	caller, ok := ctx.Value(constants.ContextKeyUser).(*identityv1.User)
	if !ok || caller == nil {
		return nil, connerr.Unauthenticated("authentication required")
	}
	isSuperAdmin, err := h.authz.IsSuperAdmin(ctx, caller.Username)
	if err != nil {
		h.logger.Error("superadmin check failed", "error", err, "procedure", procedure, "subject", caller.Username)
		return nil, connerr.Unavailable("authorization service unavailable")
	}
	if !isSuperAdmin {
		return nil, connerr.PermissionDenied("this operation requires a superadmin")
	}
	return caller, nil
}

// target looks up a personal account by username. Organisations are not
// accounts anyone signs in to and are never returned.
func (h *Handler) target(ctx context.Context, username, procedure string) (*identityv1.User, error) {
	u, err := h.userDB.GetByUsername(ctx, username)
	switch {
	case err == nil:
	case errors.Is(err, connerr.ErrNotFound), errors.Is(err, sql.ErrNoRows), errors.Is(err, pgx.ErrNoRows):
		return nil, connerr.NotFound("user not found")
	default:
		h.logger.Error("failed to look up user", "error", err, "procedure", procedure, "username", username)
		return nil, connerr.FromDB(err)
	}
	if u.Type != identityv1.UserType_USER_TYPE_USER {
		return nil, connerr.NotFound("user not found")
	}
	return u, nil
}

// describe builds the administrator's view of an account.
func (h *Handler) describe(ctx context.Context, u *identityv1.User) (*identityv1.AdminUser, error) {
	granted, err := h.authz.IsSuperAdmin(ctx, u.Username)
	if err != nil {
		return nil, err
	}
	modules, err := h.moduleDB.CountByOwner(ctx, u.Id)
	if err != nil {
		return nil, err
	}
	return &identityv1.AdminUser{
		User:                 u,
		Superadmin:           granted,
		SuperadminFromConfig: h.authz.IsConfiguredSuperAdmin(u.Username),
		ModuleCount:          modules,
	}, nil
}

func (h *Handler) ListUsers(ctx context.Context, in *connect.Request[identityv1.AdminServiceListUsersRequest]) (*connect.Response[identityv1.AdminServiceListUsersResponse], error) {
	if _, err := h.requireSuperAdmin(ctx, "AdminService.ListUsers"); err != nil {
		return nil, err
	}

	limit, offset := server.Page(in.Msg.PageSize, in.Msg.PageToken)
	users, err := h.userDB.List(ctx, in.Msg.Query, limit, offset)
	if err != nil {
		h.logger.Error("failed to list users", "error", err, "procedure", "AdminService.ListUsers")
		return nil, connerr.InternalCause("failed to list users", err)
	}
	out := make([]*identityv1.AdminUser, 0, len(users))
	for _, u := range users {
		au, err := h.describe(ctx, u)
		if err != nil {
			h.logger.Error("failed to describe user", "error", err, "procedure", "AdminService.ListUsers", "username", u.Username)
			return nil, connerr.Internal("failed to list users")
		}
		out = append(out, au)
	}
	return connect.NewResponse(&identityv1.AdminServiceListUsersResponse{
		Users:         out,
		NextPageToken: server.NextPageToken(len(users), limit, offset),
	}), nil
}

func (h *Handler) SetUserState(ctx context.Context, in *connect.Request[identityv1.SetUserStateRequest]) (*connect.Response[identityv1.SetUserStateResponse], error) {
	const procedure = "AdminService.SetUserState"
	caller, err := h.requireSuperAdmin(ctx, procedure)
	if err != nil {
		return nil, err
	}
	u, err := h.target(ctx, in.Msg.Username, procedure)
	if err != nil {
		return nil, err
	}
	if u.Id == caller.Id {
		return nil, connerr.FailedPrecondition("you cannot change the state of your own account")
	}

	if err := h.userDB.SetState(ctx, u.Id, in.Msg.State); err != nil {
		h.logger.Error("failed to set user state", "error", err, "procedure", procedure, "username", u.Username)
		return nil, connerr.FromDB(err)
	}
	h.logger.Info("account state changed", "procedure", procedure,
		"actor", caller.Username, "username", u.Username, "state", in.Msg.State.String())

	return respondUser(ctx, h, u.Id, procedure, func(au *identityv1.AdminUser) *connect.Response[identityv1.SetUserStateResponse] {
		return connect.NewResponse(&identityv1.SetUserStateResponse{User: au})
	})
}

func (h *Handler) DeleteUser(ctx context.Context, in *connect.Request[identityv1.DeleteUserRequest]) (*connect.Response[identityv1.DeleteUserResponse], error) {
	const procedure = "AdminService.DeleteUser"
	caller, err := h.requireSuperAdmin(ctx, procedure)
	if err != nil {
		return nil, err
	}
	u, err := h.target(ctx, in.Msg.Username, procedure)
	if err != nil {
		return nil, err
	}
	if u.Id == caller.Id {
		return nil, connerr.FailedPrecondition("you cannot delete your own account")
	}
	// The name stays in opa.superAdmins after the account is gone, so whoever
	// registered it next would be a superadmin.
	if h.authz.IsConfiguredSuperAdmin(u.Username) {
		return nil, connerr.FailedPrecondition("this account is listed in opa.superAdmins; remove it from the server configuration first")
	}

	if _, err := h.uow.Do(ctx, func(txCtx context.Context) (interface{}, error) {
		// Checked inside the transaction so a module created in between is not
		// deleted along with the account.
		modules, err := h.moduleDB.CountByOwner(txCtx, u.Id)
		if err != nil {
			return nil, connerr.FromDB(err)
		}
		if modules > 0 {
			return nil, connerr.FailedPrecondition("the account still owns modules; they would be deleted with it")
		}
		if err := h.authz.DeleteAllBindings(txCtx, u.Username); err != nil {
			return nil, connerr.FromDB(err)
		}
		if err := h.userDB.Delete(txCtx, u.Id); err != nil {
			return nil, connerr.FromDB(err)
		}
		return nil, nil
	}, 15*time.Second); err != nil {
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			h.logger.Error("failed to delete user", "error", err, "procedure", procedure, "username", u.Username)
		}
		return nil, err
	}
	h.logger.Info("account deleted", "procedure", procedure, "actor", caller.Username, "username", u.Username)
	return connect.NewResponse(&identityv1.DeleteUserResponse{}), nil
}

func (h *Handler) SetSuperadmin(ctx context.Context, in *connect.Request[identityv1.SetSuperadminRequest]) (*connect.Response[identityv1.SetSuperadminResponse], error) {
	const procedure = "AdminService.SetSuperadmin"
	caller, err := h.requireSuperAdmin(ctx, procedure)
	if err != nil {
		return nil, err
	}
	u, err := h.target(ctx, in.Msg.Username, procedure)
	if err != nil {
		return nil, err
	}

	if in.Msg.Superadmin {
		if u.State == identityv1.UserState_USER_STATE_DEACTIVATED {
			return nil, connerr.FailedPrecondition("reactivate the account before promoting it")
		}
		err = h.authz.GrantSuperAdmin(ctx, u.Username)
	} else {
		if u.Id == caller.Id {
			return nil, connerr.FailedPrecondition("you cannot demote your own account; ask another superadmin")
		}
		if h.authz.IsConfiguredSuperAdmin(u.Username) {
			return nil, connerr.FailedPrecondition("this account is listed in opa.superAdmins; remove it from the server configuration instead")
		}
		err = h.authz.RevokeSuperAdmin(ctx, u.Username)
	}
	if err != nil {
		h.logger.Error("failed to change superadmin status", "error", err, "procedure", procedure, "username", u.Username)
		return nil, connerr.FromDB(err)
	}
	h.logger.Info("superadmin status changed", "procedure", procedure,
		"actor", caller.Username, "username", u.Username, "superadmin", in.Msg.Superadmin)

	return respondUser(ctx, h, u.Id, procedure, func(au *identityv1.AdminUser) *connect.Response[identityv1.SetSuperadminResponse] {
		return connect.NewResponse(&identityv1.SetSuperadminResponse{User: au})
	})
}

// respondUser re-reads the account after a change and wraps its admin view.
func respondUser[T any](ctx context.Context, h *Handler, userID, procedure string, wrap func(*identityv1.AdminUser) *connect.Response[T]) (*connect.Response[T], error) {
	u, err := h.userDB.GetByID(ctx, userID)
	if err != nil {
		h.logger.Error("failed to re-read user", "error", err, "procedure", procedure, "user_id", userID)
		return nil, connerr.FromDB(err)
	}
	au, err := h.describe(ctx, u)
	if err != nil {
		h.logger.Error("failed to describe user", "error", err, "procedure", procedure, "user_id", userID)
		return nil, connerr.Internal("failed to read the updated account")
	}
	return wrap(au), nil
}
