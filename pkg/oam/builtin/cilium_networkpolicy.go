package builtin

import (
	"reflect"

	slimmetav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	ciliumlabels "github.com/cilium/cilium/pkg/labels"
	ciliumapi "github.com/cilium/cilium/pkg/policy/api"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// CiliumNetworkPolicyRendering holds no rendering keys; ValidateAndApplyDefaults rejects
// any key the operator accidentally provides (design-capability-schema.md §2.4).
type CiliumNetworkPolicyRendering struct{}

// ciliumICMPFieldShape is what ciliumapi.ICMPField's UnmarshalJSON reads a
// value into: the type's own two fields.
type ciliumICMPFieldShape struct {
	Family string              `json:"family,omitempty"`
	Type   *intstr.IntOrString `json:"type"`
}

// ciliumLabelShape is what ciliumlabels.Label's UnmarshalJSON reads the object
// form of a label into. The short form is a string and holds no key.
type ciliumLabelShape struct {
	Source string `json:"source"`
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
}

// ciliumSelfDecodedShapes holds the types under a Cilium policy rule that
// decode themselves, each with its shape. The endpoint selector is the one that
// matters: an unknown key inside one is dropped, and a selector that lost its
// only key is the empty one, which matches every endpoint.
//
// Cilium v1.20 sources: (*EndpointSelector).UnmarshalJSON in
// pkg/policy/api/selector.go, (*ICMPField).UnmarshalJSON in
// pkg/policy/api/icmp.go, (*Label).UnmarshalJSON in pkg/labels/labels.go.
//
// The positions of these types in a rule are not listed anywhere:
// UnknownCiliumKeyPath finds them by type. On a Cilium bump,
// TestCiliumSelfDecodedShapes_CoverRule fails for a new type that decodes
// itself, and TestCiliumSelfDecodedShapes_MatchTheTypes for a shape that no
// longer matches its type.
var ciliumSelfDecodedShapes = SelfDecodedShapes{
	reflect.TypeFor[ciliumapi.EndpointSelector](): reflect.TypeFor[slimmetav1.LabelSelector](),
	reflect.TypeFor[ciliumapi.ICMPField]():        reflect.TypeFor[ciliumICMPFieldShape](),
	reflect.TypeFor[ciliumlabels.Label]():         reflect.TypeFor[ciliumLabelShape](),
}

// CiliumSelfDecodedShapes returns a copy of the shapes UnknownCiliumKeyPath
// checks, for a test that holds them to the Cilium types.
func CiliumSelfDecodedShapes() SelfDecodedShapes {
	out := make(SelfDecodedShapes, len(ciliumSelfDecodedShapes))
	for self, shape := range ciliumSelfDecodedShapes {
		out[self] = shape
	}
	return out
}

// UnknownCiliumKeyPath returns the path of the first key of src that sits
// inside a Cilium type that decodes itself (an endpoint selector, an ICMP field
// or a rule label, wherever T holds one) and that the type does not declare, or
// "" when there is none. T is the Cilium type src decodes into, or one that holds it.
//
// DecodeStrictJSON[T] cannot refuse such a key: encoding/json hands the value
// to the type's own UnmarshalJSON, which drops it. A caller that decoded a
// policy rule runs this on the same src and refuses the path it returns.
func UnknownCiliumKeyPath[T any](src map[string]any) string {
	return UnknownJSONFieldPathIn[T](src, ciliumSelfDecodedShapes)
}
