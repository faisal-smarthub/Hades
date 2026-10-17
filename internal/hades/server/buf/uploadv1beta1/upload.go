// Package bufuploadv1beta1 implements the buf.registry.module.v1beta1
// UploadService protocol adapter. v1beta1's Upload RPC is the one current
// buf CLI versions actually call for `buf push` (confirmed against the real
// buf.build registry); only the v1 adapter existed here, which left `buf
// push` hitting Unimplemented against this server regardless of module
// state. This mirrors bufupload (the v1 adapter) field-for-field, since
// v1beta1's UploadRequest/UploadResponse/Commit/Digest messages are
// otherwise identical to v1's.
package bufuploadv1beta1

import (
	"context"

	modulev1beta1connect "buf.build/gen/go/bufbuild/registry/connectrpc/go/buf/registry/module/v1beta1/modulev1beta1connect"
	modulev1beta1 "buf.build/gen/go/bufbuild/registry/protocolbuffers/go/buf/registry/module/v1beta1"
	"connectrpc.com/connect"
	registryv1 "github.com/alipourhabibi/Hades/api/gen/api/registry/v1"
	"github.com/alipourhabibi/Hades/internal/buf/dto"
	"github.com/alipourhabibi/Hades/internal/hades/server"
	"github.com/alipourhabibi/Hades/internal/hades/server/content"
	"github.com/alipourhabibi/Hades/utils/log"
)

// uploadProvider is the interface the Server delegates to.
// content.Handler satisfies it; tests can provide a fake.
type uploadProvider interface {
	Upload(ctx context.Context, contents []*registryv1.UploadRequestContent) ([]*registryv1.Commit, error)
}

// Server is the buf.registry.module.v1beta1 protocol adapter for upload. It
// converts wire types to Hades's own proto types, delegates all business
// logic to the same content.Handler the v1 adapter uses, then converts the
// result back. There is deliberately no separate authorization or push logic
// here: both wire versions must behave identically, and duplicating that
// logic per version is exactly how they would drift.
type Server struct {
	modulev1beta1connect.UnimplementedUploadServiceHandler

	handler uploadProvider
	logger  *log.LoggerWrapper
}

func NewServer(deps *server.Dependencies) *Server {
	return &Server{
		logger:  deps.Logger,
		handler: content.NewHandler(deps),
	}
}

func (s *Server) Upload(ctx context.Context, req *connect.Request[modulev1beta1.UploadRequest]) (*connect.Response[modulev1beta1.UploadResponse], error) {
	contents := make([]*registryv1.UploadRequestContent, 0, len(req.Msg.Contents))
	for _, r := range req.Msg.Contents {
		contents = append(contents, dto.FromUploadContentPBV1Beta1(r))
	}

	commits, err := s.handler.Upload(ctx, contents)
	if err != nil {
		return nil, err
	}

	responseCommits := make([]*modulev1beta1.Commit, 0, len(commits))
	for _, v := range commits {
		responseCommits = append(responseCommits, dto.ToCommitPBV1Beta1(v))
	}

	return &connect.Response[modulev1beta1.UploadResponse]{
		Msg: &modulev1beta1.UploadResponse{Commits: responseCommits},
	}, nil
}
