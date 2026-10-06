package components

import (
	"net/url"
	"strings"

	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	"github.com/go-kure/kure/pkg/kubernetes/fluxcd"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// fluxcdProviderType is the component type of the Provider kind. It takes the
// prefix of `fluxcd-alert`, whose events it sends: a bare `provider` names
// nothing.
const fluxcdProviderType = "fluxcd-provider"

// FluxcdProviderHandler handles OAM fluxcd-provider components: the kind-named
// projection of a notification.toolkit.fluxcd.io/v1beta3 Provider
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// notificationv1beta3.ProviderSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the Provider, named after the component unless
// `objectName` names it, in the Flux namespace when one is set and else in the
// build namespace, and nothing else. No dimension of the environment policy
// reaches it: see fluxKind, and the README for where a Provider sends events.
// A user or a password in `address` or `proxy` is refused under every policy
// and under none. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type FluxcdProviderHandler struct{}

// CanHandle returns true for the fluxcd-provider component type.
func (h *FluxcdProviderHandler) CanHandle(componentType string) bool {
	return componentType == fluxcdProviderType
}

// ContractMetadata implements oam.ContractDescriber.
func (h *FluxcdProviderHandler) ContractMetadata() oam.ContractMetadata {
	return contract(fluxcdProviderType)
}

// ComponentObject declares the fluxcd-provider kind's Provider, which lands in
// the Flux namespace when one is set.
func (h *FluxcdProviderHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return schema.GroupKind{Group: notificationv1beta3.GroupVersion.Group, Kind: notificationv1beta3.ProviderKind}, oam.ObjectScopeFlux
}

// PropertySchema declares every top-level notificationv1beta3.ProviderSpec
// field by its json name.
func (h *FluxcdProviderHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Provider spec."
	return map[string]oam.PropertySchema{
		"type":               fluxSourceRequiredString("Required. " + spec + "type: the implementation that sends the events, such as `slack`, `generic`, `github` or `alertmanager`; the API lists the values it takes."),
		"interval":           fluxSourceString(spec + "interval: deprecated upstream and not used by this version of the API; a Flux duration."),
		"channel":            fluxSourceString(spec + "channel: the channel the events are posted to."),
		"username":           fluxSourceString(spec + "username: the name the events are posted under."),
		"address":            fluxSourceString(spec + "address: where the events are sent: an HTTP or HTTPS address for some types, a project ID or a namespace for others. It is written to the object in plain text and must carry no user or password; an address that is itself a credential, such as a webhook URL, belongs in the Secret `secretRef` names."),
		"timeout":            fluxSourceString(spec + "timeout: the timeout of sending an event, as a Flux duration in the units `ms`, `s` and `m`."),
		"proxy":              fluxSourceString(spec + "proxy: the HTTP or HTTPS address of a proxy; deprecated upstream for proxySecretRef. It must carry no user or password."),
		"proxySecretRef":     fluxSourceObject(spec + "proxySecretRef: the Secret (`name`) that holds the proxy's `address` and, optionally, its `username` and `password`, in the namespace the object lands in."),
		"secretRef":          fluxSourceObject(spec + "secretRef: the Secret (`name`) that holds the credentials of the provider, in the namespace the object lands in."),
		"serviceAccountName": fluxSourceString(spec + "serviceAccountName: the ServiceAccount the controller authenticates as with a cloud provider, through workload identity; the API names the types that take it and the feature gate it needs."),
		"certSecretRef":      fluxSourceObject(spec + "certSecretRef: the Secret (`name`) that holds a CA certificate (`ca.crt`), a client certificate (`tls.crt`, `tls.key`) or both, in the namespace the object lands in."),
		"suspend":            fluxSourceBool(spec + "suspend: stop handling events for this Provider."),
		"commitStatusExpr":   fluxSourceString(spec + "commitStatusExpr: a CEL expression the controller evaluates to the message of a commit status; launcher writes it as authored and does not parse it."),
	}
}

// providerAddressRemedy and providerProxyRemedy complete refuseFieldUserinfo's
// message for the two URLs a Provider carries: each is written to the object
// in plain text, and each has a Secret to hold a credential instead.
const (
	providerAddressRemedy = ", which would be written in plain text into the Provider; an address that holds a credential belongs in the Secret secretRef names"
	providerProxyRemedy   = ", which would be written in plain text into the Provider; use proxySecretRef, whose Secret holds the proxy's address, username and password"
)

// refuseProviderUserinfo refuses a user or a password in a Provider's `address`
// or `proxy` (refuseFieldUserinfo). The API takes values in `address` that are
// no URL (a project ID, a namespace): one that does not parse as a URL is
// refused only when it holds an `@`, without which it can carry no user, and
// the refusal does not name the value. A credential in a path or a query, as a
// webhook URL has, is not something this can tell: the README says where such
// an address belongs.
func refuseProviderUserinfo(spec *notificationv1beta3.ProviderSpec) error {
	if _, err := url.Parse(spec.Address); err != nil {
		if strings.Contains(spec.Address, "@") {
			return errors.Errorf("%s: address holds an @ and is not a valid URL, so a user or password in it cannot be ruled out; an address that holds a credential belongs in the Secret secretRef names", fluxcdProviderType)
		}
	} else if err := refuseFieldUserinfo(fluxcdProviderType, "address", spec.Address, providerAddressRemedy); err != nil {
		return err
	}
	return refuseFieldUserinfo(fluxcdProviderType, "proxy", spec.Proxy, providerProxyRemedy)
}

// fluxcdProviderKind is the fluxcd-provider kind: see fluxKind. The API
// requires `type`, which the type would write empty, and of an authored Secret
// reference its `name`; TestFluxKinds_RequiredMatchMarkers holds the list to
// the markers of the upstream source. `interval` and `timeout` are each held
// to the form their pattern takes: `timeout` takes no h, and one of an hour or
// more is written in minutes (emitFluxKind). The hosts of `address` and of
// `proxy` are held to no policy; a user or a password in either is refused
// (refuseProviderUserinfo). The object reads up to three Secrets from its own
// namespace.
var fluxcdProviderKind = &fluxKind[notificationv1beta3.ProviderSpec]{
	policyFreeKind: policyFreeKind[notificationv1beta3.ProviderSpec]{
		upstream: "notification.toolkit.fluxcd.io/v1beta3 ProviderSpec",
		validate: refuseProviderUserinfo,
		required: map[string]string{
			"type":                "the implementation that sends the events, such as `slack` or `generic`",
			"secretRef.name":      "the name of the Secret that holds the credentials of the provider",
			"proxySecretRef.name": "the name of the Secret that holds the configuration of the proxy",
			"certSecretRef.name":  "the name of the Secret that holds the certificates the provider is connected to with",
		},
		build: func(name, namespace string, spec *notificationv1beta3.ProviderSpec) client.Object {
			provider := fluxcd.CreateProvider(name, namespace)
			spec.DeepCopyInto(&provider.Spec)
			return provider
		},
	},
	reads: func(spec *notificationv1beta3.ProviderSpec, r *fluxReads) {
		r.secretRef(spec.SecretRef)
		r.secretRef(spec.ProxySecretRef)
		r.secretRef(spec.CertSecretRef)
	},
	durations: []fluxDurationField[notificationv1beta3.ProviderSpec]{
		{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *notificationv1beta3.ProviderSpec) *metav1.Duration { return s.Interval }},
		{path: []string{"timeout"}, form: fluxduration.SourceTimeout, get: func(s *notificationv1beta3.ProviderSpec) *metav1.Duration { return s.Timeout }},
	},
}

// ToApplicationConfig decodes an OAM fluxcd-provider component into its config.
func (h *FluxcdProviderHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return fluxcdProviderKind.config(component)
}
