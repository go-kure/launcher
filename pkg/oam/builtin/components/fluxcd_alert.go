package components

import (
	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// fluxcdAlertType is the component type of the Alert kind. It is not `alert`,
// which would read as an alerting rule of the monitoring stack
// (`prometheusrule`); the prefix is the one `fluxcd-kustomization` takes.
const fluxcdAlertType = "fluxcd-alert"

// FluxcdAlertHandler handles OAM fluxcd-alert components: the kind-named
// projection of a notification.toolkit.fluxcd.io/v1beta3 Alert
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// notificationv1beta3.AlertSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the Alert, named after the component unless
// `objectName` names it, in the Flux namespace when one is set and else in the
// build namespace, and nothing else. No environment policy applies: see
// fluxKind, and the README for what `eventSources` reaches.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type FluxcdAlertHandler struct{}

// CanHandle returns true for the fluxcd-alert component type.
func (h *FluxcdAlertHandler) CanHandle(componentType string) bool {
	return componentType == fluxcdAlertType
}

// ContractMetadata implements oam.ContractDescriber.
func (h *FluxcdAlertHandler) ContractMetadata() oam.ContractMetadata {
	return contract(fluxcdAlertType)
}

// ComponentObject declares the fluxcd-alert kind's Alert, which lands in the
// Flux namespace when one is set.
func (h *FluxcdAlertHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: notificationv1beta3.GroupVersion.Group, Kind: notificationv1beta3.AlertKind}, oam.ObjectScopeFlux
}

// PropertySchema declares every top-level notificationv1beta3.AlertSpec field
// by its json name.
func (h *FluxcdAlertHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Alert spec."
	regexes := func(desc string) oam.PropertySchema {
		return oam.PropertySchema{
			Type: oam.PropertyTypeArray, Description: desc,
			Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "One Go regular expression."},
		}
	}
	return map[string]oam.PropertySchema{
		"providerRef": {
			Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true,
			Description: "Required. " + spec + "providerRef: the Provider the events are sent to (`name`, required), in the namespace the Alert lands in.",
		},
		"eventSeverity": fluxSourceString(spec + "eventSeverity: `info` (every event, the API's default) or `error`."),
		"eventSources": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "eventSources: the Flux objects whose events are sent. An entry may name another namespace, and with the name `*` every object of a kind there, or with `matchLabels` every one that carries those labels: nothing holds it to the application's own namespace.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One source of events: `kind` and `name` (`*` for every object of the kind), both required, `apiVersion`, `namespace` and `matchLabels`, which narrows the name `*` to the objects that carry those labels and which the API documents as needing that name.",
			},
		},
		"inclusionList": regexes(spec + "inclusionList: only events whose message matches one of these are sent."),
		"exclusionList": regexes(spec + "exclusionList: events whose message matches one of these are not sent."),
		"eventMetadata": fluxSourceObject(spec + "eventMetadata: keys and string values added to every event sent; one the event already carries is kept."),
		"summary":       fluxSourceString(spec + "summary: a short description of the impact, at most 255 characters. Deprecated upstream for eventMetadata."),
		"suspend":       fluxSourceBool(spec + "suspend: stop sending events for this Alert."),
	}
}

// fluxcdAlertKind is the fluxcd-alert kind: see fluxKind. The API requires
// `providerRef` with its `name` and `eventSources`, of a source its `kind` and
// `name`; the type would write each one empty. The API's value rules are left
// to the API server. TestFluxKinds_RequiredMatchMarkers holds the list to the
// markers of the upstream source. An Alert reads no ConfigMap or Secret: the
// Provider it names does.
var fluxcdAlertKind = &fluxKind[notificationv1beta3.AlertSpec]{
	policyFreeKind: policyFreeKind[notificationv1beta3.AlertSpec]{
		upstream: "notification.toolkit.fluxcd.io/v1beta3 AlertSpec",
		required: map[string]string{
			"providerRef":         "the Provider the events are sent to",
			"providerRef.name":    "the name of the Provider, in the namespace the Alert lands in",
			"eventSources":        "the Flux objects whose events are sent",
			"eventSources[].kind": "the kind of the objects, such as Kustomization or HelmRelease",
			"eventSources[].name": "the name of the object, or `*` for every object of the kind, or with `matchLabels` every one that carries those labels",
		},
		build: func(name, namespace string, spec *notificationv1beta3.AlertSpec) client.Object {
			alert := fluxcd.CreateAlert(name, namespace)
			spec.DeepCopyInto(&alert.Spec)
			return alert
		},
	},
}

// ToApplicationConfig decodes an OAM fluxcd-alert component into its config.
func (h *FluxcdAlertHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return fluxcdAlertKind.config(component)
}
