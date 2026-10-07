package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// This file holds what the kind components of Cilium's BGP control plane share
// (go-kure/launcher#790): cilium-bgpadvertisement, cilium-bgpclusterconfig,
// cilium-bgpnodeconfigoverride and cilium-bgppeerconfig, each a cilium.io/v2
// object. Each is a policyFreeKind: the object runs no pod, holds no image,
// requests no storage and has no replica count. All four are cluster-scoped.
//
// A Go type does not say which of its fields the API requires, defaults or
// holds to an expression rule. The module ships the CRDs, so that is read from
// those, in tests:
// TestCiliumBGPKinds_RequiredMatchCRD holds each kind's required list to the
// fields the CRD requires and the type writes unauthored,
// TestCiliumBGPKinds_DefaultsSitOnPointers the claim that no CRD default turns
// an authored 0 or false into another value, and TestCiliumBGPKinds_ExpressionRules the
// CRDs' expression rules to the ones a kind checks or leaves to the API
// server.
//
// An address these objects name is a BGP peer's or the node's own, not an
// artifact source, and none is held to the environment policy's allowed
// registries.

// ciliumKind is the group and kind of one object of Cilium's API.
func ciliumKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: ciliumv2.CustomResourceDefinitionGroup, Kind: kind}
}
