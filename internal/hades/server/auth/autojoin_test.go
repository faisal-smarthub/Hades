package auth

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/alipourhabibi/Hades/api/gen/api/auth/v1"
	identityv1 "github.com/alipourhabibi/Hades/api/gen/api/identity/v1"
	"github.com/alipourhabibi/Hades/config"
	authorizationengine "github.com/alipourhabibi/Hades/internal/hades/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/cache"
	"github.com/alipourhabibi/Hades/internal/hades/constants"
	"github.com/alipourhabibi/Hades/internal/hades/server"
	authorizationsvc "github.com/alipourhabibi/Hades/internal/hades/server/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/testsupport"
)

type autoJoinFixture struct {
	srv   *Server
	env   *testsupport.Env
	authz *authorizationsvc.Server
	org   *identityv1.User
}

// newAutoJoinFixture builds a Server with the given auto-join list and an
// existing org "acme" owned by a seed account.
func newAutoJoinFixture(t *testing.T, autoJoin []config.AutoJoinOrg) *autoJoinFixture {
	t.Helper()
	env := testsupport.NewEnv(t)
	ctx := context.Background()
	engine, err := authorizationengine.New(ctx, env.DB.OPABinding(), cache.NewMemoryCache(), time.Minute)
	require.NoError(t, err)
	authz := authorizationsvc.NewServer(env.Logger, env.DB.User(), env.DB.Session(), engine)

	require.NoError(t, env.DB.User().Create(ctx, "founder", "founder@example.com", "", identityv1.UserType_USER_TYPE_USER, identityv1.UserState_USER_STATE_ACTIVE, "", ""))
	founder, err := env.DB.User().GetByUsername(ctx, "founder")
	require.NoError(t, err)
	org, err := env.DB.Org().Create(ctx, "acme", "", "", founder.Id)
	require.NoError(t, err)

	srv := NewServer(&server.Dependencies{
		Logger:          env.Logger,
		UserDB:          env.DB.User(),
		SessionDB:       env.DB.Session(),
		APITokenDB:      env.DB.APIToken(),
		AuditLogDB:      env.DB.AuditLog(),
		OAuthIdentityDB: env.DB.OAuthIdentity(),
		OrgDB:           env.DB.Org(),
		Authorization:   authz,
		UoW:             env.DB,
		Cache:           cache.NewMemoryCache(),
		AuthConfig: config.AuthConfig{
			AutoJoinOrgs:      autoJoin,
			EmailVerification: config.EmailVerifConfig{AutoVerify: true},
		},
	})
	return &autoJoinFixture{srv: srv, env: env, authz: authz, org: org}
}

func (f *autoJoinFixture) can(t *testing.T, username string, action constants.Action) bool {
	t.Helper()
	resp, err := f.authz.Can(context.Background(), &constants.Policy{
		Subject: username, ResourceType: string(constants.ResourceModule), Action: string(action),
		Domain: "acme/thing", Visibility: constants.VisibilityPrivate,
	})
	require.NoError(t, err)
	return resp.Allowed
}

func (f *autoJoinFixture) role(t *testing.T, username string) string {
	t.Helper()
	u, err := f.env.DB.User().GetByUsername(context.Background(), username)
	require.NoError(t, err)
	role, err := f.env.DB.Org().GetMemberRole(context.Background(), f.org.Id, u.Id)
	require.NoError(t, err, "%s must be a member of acme", username)
	return role
}

func TestAutoJoinOrgs_OAuthSignupJoinsAsReader(t *testing.T) {
	f := newAutoJoinFixture(t, []config.AutoJoinOrg{{Org: "acme"}})

	_, err := f.srv.resolveOAuthUser(context.Background(), "github", "1001", "alice@example.com", "alice")
	require.NoError(t, err)

	assert.Equal(t, "reader", f.role(t, "alice"), "the role defaults to reader")
	assert.True(t, f.can(t, "alice", constants.ActionRead), "a reader can read the org's private modules")
	assert.False(t, f.can(t, "alice", constants.ActionPush), "a reader cannot push")
}

func TestAutoJoinOrgs_RegisterJoinsWithTheConfiguredRole(t *testing.T) {
	f := newAutoJoinFixture(t, []config.AutoJoinOrg{{Org: "acme", Role: "member"}})

	_, err := f.srv.Register(context.Background(), connect.NewRequest(&authv1.RegisterRequest{
		Username: "bob",
		Password: "Corr3ctHorseBattery!",
		Email:    "bob@example.com",
	}))
	require.NoError(t, err)

	assert.Equal(t, "member", f.role(t, "bob"))
	assert.True(t, f.can(t, "bob", constants.ActionPush), "member maps to contributor, which can push")
}

func TestAutoJoinOrgs_MissingOrgIsSkippedNotFatal(t *testing.T) {
	// A typo in the config must not lock every new user out of signing up.
	f := newAutoJoinFixture(t, []config.AutoJoinOrg{{Org: "nosuch"}, {Org: "acme"}})

	_, err := f.srv.resolveOAuthUser(context.Background(), "github", "1001", "alice@example.com", "alice")
	require.NoError(t, err)

	assert.Equal(t, "reader", f.role(t, "alice"), "the orgs that do exist are still joined")
}

func TestAutoJoinOrgs_ExistingLoginDoesNotRejoin(t *testing.T) {
	// Only account creation joins: an admin who removes someone from the org
	// must not see them reappear on their next login.
	f := newAutoJoinFixture(t, []config.AutoJoinOrg{{Org: "acme"}})
	ctx := context.Background()

	_, err := f.srv.resolveOAuthUser(ctx, "github", "1001", "alice@example.com", "alice")
	require.NoError(t, err)
	alice, err := f.env.DB.User().GetByUsername(ctx, "alice")
	require.NoError(t, err)
	require.NoError(t, f.env.DB.Org().RemoveMember(ctx, f.org.Id, alice.Id))

	_, err = f.srv.resolveOAuthUser(ctx, "github", "1001", "alice@example.com", "alice")
	require.NoError(t, err)

	_, err = f.env.DB.Org().GetMemberRole(ctx, f.org.Id, alice.Id)
	assert.Error(t, err, "a returning user is not re-added")
}
