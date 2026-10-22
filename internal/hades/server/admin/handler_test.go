package admin_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identityv1 "github.com/alipourhabibi/Hades/api/gen/api/identity/v1"
	registryv1 "github.com/alipourhabibi/Hades/api/gen/api/registry/v1"
	authorizationengine "github.com/alipourhabibi/Hades/internal/hades/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/cache"
	"github.com/alipourhabibi/Hades/internal/hades/constants"
	"github.com/alipourhabibi/Hades/internal/hades/server"
	"github.com/alipourhabibi/Hades/internal/hades/server/admin"
	authorizationsvc "github.com/alipourhabibi/Hades/internal/hades/server/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/testsupport"
)

// fixture has root, a superadmin from configuration, and two ordinary
// accounts, alice and bob.
type fixture struct {
	env   *testsupport.Env
	h     *admin.Handler
	authz *authorizationsvc.Server
	root  *identityv1.User
	alice *identityv1.User
	bob   *identityv1.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := testsupport.NewEnv(t)
	engine, err := authorizationengine.New(context.Background(), env.DB.OPABinding(), cache.NewMemoryCache(), time.Minute,
		authorizationengine.WithSuperAdmins([]string{"root"}))
	require.NoError(t, err)
	authz := authorizationsvc.NewServer(env.Logger, env.DB.User(), env.DB.Session(), engine)

	h := admin.NewHandler(&server.Dependencies{
		Logger:        env.Logger,
		UserDB:        env.DB.User(),
		ModuleDB:      env.DB.Module(),
		Authorization: authz,
		UoW:           env.DB,
	})
	return &fixture{
		env:   env,
		h:     h,
		authz: authz,
		root:  env.CreateUser(t, "root", "root@example.com"),
		alice: env.CreateUser(t, "alice", "alice@example.com"),
		bob:   env.CreateUser(t, "bob", "bob@example.com"),
	}
}

func ctxFor(user *identityv1.User) context.Context {
	return context.WithValue(context.Background(), constants.ContextKeyUser, user)
}

func (f *fixture) setSuperadmin(t *testing.T, caller *identityv1.User, username string, on bool) error {
	t.Helper()
	_, err := f.h.SetSuperadmin(ctxFor(caller), connect.NewRequest(&identityv1.SetSuperadminRequest{Username: username, Superadmin: on}))
	return err
}

func (f *fixture) setState(t *testing.T, caller *identityv1.User, username string, state identityv1.UserState) error {
	t.Helper()
	_, err := f.h.SetUserState(ctxFor(caller), connect.NewRequest(&identityv1.SetUserStateRequest{Username: username, State: state}))
	return err
}

func (f *fixture) deleteUser(t *testing.T, caller *identityv1.User, username string) error {
	t.Helper()
	_, err := f.h.DeleteUser(ctxFor(caller), connect.NewRequest(&identityv1.DeleteUserRequest{Username: username}))
	return err
}

func (f *fixture) isSuperAdmin(t *testing.T, username string) bool {
	t.Helper()
	ok, err := f.authz.IsSuperAdmin(context.Background(), username)
	require.NoError(t, err)
	return ok
}

func TestAdmin_OrdinaryAccountsAreRefusedEverything(t *testing.T) {
	f := newFixture(t)
	ctx := ctxFor(f.alice)

	_, err := f.h.ListUsers(ctx, connect.NewRequest(&identityv1.AdminServiceListUsersRequest{}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "ListUsers")
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(f.setSuperadmin(t, f.alice, "alice", true)), "self-promotion")
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(f.setState(t, f.alice, "bob", identityv1.UserState_USER_STATE_DEACTIVATED)), "SetUserState")
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(f.deleteUser(t, f.alice, "bob")), "DeleteUser")
	assert.False(t, f.isSuperAdmin(t, "alice"))
}

func TestAdmin_ListUsersShowsStatusEmailAndModules(t *testing.T) {
	f := newFixture(t)
	f.env.CreateModule(t, "alice/one", f.alice, registryv1.ModuleVisibility_MODULE_VISIBILITY_PRIVATE)
	require.NoError(t, f.setSuperadmin(t, f.root, "bob", true))

	resp, err := f.h.ListUsers(ctxFor(f.root), connect.NewRequest(&identityv1.AdminServiceListUsersRequest{}))
	require.NoError(t, err)

	byName := map[string]*identityv1.AdminUser{}
	for _, u := range resp.Msg.Users {
		byName[u.User.Username] = u
	}
	require.Contains(t, byName, "root")
	require.Contains(t, byName, "alice")
	require.Contains(t, byName, "bob")

	assert.True(t, byName["root"].Superadmin)
	assert.True(t, byName["root"].SuperadminFromConfig)
	assert.True(t, byName["bob"].Superadmin)
	assert.False(t, byName["bob"].SuperadminFromConfig)
	assert.False(t, byName["alice"].Superadmin)
	assert.Equal(t, int32(1), byName["alice"].ModuleCount)
	assert.Equal(t, "alice@example.com", byName["alice"].User.Email, "administrators see addresses")
}

func TestAdmin_PromoteAndDemote(t *testing.T) {
	f := newFixture(t)

	require.NoError(t, f.setSuperadmin(t, f.root, "alice", true))
	assert.True(t, f.isSuperAdmin(t, "alice"))

	// A promoted superadmin has every permission, not just this service.
	can, err := f.authz.Can(context.Background(), &constants.Policy{
		Subject: "alice", ResourceType: string(constants.ResourceModule), Action: string(constants.ActionDelete),
		Domain: "bob/private", Visibility: constants.VisibilityPrivate,
	})
	require.NoError(t, err)
	assert.True(t, can.Allowed)

	// And may administer in turn.
	require.NoError(t, f.setSuperadmin(t, f.alice, "bob", true), "a promoted superadmin can promote others")
	require.NoError(t, f.setSuperadmin(t, f.alice, "bob", false))
	assert.False(t, f.isSuperAdmin(t, "bob"))

	require.NoError(t, f.setSuperadmin(t, f.root, "alice", true), "promoting twice is a no-op")
	require.NoError(t, f.setSuperadmin(t, f.root, "alice", false))
	assert.False(t, f.isSuperAdmin(t, "alice"))
}

func TestAdmin_SuperadminGuards(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.setSuperadmin(t, f.root, "alice", true))

	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(f.setSuperadmin(t, f.alice, "alice", false)),
		"self-demotion is refused")
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(f.setSuperadmin(t, f.alice, "root", false)),
		"a configured superadmin cannot be demoted through the API")
	assert.True(t, f.isSuperAdmin(t, "root"))

	require.NoError(t, f.setState(t, f.root, "bob", identityv1.UserState_USER_STATE_DEACTIVATED))
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(f.setSuperadmin(t, f.root, "bob", true)),
		"a deactivated account cannot be promoted")

	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(f.setSuperadmin(t, f.root, "nosuch", true)))
}

func TestAdmin_DeactivateAndReactivate(t *testing.T) {
	f := newFixture(t)

	require.NoError(t, f.setState(t, f.root, "bob", identityv1.UserState_USER_STATE_DEACTIVATED))
	got, err := f.env.DB.User().GetByUsername(context.Background(), "bob")
	require.NoError(t, err)
	assert.Equal(t, identityv1.UserState_USER_STATE_DEACTIVATED, got.State)

	require.NoError(t, f.setState(t, f.root, "bob", identityv1.UserState_USER_STATE_ACTIVE))
	got, err = f.env.DB.User().GetByUsername(context.Background(), "bob")
	require.NoError(t, err)
	assert.Equal(t, identityv1.UserState_USER_STATE_ACTIVE, got.State)

	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(f.setState(t, f.root, "root", identityv1.UserState_USER_STATE_DEACTIVATED)),
		"an administrator cannot deactivate themselves")
}

func TestAdmin_DeleteRemovesAccountAndBindings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.setSuperadmin(t, f.root, "bob", true))

	require.NoError(t, f.deleteUser(t, f.root, "bob"))

	_, err := f.env.DB.User().GetByUsername(ctx, "bob")
	assert.Error(t, err, "the account is gone")
	bindings, err := f.env.DB.OPABinding().ListBySubject(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, bindings, "no binding is left for the next person to register the name")

	// Someone registering the freed name does not inherit anything.
	f.env.CreateUser(t, "bob", "new-bob@example.com")
	assert.False(t, f.isSuperAdmin(t, "bob"))
}

func TestAdmin_DeleteGuards(t *testing.T) {
	f := newFixture(t)
	f.env.CreateModule(t, "alice/one", f.alice, registryv1.ModuleVisibility_MODULE_VISIBILITY_PRIVATE)
	require.NoError(t, f.setSuperadmin(t, f.root, "bob", true))

	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(f.deleteUser(t, f.root, "alice")),
		"an account that owns modules is not deleted, or its modules would go with it")
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(f.deleteUser(t, f.root, "root")),
		"an administrator cannot delete themselves")
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(f.deleteUser(t, f.bob, "root")),
		"a configured superadmin is not deleted while its name is still in the config")
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(f.deleteUser(t, f.root, "nosuch")))

	_, err := f.env.DB.User().GetByUsername(context.Background(), "alice")
	assert.NoError(t, err, "alice is still there")
}
