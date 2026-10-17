package dto

import (
	"encoding/hex"

	modulev1beta1 "buf.build/gen/go/bufbuild/registry/protocolbuffers/go/buf/registry/module/v1beta1"
	registryv1 "github.com/alipourhabibi/Hades/api/gen/api/registry/v1"
	"github.com/alipourhabibi/Hades/internal/hades/storage/db/sqlutil"
	"github.com/google/uuid"
)

// FromUploadContentPBV1Beta1 converts a buf.registry.module.v1beta1 upload
// content item to the internal type. v1beta1's UploadRequest_Content is
// field-for-field identical to v1's; this mirrors FromUploadContentPB rather
// than sharing code with it so each wire version stays free to diverge later
// without one adapter's change silently affecting the other.
func FromUploadContentPBV1Beta1(in *modulev1beta1.UploadRequest_Content) *registryv1.UploadRequestContent {
	if in == nil {
		return nil
	}
	content := &registryv1.UploadRequestContent{
		ModuleRef: &registryv1.ModuleRef{
			Id:     in.ModuleRef.GetId(),
			Owner:  in.ModuleRef.GetName().GetOwner(),
			Module: in.ModuleRef.GetName().GetModule(),
		},
		Files: make([]*registryv1.File, 0, len(in.Files)),
	}
	for _, f := range in.Files {
		content.Files = append(content.Files, &registryv1.File{
			Path:    f.Path,
			Content: f.Content,
		})
	}
	return content
}

// ToCommitPBV1Beta1 converts an internal Commit to the buf.registry.module.v1beta1
// wire type. See ToCommitPB: same digest-decoding behavior, mirrored rather
// than shared for the same reason as FromUploadContentPBV1Beta1.
func ToCommitPBV1Beta1(in *registryv1.Commit) *modulev1beta1.Commit {
	tId, _ := uuid.Parse(in.Id)

	var digestValue []byte
	if len(in.Digest.GetValue()) > 0 {
		decoded, err := hex.DecodeString(string(in.Digest.Value))
		if err == nil {
			digestValue = decoded
		} else {
			digestValue = in.Digest.Value
		}
	}

	return &modulev1beta1.Commit{
		Id:         sqlutil.UUID(tId),
		CreateTime: in.CreateTime,
		OwnerId:    in.OwnerId,
		ModuleId:   in.ModuleId,
		Digest: &modulev1beta1.Digest{
			Type:  modulev1beta1.DigestType(in.Digest.Type),
			Value: digestValue,
		},
		CreatedByUserId:  in.CreatedByUserId,
		SourceControlUrl: in.SourceControlUrl,
	}
}
