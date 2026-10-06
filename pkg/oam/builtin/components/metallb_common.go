package components

import (
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// This file holds what the kind components of MetalLB's API share
// (go-kure/launcher#790), each a metallb.io/v1beta1 object. Each is a
// policyFreeKind: the object runs no pod, holds no image, requests no storage
// and has no replica count. All are namespaced, and MetalLB reads them in one
// namespace, the one it is configured to watch (--namespace or
// METALLB_NAMESPACE), by default the one it runs in.
//
// A Go type does not say which of its fields the API requires, defaults or
// holds to an expression rule. The module ships the CRDs, so that is read from
// those, in tests: TestMetalLBKinds_RequiredMatchCRD holds each kind's
// required list to the fields the CRD requires and the type writes unauthored,
// TestMetalLBKinds_NoDefaultIsLost the claim that no CRD default turns an
// authored 0 or false into another value, and TestMetalLBKinds_ExpressionRules
// the claim that no CRD of them declares an expression rule.
//
// MetalLB also runs a validating webhook over these objects. It was not read,
// and nothing it refuses is repeated here.
//
// An address these objects name is one the cluster hands to a Service or
// announces, not an artifact source, and none is held to the environment
// policy's allowed registries.

// metallbKind is the group and kind of one object of MetalLB's API.
func metallbKind(kind string) schema.GroupKind {
	return schema.GroupKind{Group: metallbv1beta1.GroupVersion.Group, Kind: kind}
}

// metallbSelectorsRequired is the required list of the label selectors under
// each path of lists ("nodeSelectors", "serviceAllocation.serviceSelectors"),
// every one a list of Kubernetes label selectors: the two fields the API
// requires of a match expression, each of which the Go type would write empty
// (labelSelectorRequired).
func metallbSelectorsRequired(lists ...string) map[string]string {
	required := map[string]string{}
	for _, at := range lists {
		required = requiredFields(required, labelSelectorRequired(at+"[]"))
	}
	return required
}
