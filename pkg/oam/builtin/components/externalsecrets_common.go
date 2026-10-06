package components

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components of the External Secrets Operator's
// external-secrets.io/v1 API share (go-kure/launcher#790): secretstore,
// clustersecretstore, externalsecret and clusterexternalsecret.
//
// The two stores are policyHeldKinds: the object runs no pod and holds no
// image, but a few providers take a credential as a value written into the
// object, beside the reference to a Secret that holds it. Under an environment
// policy that forbids explicit secrets those values are refused
// (enforceSecretStorePolicy). The two external-secret kinds are
// policyHeldKinds too, for one field: an ExternalSecret names what is read and
// where it is written, and holds no credential field, but it can have the
// operator write an object of another kind instead of a Secret
// (target.manifest), and a kind the environment policy checks is refused there
// (enforceTargetManifest).
//
// What launcher cannot tell from a secret is not checked on any of the four: a
// header or the body of a webhook provider's request, a header of a Vault
// provider, the user part of a provider's URL, and the template of the Secret
// an ExternalSecret writes (target.template.data, a templateFrom literal, the
// template's metadata) are text in which a reference to a fetched value and a
// literal look alike.
//
// The API's types publish no field descriptions and the module ships no CRD,
// so the markers its CRDs are generated from are the source, as for the
// Prometheus operator's kinds. TestExternalSecretsKinds_RequiredMatchSource
// holds each kind's required list to them, TestExternalSecretsKinds_Rules
// every expression and property-count rule to what the kinds do about it, and
// TestExternalSecretsKinds_DefaultedZeros the claim that no authored 0 or
// false is lost on these types: the stores refuse one on the fields of
// secretStoreDefaultedZeros, and no other field is of that shape.
//
// A host these objects name is one the operator reaches, not an artifact
// source: a provider's API, a Vault, a webhook. None is held to the
// environment policy's allowed registries.

// What a required field of these kinds says of itself where the list is
// generated: a field the API requires, the name of what a reference refers
// to, or a field the API would default (the verb takes that default). See
// secretStoreRequired.
const (
	externalSecretsRequiresField    = "the external-secrets API requires this field"
	externalSecretsRequiresName     = "the external-secrets API requires the name of what this refers to"
	externalSecretsUnappliedDefault = "the object always carries this field, so the external-secrets API's default %s never applies: write the value"
)

// secretStoreRequired is the required list of the secretstore and
// clustersecretstore kinds: the fields the Go types write whether or not they
// were authored and that an author must therefore write. They are of two
// classes. The API requires the first (secretStoreRequiredPaths). The second
// it would default (secretStoreUnappliedDefaults), but a default is applied to
// an absent field only, and the object carries an empty one: the API's enum
// refuses the empty `version` of a Vault provider, and an empty mount path or
// a cache lifetime of zero is not what the default would have been. Beside
// `provider` itself, each is a field of one provider, asked for only where
// that provider (and every block above the field) is authored.
//
// Both classes are generated from the linked module's types
// (zz_generated_externalsecrets_required.go) and not phrased one by one: a
// required path that ends in `name` names what a reference refers to, every
// other one is a field, and a defaulted one names its default.
// TestExternalSecretsKinds_StoreRequiredIsGenerated holds the generated file
// to the derivation, each class in both directions.
var secretStoreRequired = requiredFields(
	generatedRequired(secretStoreRequiredPaths),
	unappliedDefaults(secretStoreUnappliedDefaults),
)

// unappliedDefaults is the required list of the given fields the API would
// default, each mapped to that default.
func unappliedDefaults(defaults map[string]string) map[string]string {
	out := make(map[string]string, len(defaults))
	for path, def := range defaults {
		out[path] = fmt.Sprintf(externalSecretsUnappliedDefault, def)
	}
	return out
}

// generatedRequired is the required list of the given generated paths, each
// saying what its class of path says.
func generatedRequired(paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, path := range paths {
		out[path] = externalSecretsRequiresField
		if path == "name" || strings.HasSuffix(path, ".name") {
			out[path] = externalSecretsRequiresName
		}
	}
	return out
}

// secretStoreDefaultedZeros is the stores' defaulted-zero list for
// refuseUncarriedSpecValues: the fields of esv1.SecretStoreSpec whose encoding
// omits a 0 or false and to which the CRD gives another default, so that the
// API server would replace the authored value. Each maps to that default.
// TestExternalSecretsKinds_DefaultedZeros holds the list to the default
// markers of the linked module's source, in both directions.
var secretStoreDefaultedZeros = defaultedZeroFields{
	api:       "external-secrets",
	defaulter: "API server",
	fields: map[string]string{
		"provider.beyondtrust.server.decrypt":                    "true",
		"provider.infisical.secretsScope.expandSecretReferences": "true",
		"provider.onepasswordSDK.cache.maxSize":                  "100",
	},
}

// secretStoreSchema returns the properties of the secretstore and
// clustersecretstore kinds: the top-level fields of esv1.SecretStoreSpec,
// which the two objects share. kind names the object in each description
// ("SecretStore").
func secretStoreSchema(kind string) map[string]oam.PropertySchema {
	spec := kind + " spec."
	return map[string]oam.PropertySchema{
		"controller": {
			Type:        oam.PropertyTypeString,
			Description: spec + "controller: the name of the operator instance that reconciles the store, where several run; an instance started with a controller name reads only the stores that name it.",
		},
		"provider": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "provider: the provider the store reads from, under its key (aws, vault, kubernetes, webhook and the others the API lists). Required, and exactly one key. What the API requires inside the provider is required here too. Under an environment policy that forbids explicit secrets, a credential written as a `value` beside a `secretRef` is refused, and so is the data of the `fake` provider. Decoded strictly into the operator's API type: see SecretStoreProvider in its API reference.",
		},
		"retrySettings": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "retrySettings: how a failed request to the provider is retried: maxRetries and retryInterval.",
		},
		"refreshInterval": {
			Types:       []oam.PropertyType{oam.PropertyTypeInteger, oam.PropertyTypeString},
			Description: spec + "refreshInterval: the store's refresh interval, as a number of seconds or a duration string such as \"5m\". Unset or 0, the operator's own setting applies.",
		},
		"conditions": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "conditions: the namespaces whose ExternalSecrets may read from the store. Read on a ClusterSecretStore only.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One condition: namespaceSelector (a label query over namespaces), namespaces (their names) and namespaceRegexes (expressions matched against their names).",
			},
		},
	}
}

// validateSecretStore refuses a store whose `provider` configures no provider
// or more than one: the API's property-count rule on SecretStoreProvider. A
// provider key written as null configures nothing, as on the API server.
func validateSecretStore(spec *esv1.SecretStoreSpec) error {
	set := authoredProviders(spec.Provider)
	switch len(set) {
	case 1:
		return nil
	case 0:
		return errors.New("provider: configures no provider; the API takes exactly one")
	default:
		return errors.Errorf("provider: configures %d providers (%s); the API takes exactly one", len(set), strings.Join(set, ", "))
	}
}

// authoredProviders returns the json names of the providers p configures,
// sorted. Every field of esv1.SecretStoreProvider is a pointer to one
// provider's configuration (TestExternalSecretsKinds_ProviderUnion).
func authoredProviders(p *esv1.SecretStoreProvider) []string {
	if p == nil {
		return nil
	}
	var out []string
	v := reflect.ValueOf(p).Elem()
	for i := range v.NumField() {
		if f := v.Field(i); f.Kind() == reflect.Pointer && !f.IsNil() {
			name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// secretStoreInlineCredentials lists the fields of a store's provider that can
// hold a credential in the object: each is a union of a `value` and a
// `secretRef`, and what it holds is a secret. value returns the authored
// `value`, "" for none.
//
// The same union holds an identifier on other fields, which are not listed and
// not refused: a Delinea client ID, a Secret Server username, a BeyondTrust
// client ID and certificate, a Scaleway access key, a Barbican username and
// application credential ID. TestExternalSecretsKinds_InlineValues finds every
// such union in the linked module and holds each to one of the two answers.
var secretStoreInlineCredentials = []struct {
	at    string
	value func(p *esv1.SecretStoreProvider) string
}{
	{"provider.beyondtrust.auth.apiKey", func(p *esv1.SecretStoreProvider) string {
		return beyondtrustValue(p, func(a *esv1.BeyondtrustAuth) *esv1.BeyondTrustProviderSecretRef { return a.APIKey })
	}},
	{"provider.beyondtrust.auth.certificateKey", func(p *esv1.SecretStoreProvider) string {
		return beyondtrustValue(p, func(a *esv1.BeyondtrustAuth) *esv1.BeyondTrustProviderSecretRef { return a.CertificateKey })
	}},
	{"provider.beyondtrust.auth.clientSecret", func(p *esv1.SecretStoreProvider) string {
		return beyondtrustValue(p, func(a *esv1.BeyondtrustAuth) *esv1.BeyondTrustProviderSecretRef { return a.ClientSecret })
	}},
	{"provider.delinea.clientSecret", func(p *esv1.SecretStoreProvider) string {
		if p.Delinea == nil || p.Delinea.ClientSecret == nil {
			return ""
		}
		return p.Delinea.ClientSecret.Value
	}},
	{"provider.scaleway.secretKey", func(p *esv1.SecretStoreProvider) string {
		if p.Scaleway == nil || p.Scaleway.SecretKey == nil {
			return ""
		}
		return p.Scaleway.SecretKey.Value
	}},
	{"provider.secretserver.password", func(p *esv1.SecretStoreProvider) string {
		return secretServerValue(p, func(s *esv1.SecretServerProvider) *esv1.SecretServerProviderRef { return s.Password })
	}},
	{"provider.secretserver.token", func(p *esv1.SecretStoreProvider) string {
		return secretServerValue(p, func(s *esv1.SecretServerProvider) *esv1.SecretServerProviderRef { return s.Token })
	}},
}

// beyondtrustValue is the `value` of one credential union of the BeyondTrust
// provider's auth block, "" where nothing is authored down to it.
func beyondtrustValue(p *esv1.SecretStoreProvider, field func(*esv1.BeyondtrustAuth) *esv1.BeyondTrustProviderSecretRef) string {
	if p.Beyondtrust == nil || p.Beyondtrust.Auth == nil {
		return ""
	}
	if ref := field(p.Beyondtrust.Auth); ref != nil {
		return ref.Value
	}
	return ""
}

// secretServerValue is the `value` of one credential union of the Secret
// Server provider, "" where nothing is authored down to it.
func secretServerValue(p *esv1.SecretStoreProvider, field func(*esv1.SecretServerProvider) *esv1.SecretServerProviderRef) string {
	if p.SecretServer == nil {
		return ""
	}
	if ref := field(p.SecretServer); ref != nil {
		return ref.Value
	}
	return ""
}

// enforceSecretStorePolicy holds a SecretStore's or a ClusterSecretStore's
// spec to the environment policy: under one that forbids explicit secrets
// (oam.ExplicitSecretPolicy) a credential written into the object is refused,
// since the object, and with it the credential, is in the build's output. That
// is the `value` of a credential union (secretStoreInlineCredentials) and any
// entry of the `fake` provider's data, which is nothing but values. The
// message quotes nothing of the value. A policy that does not implement that
// interface allows both.
func enforceSecretStorePolicy(spec *esv1.SecretStoreSpec, p oam.Policy) error {
	if oam.ExplicitSecretsAllowed(p) || spec.Provider == nil {
		return nil
	}
	for _, field := range secretStoreInlineCredentials {
		if field.value(spec.Provider) != "" {
			return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, fmt.Sprintf("%s.value: holds the credential in the object, and the environment policy forbids explicit secrets; name the key of a Secret created out of band in %s.secretRef instead", field.at, field.at))
		}
	}
	if fake := spec.Provider.Fake; fake != nil && len(fake.Data) > 0 {
		return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, "provider.fake.data: holds the values the store serves in the object, and the environment policy forbids explicit secrets; the fake provider has no reference to a Secret, so use a provider that reads the values from outside the object")
	}
	return nil
}

// externalSecretSchema returns the properties of the externalsecret kind: the
// top-level fields of esv1.ExternalSecretSpec.
func externalSecretSchema() map[string]oam.PropertySchema {
	const spec = "ExternalSecret spec."
	const decoded = " Decoded strictly into the operator's API type: see "
	return map[string]oam.PropertySchema{
		"secretStoreRef": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "secretStoreRef: the store the values are read from: name, and kind (SecretStore or ClusterSecretStore; unset, the API fills SecretStore). No store is created or looked up: the name is the author's.",
		},
		"target": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "target: the Secret the operator writes: name (unset, the ExternalSecret's), creationPolicy (unset, the API fills Owner), deletionPolicy (unset, Retain), immutable, template (the blueprint of the Secret) and manifest (apiVersion and kind of another resource written instead of a Secret; a kind the environment policy checks, a workload among them, is refused, since what the operator would write is not known at build). The template's text is not checked for a secret written into it." + decoded + "ExternalSecretTarget in its API reference.",
		},
		"refreshPolicy": {
			Type:        oam.PropertyTypeString,
			Description: spec + "refreshPolicy: when the Secret is refreshed: CreatedOnce, Periodic or OnChange.",
		},
		"refreshInterval": {
			Type:        oam.PropertyTypeString,
			Description: spec + "refreshInterval: how long before the values are read again from the provider, as a duration string such as \"1h\". Unset, the API fills 1h0m0s; \"0s\" reads them once.",
		},
		"syncWindows": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "syncWindows: when a periodic refresh may happen: kind (required: allow or deny) and windows (required, at least one), each a schedule (required: a five-field cron expression in UTC, or a shorthand such as @daily) and a duration (required).",
		},
		"data": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "data: the keys of the Secret, each with the provider entry it is read from.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One key: secretKey (required: the key in the Secret), remoteRef (required: key, which is required, and the optional property, version, metadataPolicy, conversionStrategy, decodingStrategy and nullBytePolicy) and sourceRef (another store or a generator to read from)." + decoded + "ExternalSecretData in its API reference.",
			},
		},
		"dataFrom": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "dataFrom: provider entries whose every key is written to the Secret; later entries are merged over earlier ones.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One source: extract (the keys of one provider entry; its key is required), find (the entries a name expression or tags match), rewrite (how the keys are renamed) and sourceRef (another store or a generator to read from)." + decoded + "ExternalSecretDataFromRemoteRef in its API reference.",
			},
		},
	}
}

// externalSecretRequired is the required list of the externalsecret kind: the
// fields the API requires that the Go types write whether or not they were
// authored. None is at the top level: an ExternalSecret that authors nothing
// is one the API takes. TestExternalSecretsKinds_RequiredMatchSource holds the
// list to the source's markers.
var externalSecretRequired = requiredFields(
	map[string]string{ //nolint:gosec // G101: the keys are field paths (secretKey is a field's name) and the values their descriptions
		"data[].secretKey":                        "the key of the Secret the value is written to",
		"data[].remoteRef":                        "the provider entry the value is read from",
		"data[].remoteRef.key":                    "the key of the provider's entry",
		"dataFrom[].extract.key":                  "the key of the provider's entry",
		"dataFrom[].rewrite[].regexp.source":      "the regular expression matched against each key",
		"dataFrom[].rewrite[].regexp.target":      "what a match is replaced with",
		"dataFrom[].rewrite[].transform.template": "the template each key is rewritten with",
		"syncWindows.kind":                        "allow or deny, for every window in the list",
		"syncWindows.windows":                     "the windows; the API wants at least one",
		"syncWindows.windows[].schedule":          "when the window opens: a five-field cron expression in UTC, or a shorthand such as @daily",
		"syncWindows.windows[].duration":          "how long the window stays open",
		"target.manifest.apiVersion":              "the apiVersion of the resource written instead of a Secret",
		"target.manifest.kind":                    "the kind of the resource written instead of a Secret",
	},
	generatorRefRequired("data[].sourceRef.generatorRef"),
	generatorRefRequired("dataFrom[].sourceRef.generatorRef"),
	templateRefRequired("target.template.templateFrom[].configMap", "ConfigMap"),
	templateRefRequired("target.template.templateFrom[].secret", "Secret"),
)

// generatorRefRequired is the required list of one reference to a generator
// under the path at.
func generatorRefRequired(at string) map[string]string {
	return map[string]string{
		at + ".kind": "the kind of the generator",
		at + ".name": "the name of the generator",
	}
}

// templateRefRequired is the required list of one template source under the
// path at: a ConfigMap or a Secret, as kind says, whose keys hold templates.
func templateRefRequired(at, kind string) map[string]string {
	return map[string]string{
		at + ".name":        "the name of the " + kind,
		at + ".items":       "the keys of the " + kind + " that are read as templates",
		at + ".items[].key": "a key of the " + kind,
	}
}

// clusterExternalSecretRequired is the required list of the
// clusterexternalsecret kind: the spec of the ExternalSecrets it creates, and
// under it everything externalSecretRequired lists.
var clusterExternalSecretRequired = func() map[string]string {
	const at = "externalSecretSpec"
	out := map[string]string{at: "the spec of the ExternalSecrets created in the selected namespaces"}
	for path, says := range externalSecretRequired {
		out[at+"."+path] = says
	}
	return out
}()

// validateExternalSecret holds an ExternalSecret's spec to what its object can
// carry: see validateExternalSecretSpec.
func validateExternalSecret(spec *esv1.ExternalSecretSpec) error {
	return validateExternalSecretSpec(spec, "")
}

// validateClusterExternalSecret is validateExternalSecret for the spec of the
// ExternalSecrets a ClusterExternalSecret creates.
func validateClusterExternalSecret(spec *esv1.ClusterExternalSecretSpec) error {
	return validateExternalSecretSpec(&spec.ExternalSecretSpec, "externalSecretSpec.")
}

// validateExternalSecretSpec is what the two kinds refuse of an
// ExternalSecret's spec. at prefixes the path.
func validateExternalSecretSpec(spec *esv1.ExternalSecretSpec, at string) error {
	if err := refuseDataGeneratorRef(spec, at); err != nil {
		return err
	}
	return refuseManifestAPIVersion(spec, at)
}

// refuseManifestAPIVersion refuses a target.manifest whose apiVersion is no
// API version: neither a version nor a group and a version separated by one
// slash (schema.ParseGroupVersion). No object can be written under such a
// value, and it names no group: the kind beside it then reads as no kind at
// all, so neither check of enforceTargetManifest would see a Deployment or a
// Secret there. The API bounds the field by a minimum length only.
func refuseManifestAPIVersion(spec *esv1.ExternalSecretSpec, at string) error {
	manifest := spec.Target.Manifest
	if manifest == nil {
		return nil
	}
	if _, err := schema.ParseGroupVersion(manifest.APIVersion); err != nil {
		return errors.Errorf("%starget.manifest.apiVersion: %q is no API version: want a version (v1) or a group and a version (apps/v1)", at, manifest.APIVersion)
	}
	return nil
}

// refuseDataGeneratorRef refuses a generator named as the source of one key of
// `data`. The API takes exactly one of storeRef and generatorRef in that
// sourceRef (its property-count rule on StoreSourceRef), and the Go type
// writes storeRef whether or not it was authored: with a generatorRef the
// object holds two, which the API server refuses. A source of `dataFrom` holds
// its storeRef behind a pointer and takes a generator. at prefixes the path.
func refuseDataGeneratorRef(spec *esv1.ExternalSecretSpec, at string) error {
	for i, data := range spec.Data {
		//nolint:staticcheck // SA1019: the field is deprecated as not implemented in data[]; it is read here only to refuse it
		if data.SourceRef != nil && data.SourceRef.GeneratorRef != nil {
			return errors.Errorf("%sdata[%d].sourceRef.generatorRef: cannot be carried: the API takes exactly one of storeRef and generatorRef here, and the object always carries a storeRef; read from a generator with %sdataFrom[].sourceRef.generatorRef", at, i, at)
		}
	}
	return nil
}

// enforceExternalSecretPolicy holds an ExternalSecret's spec to the
// environment policy: see enforceTargetManifest.
func enforceExternalSecretPolicy(spec *esv1.ExternalSecretSpec, p oam.Policy) error {
	return enforceTargetManifest(spec, "", p)
}

// enforceClusterExternalSecretPolicy is enforceExternalSecretPolicy for the
// spec of the ExternalSecrets a ClusterExternalSecret creates.
func enforceClusterExternalSecretPolicy(spec *esv1.ClusterExternalSecretSpec, p oam.Policy) error {
	return enforceTargetManifest(&spec.ExternalSecretSpec, "externalSecretSpec.", p)
}

// enforceTargetManifest holds the object an ExternalSecret has the operator
// write instead of a Secret (target.manifest, which names its apiVersion and
// kind) to what the same policy gives an object of that kind on the
// passthrough component, and no more.
//
// What passthrough would have to read cannot be read here: the object's
// content is template text the operator renders in the cluster
// (target.template, a templateFrom entry and its target path). So a kind the
// rendered-object check reads anything from (policyReadsKind: a workload, a
// claim, a PersistentVolume, a HorizontalPodAutoscaler, in any version) is
// refused, whatever the policy's own limits are. A core Secret is asked of
// enforceExplicitSecretObject, the call passthrough makes: refused under a
// policy that forbids explicit secrets, and passed under any other. An object
// of any other kind passes, as it does on passthrough: a ConfigMap, a custom
// resource.
//
// The apiVersion is an API version here: one that is none was refused where the
// spec was decoded (refuseManifestAPIVersion), since its kind would read as no
// kind at all to both checks.
//
// Whether the operator writes such an object at all is the cluster's: its
// generic-target setting and the access it was given. at prefixes the path.
func enforceTargetManifest(spec *esv1.ExternalSecretSpec, at string, p oam.Policy) error {
	manifest := spec.Target.Manifest
	if manifest == nil {
		return nil
	}
	written := &unstructured.Unstructured{}
	written.SetAPIVersion(manifest.APIVersion)
	written.SetKind(manifest.Kind)
	if err := enforceExplicitSecretObject(written, p); err != nil {
		return errors.Wrapf(err, "%starget.manifest", at)
	}
	if policyReadsKind(written.GroupVersionKind()) {
		return oam.NewPolicyRefusal(oam.RefusalUnreadableObject, fmt.Sprintf("%starget.manifest: the operator would write a %s, a kind the environment policy checks, and what the object would hold is not known at build, so it cannot be checked against environment policy", at, manifest.Kind))
	}
	return nil
}

// clusterExternalSecretSchema returns the properties of the
// clusterexternalsecret kind: the top-level fields of
// esv1.ClusterExternalSecretSpec.
func clusterExternalSecretSchema() map[string]oam.PropertySchema {
	const spec = "ClusterExternalSecret spec."
	return map[string]oam.PropertySchema{
		"externalSecretSpec": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "externalSecretSpec: the spec of the ExternalSecret created in each selected namespace: the fields of the externalsecret kind (secretStoreRef, target, refreshPolicy, refreshInterval, syncWindows, data, dataFrom), with what that kind requires and what it refuses of a target.manifest. Required. Decoded strictly into the operator's API type: see ExternalSecretSpec in its API reference.",
		},
		"externalSecretName": {
			Type:        oam.PropertyTypeString,
			Description: spec + "externalSecretName: the name of the ExternalSecrets created. Unset, the ClusterExternalSecret's.",
		},
		"externalSecretMetadata": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "externalSecretMetadata: the labels and annotations of the ExternalSecrets created.",
		},
		"namespaceSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "namespaceSelector: a label query over the namespaces an ExternalSecret is created in (matchLabels, matchExpressions). The API deprecates it for namespaceSelectors.",
		},
		"namespaceSelectors": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "namespaceSelectors: label queries over the namespaces an ExternalSecret is created in; a namespace any of them selects is one.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One label query: matchLabels and matchExpressions.",
			},
		},
		"namespaces": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "namespaces: the names of namespaces an ExternalSecret is created in, beside those the selectors choose. The API deprecates it for namespaceSelectors.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "The name of one namespace."},
		},
		"refreshTime": {
			Type:        oam.PropertyTypeString,
			Description: spec + "refreshTime: how often the operator reads the namespaces' labels again and reconciles the ExternalSecrets, as a duration string such as \"1m\".",
		},
	}
}
