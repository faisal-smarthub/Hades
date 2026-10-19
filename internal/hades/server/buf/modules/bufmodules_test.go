package bufmodules

import (
	"context"
	"errors"
	"testing"

	modulev1 "buf.build/gen/go/bufbuild/registry/protocolbuffers/go/buf/registry/module/v1"
	ownerv1 "buf.build/gen/go/bufbuild/registry/protocolbuffers/go/buf/registry/owner/v1"
	"connectrpc.com/connect"
	registryv1 "github.com/alipourhabibi/Hades/api/gen/api/registry/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeModulesProvider implements modulesProvider for tests.
type fakeModulesProvider struct {
	modules []*registryv1.Module
	created *registryv1.Module
	err     error
}

func (f *fakeModulesProvider) GetModules(_ context.Context, _ []*registryv1.ModuleRef) ([]*registryv1.Module, error) {
	return f.modules, f.err
}

func (f *fakeModulesProvider) CreateModule(_ context.Context, _, _ string, _ registryv1.ModuleVisibility) (*registryv1.Module, error) {
	return f.created, f.err
}

func newServer(h modulesProvider) *Server {
	return &Server{handler: h}
}

func moduleRef(owner, module string) *modulev1.ModuleRef {
	return &modulev1.ModuleRef{
		Value: &modulev1.ModuleRef_Name_{
			Name: &modulev1.ModuleRef_Name{
				Owner:  owner,
				Module: module,
			},
		},
	}
}

func TestGetModules_HandlerError(t *testing.T) {
	handlerErr := errors.New("denied")
	s := newServer(&fakeModulesProvider{err: handlerErr})
	req := connect.NewRequest(&modulev1.GetModulesRequest{
		ModuleRefs: []*modulev1.ModuleRef{moduleRef("alice", "m")},
	})
	_, err := s.GetModules(context.Background(), req)
	assert.ErrorIs(t, err, handlerErr)
}

func TestGetModules_ConvertsResult(t *testing.T) {
	modules := []*registryv1.Module{{Id: "m1", Name: "alice/repo"}}
	s := newServer(&fakeModulesProvider{modules: modules})
	req := connect.NewRequest(&modulev1.GetModulesRequest{
		ModuleRefs: []*modulev1.ModuleRef{moduleRef("alice", "repo")},
	})
	resp, err := s.GetModules(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, resp.Msg.Modules, 1)
	// ToBufModulePB maps Id → Id in the buf proto
	assert.NotEmpty(t, resp.Msg.Modules[0])
}

func TestGetModules_EmptyRequest(t *testing.T) {
	s := newServer(&fakeModulesProvider{modules: nil})
	req := connect.NewRequest(&modulev1.GetModulesRequest{})
	resp, err := s.GetModules(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Modules)
}

func TestGetModules_PropagatesError(t *testing.T) {
	handlerErr := errors.New("storage failure")
	s := newServer(&fakeModulesProvider{err: handlerErr})
	req := connect.NewRequest(&modulev1.GetModulesRequest{
		ModuleRefs: []*modulev1.ModuleRef{moduleRef("alice", "m")},
	})
	_, err := s.GetModules(context.Background(), req)
	assert.ErrorIs(t, err, handlerErr)
}

func createValue(owner, module string) *modulev1.CreateModulesRequest_Value {
	return &modulev1.CreateModulesRequest_Value{
		OwnerRef: &ownerv1.OwnerRef{Value: &ownerv1.OwnerRef_Name{Name: owner}},
		Name:     module,
	}
}

func TestCreateModules_HandlerError(t *testing.T) {
	handlerErr := errors.New("permission denied")
	s := newServer(&fakeModulesProvider{err: handlerErr})
	req := connect.NewRequest(&modulev1.CreateModulesRequest{
		Values: []*modulev1.CreateModulesRequest_Value{createValue("smarthub", "infer-metricquery-svc")},
	})
	_, err := s.CreateModules(context.Background(), req)
	assert.ErrorIs(t, err, handlerErr)
}

func TestCreateModules_ConvertsResult(t *testing.T) {
	created := &registryv1.Module{Id: "m1", Name: "smarthub/infer-metricquery-svc"}
	s := newServer(&fakeModulesProvider{created: created})
	req := connect.NewRequest(&modulev1.CreateModulesRequest{
		Values: []*modulev1.CreateModulesRequest_Value{createValue("smarthub", "infer-metricquery-svc")},
	})
	resp, err := s.CreateModules(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, resp.Msg.Modules, 1)
	assert.Equal(t, "m1", resp.Msg.Modules[0].Id)
}

func TestCreateModules_EmptyRequest(t *testing.T) {
	s := newServer(&fakeModulesProvider{})
	req := connect.NewRequest(&modulev1.CreateModulesRequest{})
	resp, err := s.CreateModules(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Modules)
}

func TestCreateModules_RejectsOwnerRefByID(t *testing.T) {
	s := newServer(&fakeModulesProvider{})
	req := connect.NewRequest(&modulev1.CreateModulesRequest{
		Values: []*modulev1.CreateModulesRequest_Value{{
			OwnerRef: &ownerv1.OwnerRef{Value: &ownerv1.OwnerRef_Id{Id: "abc"}},
			Name:     "infer-metricquery-svc",
		}},
	})
	_, err := s.CreateModules(context.Background(), req)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
