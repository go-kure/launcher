package components

import (
	notificationv1 "github.com/fluxcd/notification-controller/api/v1"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// fluxcdReceiverType is the component type of the Receiver kind. It takes the
// prefix of `fluxcd-alert` and `fluxcd-provider`: a bare `receiver` names
// nothing.
const fluxcdReceiverType = "fluxcd-receiver"

// FluxcdReceiverHandler handles OAM fluxcd-receiver components: the kind-named
// projection of a notification.toolkit.fluxcd.io/v1 Receiver
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// notificationv1.ReceiverSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the Receiver, named after the component unless
// `objectName` names it, in the Flux namespace when one is set and else in the
// build namespace, and nothing else. No environment policy applies: see
// fluxKind, and the README for what `resources` reaches and the inbound path a
// Receiver opens. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type FluxcdReceiverHandler struct{}

// CanHandle returns true for the fluxcd-receiver component type.
func (h *FluxcdReceiverHandler) CanHandle(componentType string) bool {
	return componentType == fluxcdReceiverType
}

// ContractMetadata implements oam.ContractDescriber.
func (h *FluxcdReceiverHandler) ContractMetadata() oam.ContractMetadata {
	return contract(fluxcdReceiverType)
}

// ComponentObject declares the fluxcd-receiver kind's Receiver, which lands in
// the Flux namespace when one is set.
func (h *FluxcdReceiverHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: notificationv1.GroupVersion.Group, Kind: notificationv1.ReceiverKind}, oam.ObjectScopeFlux
}

// PropertySchema declares every top-level notificationv1.ReceiverSpec field by
// its json name.
func (h *FluxcdReceiverHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Receiver spec."
	return map[string]oam.PropertySchema{
		"type":     fluxSourceRequiredString("Required. " + spec + "type: the sender of the webhook, such as `github`, `gitlab`, `generic-hmac` or `generic-oidc`, which decides how a request is validated; the API lists the values it takes."),
		"interval": fluxSourceString(spec + "interval: how often the Receiver is reconciled with its Secret, as a Flux duration; the API defaults it to 10m."),
		"events": {
			Type: oam.PropertyTypeArray, Description: spec + "events: the event types handled, such as `push` for GitHub or `Push Hook` for GitLab.",
			Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One event type."},
		},
		"resources": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "resources: the Flux objects a webhook makes the controller reconcile. An entry may name another namespace, and with the name `*` every object of a kind there, or with `matchLabels` every one that carries those labels: nothing holds it to the application's own namespace.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One resource: `kind` and `name` (`*` for every object of the kind), both required, `apiVersion`, `namespace`, `matchLabels`, which narrows the name `*` to the objects that carry those labels and which the API documents as needing that name, and `filter`, a CEL expression the controller evaluates.",
			},
		},
		"resourceFilter": fluxSourceString(spec + "resourceFilter: a CEL expression the controller evaluates for each resource when a webhook is received; launcher writes it as authored and does not parse it."),
		"secretRef":      fluxSourceObject(spec + "secretRef: the Secret (`name`) that holds the token a request is validated with, in the namespace the object lands in; the API requires it for every type but `generic-oidc`, which must not set it."),
		"oidcProviders": {
			Type: oam.PropertyTypeArray, Description: spec + "oidcProviders: the OIDC issuers a `generic-oidc` Receiver authenticates requests with.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One issuer: `issuerURL` and `validations` (each an `expression` and a `message`), both required, `audience` and `variables` (each a `name` and an `expression`); the expressions are CEL the controller evaluates.",
			},
		},
		"suspend": fluxSourceBool(spec + "suspend: stop handling events for this Receiver."),
	}
}

// fluxcdReceiverKind is the fluxcd-receiver kind: see fluxKind. The API
// requires `type` and `resources`, of a resource its `kind` and `name`, of an
// authored `secretRef` its `name`, and of an OIDC provider its `issuerURL` and
// `validations` with the fields of each validation and variable; the type would
// write each one empty. TestFluxKinds_RequiredMatchMarkers holds the list to
// the markers of the upstream source. The API's expression rules, which tie
// `secretRef` and `oidcProviders` to `type`, are left to the API server
// (fluxRulesLeft). The object reads the Secret of `secretRef` from its own
// namespace.
var fluxcdReceiverKind = &fluxKind[notificationv1.ReceiverSpec]{
	policyFreeKind: policyFreeKind[notificationv1.ReceiverSpec]{
		upstream: "notification.toolkit.fluxcd.io/v1 ReceiverSpec",
		required: map[string]string{
			"type":                        "the sender of the webhook, such as `github` or `generic-hmac`",
			"resources":                   "the Flux objects a webhook makes the controller reconcile",
			"resources[].kind":            "the kind of the objects, such as GitRepository or OCIRepository",
			"resources[].name":            "the name of the object, or `*` for every object of the kind, or with `matchLabels` every one that carries those labels",
			"secretRef.name":              "the name of the Secret that holds the token a request is validated with",
			"oidcProviders[].issuerURL":   "the URL of the OIDC issuer, which must match the `iss` claim of its tokens",
			"oidcProviders[].validations": "the CEL expressions a token's claims must satisfy",
			"oidcProviders[].validations[].expression": "the CEL expression evaluated against the token's claims",
			"oidcProviders[].validations[].message":    "the message returned when the expression is false",
			"oidcProviders[].variables[].name":         "the name of the variable, a CEL identifier",
			"oidcProviders[].variables[].expression":   "the CEL expression that defines the variable",
		},
		build: func(name, namespace string, spec *notificationv1.ReceiverSpec) client.Object {
			receiver := fluxcd.CreateReceiver(name, namespace)
			spec.DeepCopyInto(&receiver.Spec)
			return receiver
		},
	},
	reads: func(spec *notificationv1.ReceiverSpec, r *fluxReads) {
		r.secretRef(spec.SecretRef)
	},
	durations: []fluxDurationField[notificationv1.ReceiverSpec]{
		{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *notificationv1.ReceiverSpec) *metav1.Duration { return s.Interval }},
	},
}

// ToApplicationConfig decodes an OAM fluxcd-receiver component into its config.
func (h *FluxcdReceiverHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return fluxcdReceiverKind.config(component)
}
