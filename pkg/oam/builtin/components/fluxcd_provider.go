package components

import (
	"fmt"
	"net/url"
	"slices"
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
// build namespace, and nothing else. One dimension of the environment policy
// reaches it: under a policy that forbids explicit secrets, an `address` that is
// itself a credential is refused (enforceProviderPolicy); see the README for
// where a Provider sends events. A user or a password in `address` or `proxy`
// is refused under every policy and under none. TestCoreKindSchemas_CoverSpec
// keeps the published key set equal to the upstream json tags.
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
		"address":            fluxSourceString(spec + "address: where the events are sent: an HTTP or HTTPS address for some types, a project ID or a namespace for others. It is written to the object in plain text and must carry no user or password. An address that is itself a credential, such as a webhook URL, belongs under the `address` key of the Secret `secretRef` names, which the controller reads in place of this field; under a policy that forbids explicit secrets it is refused here for the types whose address is one."),
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
// no URL (a project ID, a namespace): one that is no URL with a host is refused
// only when it holds an `@`, without which it can carry no user, and the
// refusal does not name the value. Parsing alone does not tell: `bot:pw@host`
// parses as an opaque URL of scheme `bot`, and `bot@host` as a path, neither
// with a user. A credential in a path or a query, as a webhook URL has, is not
// something this can tell: the README says where such an address belongs.
// `proxy`, which the API takes only as an http or https URL, is held the same
// way (refuseHostedFieldUserinfo).
func refuseProviderUserinfo(spec *notificationv1beta3.ProviderSpec) error {
	if u, err := url.Parse(spec.Address); err == nil && u.Host != "" {
		if err := refuseFieldUserinfo(fluxcdProviderType, "address", spec.Address, providerAddressRemedy); err != nil {
			return err
		}
	} else if strings.Contains(spec.Address, "@") {
		return errors.Errorf("%s: address holds an @ and is no URL with a host, so a user or password in it cannot be ruled out; an address that holds a credential belongs in the Secret secretRef names", fluxcdProviderType)
	}
	return refuseHostedFieldUserinfo(fluxcdProviderType, "proxy", spec.Proxy, providerProxyRemedy)
}

// providerAddressCredentialTypes are the Provider types whose `address` is the
// credential, as the notifier each type builds reads it (internal/notifier at
// notification-controller v1.9.4):
//
//   - discord, rocket, msteams, googlechat and lark take the address and no
//     token (factory.go:262-276, :298-300): a webhook URL, whose path or query
//     holds the token, is all they post with;
//   - slack takes a token from the Secret where one is set, and posts to the
//     address as an incoming webhook where none is (factory.go:258-260); which
//     of the two a Provider is, the Secret decides, and it is not read here;
//   - generic and generic-hmac post to the address as written
//     (forwarder.go:47-61), and nothing tells an endpoint from one whose path or
//     query is the credential.
//
// TestProviderAddressCredentialTypes_InUpstreamEnum holds every entry to the
// values the API takes.
var providerAddressCredentialTypes = []string{
	notificationv1beta3.DiscordProvider,
	notificationv1beta3.GenericProvider,
	notificationv1beta3.GenericHMACProvider,
	notificationv1beta3.GoogleChatProvider,
	notificationv1beta3.LarkProvider,
	notificationv1beta3.MSTeamsProvider,
	notificationv1beta3.RocketProvider,
	notificationv1beta3.SlackProvider,
}

// providerSASKey marks an azureeventhub address that is a connection string
// with its shared access key: the notifier connects with such an address as
// written (isSASAuth and newSASHub, azure_eventhub.go:53-57 and :167-169 at
// notification-controller v1.9.4). Any other azureeventhub address is an
// endpoint, and the credential is a token or a workload identity.
const providerSASKey = "SharedAccessKey"

// providerWebhookRemedy is what a refused address is replaced with. The
// controller reads the `address` key of the Secret `secretRef` names in place of
// `address`, for every type (internal/server/event_handlers.go:479 and
// :485-503 at notification-controller v1.9.4), and `address` is optional.
const providerWebhookRemedy = "omit address, set secretRef, and put the URL under the address key of that Secret, which the controller reads in place of address"

// enforceProviderPolicy holds a Provider's spec to the environment policy p:
// under one that forbids explicit secrets (oam.ExplicitSecretPolicy) an
// `address` that is itself a credential is refused, since the object, and with
// it the address, is in the build's output. That is a non-empty address of a
// type in providerAddressCredentialTypes, and an azureeventhub address that
// holds a shared access key. The message names the type and quotes nothing of
// the value. A policy that does not implement that interface allows it, and so
// does an empty address, which the type leaves out of the object.
func enforceProviderPolicy(spec *notificationv1beta3.ProviderSpec, p oam.Policy) error {
	if oam.ExplicitSecretsAllowed(p) || spec.Address == "" {
		return nil
	}
	switch {
	case slices.Contains(providerAddressCredentialTypes, spec.Type):
		return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, fmt.Sprintf("address: the address of a %s Provider is the credential it posts with, and the environment policy forbids explicit secrets; %s", spec.Type, providerWebhookRemedy))
	case spec.Type == notificationv1beta3.AzureEventHubProvider && strings.Contains(spec.Address, providerSASKey):
		return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, fmt.Sprintf("address: the address of an %s Provider holds a %s, so it is a connection string with its key, and the environment policy forbids explicit secrets; %s", notificationv1beta3.AzureEventHubProvider, providerSASKey, providerWebhookRemedy))
	}
	return nil
}

// fluxcdProviderKind is the fluxcd-provider kind: see fluxKind. The API
// requires `type`, which the type would write empty, and of an authored Secret
// reference its `name`; TestFluxKinds_RequiredMatchMarkers holds the list to
// the markers of the upstream source. `interval` and `timeout` are each held
// to the form their pattern takes: `timeout` takes no h, and one of an hour or
// more is written in minutes (emitFluxKind). The hosts of `address` and of
// `proxy` are held to no policy; a user or a password in either is refused
// (refuseProviderUserinfo), and an `address` that is a credential is refused
// under a policy that forbids explicit secrets (enforceProviderPolicy). The
// object reads up to three Secrets from its own namespace.
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
	enforce: enforceProviderPolicy,
	durations: []fluxDurationField[notificationv1beta3.ProviderSpec]{
		{path: []string{"interval"}, form: fluxduration.Interval, get: func(s *notificationv1beta3.ProviderSpec) *metav1.Duration { return s.Interval }},
		{path: []string{"timeout"}, form: fluxduration.SourceTimeout, get: func(s *notificationv1beta3.ProviderSpec) *metav1.Duration { return s.Timeout }},
	},
}

// ToApplicationConfig decodes an OAM fluxcd-provider component into its config.
func (h *FluxcdProviderHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return fluxcdProviderKind.config(component)
}
