package components

import (
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/go-kure/launcher/pkg/oam"
)

// crdCreateStatusKinds lists the kind components of the Gateway API whose
// object holds a status as the kind emits it: the Go type tags the struct
// `omitempty` alone, which omits no struct, so it is always encoded. (A
// Gateway's, a GatewayClass's and a ListenerSet's are tagged `omitzero` too,
// and are left out when nothing is set.) Each row gives the least the kind may
// author, and required, the field of the status that the CRD's schema requires
// and the emitted status does not hold.
var crdCreateStatusKinds = []struct {
	component string
	crd       string
	handler   oam.ComponentHandler
	props     map[string]any
	required  string
}{
	{
		component: "backendtlspolicy", crd: "backendtlspolicies", handler: &BackendTLSPolicyHandler{},
		props: map[string]any{
			"targetRefs": []any{map[string]any{"group": "", "kind": "Service", "name": "payments"}},
			"validation": map[string]any{"hostname": "payments.internal.example.com", "wellKnownCACertificates": "System"},
		},
		required: "status.ancestors",
	},
	{
		component: "grpcroute", crd: "grpcroutes", handler: &GRPCRouteHandler{},
		props:    map[string]any{"parentRefs": []any{map[string]any{"name": "public"}}},
		required: "status.parents",
	},
	{
		component: "httproute", crd: "httproutes", handler: &HTTPRouteHandler{},
		props:    map[string]any{"parentRefs": []any{map[string]any{"name": "public"}}},
		required: "status.parents",
	},
}

// TestCRDCreate_StatusIsDroppedOnCreate holds crdCreate to the API server on
// the status of a document: where the version has the status subresource, a
// create cannot set the status, so what the document holds there is dropped
// before the object is validated (step 5 of crdCreate).
//
// It reads the step where it decides the answer: on the object a kind emits
// with the empty status of a Go type that always encodes it. That status lacks
// a field the CRD's schema requires. The CRDs of the linked module give the
// version the status subresource, in both channels, and the API server accepts
// the object. The same CRD without the subresource is the control: there the
// status is a part of the object like any other, and the object is refused for
// that one field and nothing else. So the test fails on a harness that does
// not drop the status, and on one that drops it whatever the CRD says.
func TestCRDCreate_StatusIsDroppedOnCreate(t *testing.T) {
	for _, kind := range crdCreateStatusKinds {
		for _, channel := range gatewayAPIChannels {
			t.Run(kind.component+"/"+channel, func(t *testing.T) {
				crd, _ := gatewayAPICRD(t, channel, kind.crd)
				served := crdCreateOf(t, crd, "v1")
				if !served.dropsStatus {
					t.Fatalf("%s has no status subresource in v1; the row shows nothing of the step", crd.Name)
				}
				object, err := checkedRules{create: served, component: kind.component, handler: kind.handler}.build(t, kind.props)
				if err != nil {
					t.Fatalf("the kind refuses the properties: %v", err)
				}
				if _, ok := object["status"]; !ok {
					t.Fatalf("the object the kind emits holds no status; the row shows nothing of the step")
				}

				answer := served.create(t, object)
				answer.accepted(t, "the object the kind emits")
				if _, ok := answer.object["status"]; ok {
					t.Errorf("the validated object holds a status; a create under the status subresource sets none")
				}

				bare := crdCreateOf(t, withoutStatusSubresource(crd), "v1")
				if bare.dropsStatus {
					t.Fatalf("the control still has the status subresource in v1")
				}
				control := bare.create(t, object)
				if _, ok := control.object["status"]; !ok {
					t.Errorf("without the status subresource the status is dropped too; there it is a part of the object")
				}
				if len(control.unknown) > 0 || len(control.rules) > 0 || len(control.schema) != 1 ||
					control.schema[0].Type != field.ErrorTypeRequired || control.schema[0].Field != kind.required {
					t.Errorf("without the status subresource the API server answers %q, want one refusal: %s is required", control, kind.required)
				}
			})
		}
	}
}

// withoutStatusSubresource returns a copy of crd whose versions have no status
// subresource.
func withoutStatusSubresource(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
	crd = crd.DeepCopy()
	for i := range crd.Spec.Versions {
		if sub := crd.Spec.Versions[i].Subresources; sub != nil {
			sub.Status = nil
		}
	}
	return crd
}
