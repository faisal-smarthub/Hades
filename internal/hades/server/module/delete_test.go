package module_test

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
	authorizationsvc "github.com/alipourhabibi/Hades/internal/hades/server/authorization"
	"github.com/alipourhabibi/Hades/internal/hades/server/module"
	"github.com/alipourhabibi/Hades/internal/hades/storage/db/resource"
	"github.com/alipourhabibi/Hades/internal/hades/testsupport"
)

// DeleteModule over a real database, a real git repository and the policy the
// server ships. root is a superadmin from configuration.
type deleteStack struct {
	env    *testsupport.Env
	module *module.Server
	authz  *authorizationsvc.Server
}

func newDeleteStack(t *testing.T) *deleteStack {
	t.Helper()
	env := testsupport.NewEnv(t)
	engine, err := authorizationengine.New(context.Background(), env.DB.OPABinding(), cache.NewMemoryCache(), time.Minute,
		authorizationengine.WithSuperAdmins([]string{"root"}))
	require.NoError(t, err)
	authz := authorizationsvc.NewServer(env.Logger, env.DB.User(), env.DB.Session(), engine)
	return &deleteStack{
		env: env,
		module: module.NewServer(&server.Dependencies{
			Logger:        env.Logger,
			ModuleDB:      env.DB.Module(),
			CommitDB:      env.DB.Commit(),
			OrgDB:         env.DB.Org(),
			UserDB:        env.DB.User(),
			GitStorage:    env.Git,
			Authorization: authz,
			UoW:           env.DB,
			GitalyOpLog:   env.DB.GitalyOpLog(),
		}),
		authz: authz,
	}
}

// user creates an account with the namespace-wide owner binding registration
// grants.
func (s *deleteStack) user(t *testing.T, username string) (*identityv1.User, context.Context) {
	t.Helper()
	u := s.env.CreateUser(t, username, username+"@example.com")
	require.NoError(t, s.authz.AddBasicRoles(context.Background(), username))
	return u, context.WithValue(context.Background(), constants.ContextKeyUser, u)
}

// create makes a module through the real handler, which also makes its
// repository and first commit.
func (s *deleteStack) create(t *testing.T, ctx context.Context, owner, name string) *registryv1.Module {
	t.Helper()
	resp, err := s.module.CreateModuleByName(ctx, connect.NewRequest(&registryv1.CreateModuleByNameRequest{
		Owner: owner, Name: name, Visibility: registryv1.ModuleVisibility_MODULE_VISIBILITY_PRIVATE,
	}))
	require.NoError(t, err)
	return resp.Msg.Module
}

func (s *deleteStack) delete(ctx context.Context, owner, name string) error {
	_, err := s.module.DeleteModule(ctx, connect.NewRequest(&registryv1.DeleteModuleRequest{Owner: owner, Name: name}))
	return err
}

func TestDeleteModule_RemovesRowsRepositoryAndResources(t *testing.T) {
	s := newDeleteStack(t)
	ctx := context.Background()
	alice, aliceCtx := s.user(t, "alice")
	mod := s.create(t, aliceCtx, "alice", "doomed")
	s.env.Commit(t, mod, alice, "second", map[string]string{"a.proto": "syntax = \"proto3\";\n"})
	commits, err := s.env.DB.Commit().ListByModule(ctx, mod.Id, 10, 0)
	require.NoError(t, err)
	require.Len(t, commits, 2)

	require.NoError(t, s.delete(aliceCtx, "alice", "doomed"))

	_, err = s.env.DB.Module().GetModuleByOwnerAndName(ctx, "alice", "doomed")
	assert.Error(t, err, "the module row is gone")
	_, err = s.env.Git.ListFiles(ctx, "alice/doomed", "main")
	assert.Error(t, err, "the repository is gone")
	_, err = s.env.DB.Resource().ResolveType(ctx, mod.Id)
	assert.ErrorIs(t, err, resource.ErrNotFound)
	for _, c := range commits {
		_, err = s.env.DB.Resource().ResolveType(ctx, c.Id)
		assert.ErrorIs(t, err, resource.ErrNotFound, "commit %s", c.Id)
	}

	// The name is free again: neither a row nor an orphan repository is left
	// to make the next create collide.
	s.create(t, aliceCtx, "alice", "doomed")
}

func TestDeleteModule_Permissions(t *testing.T) {
	s := newDeleteStack(t)
	_, aliceCtx := s.user(t, "alice")
	_, bobCtx := s.user(t, "bob")
	_, rootCtx := s.user(t, "root")
	s.create(t, aliceCtx, "alice", "one")
	s.create(t, aliceCtx, "alice", "two")
	s.create(t, aliceCtx, "alice", "three")

	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(s.delete(bobCtx, "alice", "one")),
		"no binding over the namespace")

	for _, role := range []string{constants.RoleReader, constants.RoleContributor} {
		require.NoError(t, s.authz.AddOrgMemberBinding(context.Background(), "bob", role, "alice"))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(s.delete(bobCtx, "alice", "one")), role)
		require.NoError(t, s.authz.DeleteOrgBinding(context.Background(), "bob", "alice"))
	}

	require.NoError(t, s.authz.AddOrgMemberBinding(context.Background(), "bob", constants.RoleAdmin, "alice"))
	require.NoError(t, s.delete(bobCtx, "alice", "one"), "an organization admin may delete")

	require.NoError(t, s.delete(rootCtx, "alice", "two"), "a superadmin may delete anywhere")
	require.NoError(t, s.delete(aliceCtx, "alice", "three"), "the namespace owner may delete")

	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(s.delete(aliceCtx, "alice", "nosuch")),
		"a caller with rights over the namespace learns the module does not exist")
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(s.delete(bobCtx, "carol", "nosuch")),
		"a caller without them learns nothing")
}
