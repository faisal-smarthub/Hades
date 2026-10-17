package auth_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/alipourhabibi/Hades/api/gen/api/auth/v1"
	"github.com/alipourhabibi/Hades/config"
	authorizationengine "github.com/alipourhabibi/Hades/internal/hades/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/cache"
	"github.com/alipourhabibi/Hades/internal/hades/server"
	"github.com/alipourhabibi/Hades/internal/hades/server/auth"
	authorizationsvc "github.com/alipourhabibi/Hades/internal/hades/server/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/testsupport"
)

// With FirstUserOnlySignup set, the first Register call against an empty
// instance must succeed, and every one after it must be rejected, with no
// further configuration change needed in between.
func TestRegister_FirstUserOnlySignupClosesAfterTheFirstAccount(t *testing.T) {
	env := testsupport.NewEnv(t)
	engine, err := authorizationengine.New(context.Background(), env.DB.OPABinding(), cache.NewMemoryCache(), time.Minute)
	require.NoError(t, err)
	authz := authorizationsvc.NewServer(env.Logger, env.DB.User(), env.DB.Session(), engine)

	srv := auth.NewServer(&server.Dependencies{
		Logger:        env.Logger,
		UserDB:        env.DB.User(),
		SessionDB:     env.DB.Session(),
		APITokenDB:    env.DB.APIToken(),
		AuditLogDB:    env.DB.AuditLog(),
		Authorization: authz,
		UoW:           env.DB,
		Cache:         cache.NewMemoryCache(),
		AuthConfig:    config.AuthConfig{FirstUserOnlySignup: true},
	})

	_, err = srv.Register(context.Background(), connect.NewRequest(&authv1.RegisterRequest{
		Username: "firstadmin",
		Password: "Corr3ctHorseBattery!",
		Email:    "firstadmin@example.com",
	}))
	require.NoError(t, err, "the first account must still be registrable")

	_, err = srv.Register(context.Background(), connect.NewRequest(&authv1.RegisterRequest{
		Username: "someone-else",
		Password: "Corr3ctHorseBattery!",
		Email:    "someone-else@example.com",
	}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "signup must be closed once an account exists")
}

// GetSignupStatus must agree with what Register would actually do, so a
// client can rely on it to decide whether to show a signup entry point at
// all, rather than show one that always then fails.
func TestGetSignupStatus_AgreesWithRegister(t *testing.T) {
	env := testsupport.NewEnv(t)
	engine, err := authorizationengine.New(context.Background(), env.DB.OPABinding(), cache.NewMemoryCache(), time.Minute)
	require.NoError(t, err)
	authz := authorizationsvc.NewServer(env.Logger, env.DB.User(), env.DB.Session(), engine)

	srv := auth.NewServer(&server.Dependencies{
		Logger:        env.Logger,
		UserDB:        env.DB.User(),
		SessionDB:     env.DB.Session(),
		APITokenDB:    env.DB.APIToken(),
		AuditLogDB:    env.DB.AuditLog(),
		Authorization: authz,
		UoW:           env.DB,
		Cache:         cache.NewMemoryCache(),
		AuthConfig:    config.AuthConfig{FirstUserOnlySignup: true},
	})

	resp, err := srv.GetSignupStatus(context.Background(), connect.NewRequest(&authv1.GetSignupStatusRequest{}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Enabled, "no account exists yet, so signup must still read as open")

	_, err = srv.Register(context.Background(), connect.NewRequest(&authv1.RegisterRequest{
		Username: "firstadmin",
		Password: "Corr3ctHorseBattery!",
		Email:    "firstadmin@example.com",
	}))
	require.NoError(t, err)

	resp, err = srv.GetSignupStatus(context.Background(), connect.NewRequest(&authv1.GetSignupStatusRequest{}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Enabled, "an account now exists, so signup must read as closed")
}
