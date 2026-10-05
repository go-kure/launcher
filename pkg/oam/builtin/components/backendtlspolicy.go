package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// BackendTLSPolicyHandler handles OAM backendtlspolicy components: the
// kind-named projection of a gateway.networking.k8s.io/v1 BackendTLSPolicy
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// gatewayv1.BackendTLSPolicySpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the BackendTLSPolicy, named after the component
// unless `objectName` names it, in the build namespace, and nothing else: no
// Service it targets and no ConfigMap of CA certificates it names.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type BackendTLSPolicyHandler struct{}

// CanHandle returns true for the backendtlspolicy component type.
func (h *BackendTLSPolicyHandler) CanHandle(componentType string) bool {
	return componentType == "backendtlspolicy"
}

// PropertySchema declares every top-level gatewayv1.BackendTLSPolicySpec
// field by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *BackendTLSPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "BackendTLSPolicy spec."
	return map[string]oam.PropertySchema{
		"targetRefs": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "targetRefs: the backends of the policy's namespace it applies to, usually Services, at least one and at most 16.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One backend: `group` (\"\" is the core group), `kind` and `name`, all three required, and `sectionName`, for a Service the name of one of its ports." + gatewayDecoded + "LocalPolicyTargetReferenceWithSectionName in its API reference.",
			},
		},
		"validation": {
			Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true,
			Description: "Required. " + spec + "validation: how the Gateway validates the certificate a backend presents: `hostname`, required, the name it sends as SNI and matches the certificate against; one of `caCertificateRefs` (`group`, `kind` and `name` of each, all three required) and `wellKnownCACertificates` (System); and `subjectAltNames` (`type`, required, with `hostname` or `uri`)." + gatewayDecoded + "BackendTLSPolicyValidation in its API reference.",
		},
		"options": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "options: implementation-specific TLS settings, at most 16 keys, as the controller defines them. Written as authored.",
		},
	}
}

// backendTLSPolicyKind is the backendtlspolicy kind: see policyFreeKind. The
// API requires `validation` with its hostname, which the type would write
// empty, and at least one of `targetRefs`, which the type omits when empty:
// validate refuses that one, as the API server refuses a BackendTLSPolicy
// without it. Of a target, a CA certificate reference and a subject
// alternative name that are authored, it requires the fields the list names.
// The API's value rules, those it writes as expressions included (one of
// caCertificateRefs and wellKnownCACertificates, a subject alternative name
// against its type), are left to the API server.
// TestGatewayKinds_RequiredMatchCRD holds the list to the CRD.
var backendTLSPolicyKind = &policyFreeKind[gatewayv1.BackendTLSPolicySpec]{
	upstream: "gateway.networking.k8s.io/v1 BackendTLSPolicySpec",
	required: requiredFields(
		map[string]string{
			"validation":                        "how the Gateway validates the certificate a backend presents",
			"validation.hostname":               "the name the Gateway sends as SNI and matches the backend's certificate against",
			"validation.subjectAltNames[].type": "the type of the subject alternative name: Hostname or URI",
		},
		gatewayReferenceRequired("the backend the policy applies to", "targetRefs[]"),
		gatewayReferenceRequired("the object that holds the CA certificates", "validation.caCertificateRefs[]"),
	),
	validate: func(spec *gatewayv1.BackendTLSPolicySpec) error {
		if len(spec.TargetRefs) == 0 {
			return errors.New("targetRefs: required (at least one backend the policy applies to)")
		}
		return nil
	},
	build: func(name, namespace string, spec *gatewayv1.BackendTLSPolicySpec) client.Object {
		policy := kubernetes.CreateBackendTLSPolicy(name, namespace)
		spec.DeepCopyInto(&policy.Spec)
		return policy
	},
}

// ToApplicationConfig decodes an OAM backendtlspolicy component into its
// config. The object takes the namespace of the application it is generated
// in.
func (h *BackendTLSPolicyHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return backendTLSPolicyKind.config(component)
}
