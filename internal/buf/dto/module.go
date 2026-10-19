// Package dto provides conversion functions between internal protobuf
// types and the buf.build registry wire types.
package dto

import (
	modulev1 "buf.build/gen/go/bufbuild/registry/protocolbuffers/go/buf/registry/module/v1"
	ownerv1 "buf.build/gen/go/bufbuild/registry/protocolbuffers/go/buf/registry/owner/v1"
	registryv1 "github.com/alipourhabibi/Hades/api/gen/api/registry/v1"
)

// ToBufModulePB converts an internal Module to the buf.build wire type.
func ToBufModulePB(in *registryv1.Module) *modulev1.Module {
	return &modulev1.Module{
		Id:               in.Id,
		CreateTime:       in.CreateTime,
		UpdateTime:       in.UpdateTime,
		Name:             in.Name,
		OwnerId:          in.OwnerId,
		Visibility:       modulev1.ModuleVisibility(in.Visibility),
		State:            modulev1.ModuleState(in.State),
		Description:      in.Description,
		Url:              in.Url,
		DefaultLabelName: in.DefaultLabelName,
	}
}

// FromModuleRefPB converts a buf.build ModuleRef to the internal type.
func FromModuleRefPB(in *modulev1.ModuleRef) *registryv1.ModuleRef {
	if in == nil {
		return &registryv1.ModuleRef{}
	}
	if in.GetId() != "" {
		return &registryv1.ModuleRef{Id: in.GetId()}
	}
	return &registryv1.ModuleRef{
		Owner:  in.GetName().GetOwner(),
		Module: in.GetName().GetModule(),
	}
}

// OwnerNameFromRef returns the owner name named by a CreateModules request's
// OwnerRef.
//
// Only the name form is supported: the id form would need a by-id owner
// lookup this server doesn't have a storage method for, and the buf CLI's
// own "--create" flow never sends one — it resolves the owner straight from
// the module's configured full name (e.g. "smarthub" in
// "buf.smarthubai.net/smarthub/infer-metricquery-svc"), so it is always the
// name form in practice.
func OwnerNameFromRef(in *ownerv1.OwnerRef) (name string, ok bool) {
	if in == nil || !in.HasName() {
		return "", false
	}
	return in.GetName(), true
}

// FromBufModuleVisibility converts a buf.build ModuleVisibility to the
// internal type. The two enums share the same underlying values.
func FromBufModuleVisibility(in modulev1.ModuleVisibility) registryv1.ModuleVisibility {
	return registryv1.ModuleVisibility(in)
}
