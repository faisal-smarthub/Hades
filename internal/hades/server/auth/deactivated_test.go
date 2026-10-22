package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/alipourhabibi/Hades/api/gen/api/auth/v1"
	identityv1 "github.com/alipourhabibi/Hades/api/gen/api/identity/v1"
)

// A deactivated account used to be handed a session token by Login, which the
// interceptor then refused on every request. Sign-in now refuses it up front.
func TestLogin_DeactivatedAccountIsRefused(t *testing.T) {
	f := newAutoJoinFixture(t, nil)
	ctx := context.Background()
	_, err := f.srv.Register(ctx, connect.NewRequest(&authv1.RegisterRequest{
		Username: "bob", Password: "Corr3ctHorseBattery!", Email: "bob@example.com",
	}))
	require.NoError(t, err)
	bob, err := f.env.DB.User().GetByUsername(ctx, "bob")
	require.NoError(t, err)

	login := func() error {
		_, err := f.srv.Login(ctx, connect.NewRequest(&authv1.LoginRequest{Username: "bob", Password: "Corr3ctHorseBattery!"}))
		return err
	}
	require.NoError(t, login(), "an active account signs in")

	require.NoError(t, f.env.DB.User().SetState(ctx, bob.Id, identityv1.UserState_USER_STATE_DEACTIVATED))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(login()))

	_, err = f.srv.Login(ctx, connect.NewRequest(&authv1.LoginRequest{Username: "bob", Password: "wrong-password!!"}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err),
		"without the password the answer is the ordinary one, saying nothing about the account")

	require.NoError(t, f.env.DB.User().SetState(ctx, bob.Id, identityv1.UserState_USER_STATE_ACTIVE))
	assert.NoError(t, login(), "reactivation restores sign-in")
}
