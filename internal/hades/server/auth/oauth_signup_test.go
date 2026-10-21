package auth

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alipourhabibi/Hades/config"
	authorizationengine "github.com/alipourhabibi/Hades/internal/hades/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/cache"
	"github.com/alipourhabibi/Hades/internal/hades/server"
	authorizationsvc "github.com/alipourhabibi/Hades/internal/hades/server/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/testsupport"
)

// newOAuthSignupServer builds a Server with FirstUserOnlySignup set and the
// given OAuth config, backed by a fresh store.
func newOAuthSignupServer(t *testing.T, oauthCfg config.OAuthConfig) *Server {
	t.Helper()
	env := testsupport.NewEnv(t)
	engine, err := authorizationengine.New(context.Background(), env.DB.OPABinding(), cache.NewMemoryCache(), time.Minute)
	require.NoError(t, err)
	authz := authorizationsvc.NewServer(env.Logger, env.DB.User(), env.DB.Session(), engine)

	return NewServer(&server.Dependencies{
		Logger:          env.Logger,
		UserDB:          env.DB.User(),
		SessionDB:       env.DB.Session(),
		APITokenDB:      env.DB.APIToken(),
		AuditLogDB:      env.DB.AuditLog(),
		OAuthIdentityDB: env.DB.OAuthIdentity(),
		Authorization:   authz,
		UoW:             env.DB,
		Cache:           cache.NewMemoryCache(),
		AuthConfig:      config.AuthConfig{FirstUserOnlySignup: true},
		OAuthConfig:     oauthCfg,
	})
}

// A first OAuth login creates an account, so with FirstUserOnlySignup set it
// must be refused once an account exists, exactly as Register is. Before this
// check existed, any GitHub or Google account could create one on an instance
// whose signup was closed.
func TestResolveOAuthUser_FirstUserOnlySignupClosesOAuthSignup(t *testing.T) {
	for _, provider := range []string{"github", "google"} {
		t.Run(provider, func(t *testing.T) {
			srv := newOAuthSignupServer(t, config.OAuthConfig{})
			ctx := context.Background()

			_, err := srv.resolveOAuthUser(ctx, provider, "1001", "first@example.com")
			require.NoError(t, err, "the first account may still be created through OAuth")

			_, err = srv.resolveOAuthUser(ctx, provider, "1002", "second@example.com")
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "signup must be closed once an account exists")
		})
	}
}

// Closing signup must not lock out an account that is already linked: that
// is a login, not a signup.
func TestResolveOAuthUser_LinkedIdentityStillLogsInWhenSignupClosed(t *testing.T) {
	srv := newOAuthSignupServer(t, config.OAuthConfig{})
	ctx := context.Background()

	first, err := srv.resolveOAuthUser(ctx, "github", "1001", "first@example.com")
	require.NoError(t, err)

	again, err := srv.resolveOAuthUser(ctx, "github", "1001", "first@example.com")
	require.NoError(t, err)
	assert.Equal(t, first, again)
}

// With an org allow-list, a GitHub login has already been checked against
// the operator's own list of who may have an account, so it may create one
// even while FirstUserOnlySignup keeps password signup closed.
func TestResolveOAuthUser_GitHubOrgAllowListBypassesClosedSignup(t *testing.T) {
	srv := newOAuthSignupServer(t, config.OAuthConfig{
		GitHub: config.GitHubOAuthProvider{AllowedOrgs: []string{"smarthub"}},
	})
	ctx := context.Background()

	_, err := srv.resolveOAuthUser(ctx, "github", "1001", "first@example.com")
	require.NoError(t, err)

	_, err = srv.resolveOAuthUser(ctx, "github", "1002", "second@example.com")
	require.NoError(t, err, "an org-checked GitHub login must be able to create an account")

	// The exemption is GitHub's alone: Google has no allow-list.
	_, err = srv.resolveOAuthUser(ctx, "google", "2001", "third@example.com")
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
