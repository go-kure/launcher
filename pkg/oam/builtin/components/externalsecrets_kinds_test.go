package components_test

import (
	"fmt"
	"strings"
	"testing"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind components of the External Secrets Operator's API
// (go-kure/launcher#790): secretstore, clustersecretstore, externalsecret and
// clusterexternalsecret. Their fixtures are here; what every kind built on the
// shared helper promises is tested over policyFreeKinds, where each has a row.

// secretStores are the two kinds an esv1.SecretStoreSpec builds.
var secretStores = []struct {
	component string
	handler   oam.ComponentHandler
}{
	{"secretstore", &components.SecretStoreHandler{}},
	{"clustersecretstore", &components.ClusterSecretStoreHandler{}},
}

// externalSecrets are the two kinds an esv1.ExternalSecretSpec is authored
// for: at names where that spec starts in the kind's properties.
var externalSecrets = []struct {
	component string
	handler   oam.ComponentHandler
	upstream  string
	at        string
}{
	{"externalsecret", &components.ExternalSecretHandler{}, "external-secrets.io/v1 ExternalSecretSpec", ""},
	{"clusterexternalsecret", &components.ClusterExternalSecretHandler{}, "external-secrets.io/v1 ClusterExternalSecretSpec", "externalSecretSpec."},
}

// storeProvider returns a store's properties with one provider.
func storeProvider(name string, provider map[string]any) map[string]any {
	return map[string]any{"provider": map[string]any{name: provider}}
}

// secretStoreMinimal is the least a store may author: one provider, with what
// the API requires of it.
func secretStoreMinimal() map[string]any {
	return storeProvider("aws", map[string]any{"service": "SecretsManager", "region": "eu-west-1"})
}

// vaultStore returns a store on a Vault provider with what the API requires of
// one, the version the object would otherwise carry empty included, and the
// given fields over it.
func vaultStore(over map[string]any) map[string]any {
	vault := map[string]any{"server": "https://vault.example.com", "version": "v2"}
	for name, value := range over {
		vault[name] = value
	}
	return storeProvider("vault", vault)
}

// secretStoreFull sets every top-level field of esv1.SecretStoreSpec. It holds
// no credential: a store's fixtures are built with explicit secrets forbidden.
func secretStoreFull() map[string]any {
	full := vaultStore(map[string]any{
		"path": "secret", "namespace": "tenant-a",
		"auth": map[string]any{"kubernetes": map[string]any{
			"mountPath": "kubernetes", "role": "reader",
			"serviceAccountRef": map[string]any{"name": "secrets-reader", "audiences": []any{"vault"}},
		}},
		"caProvider": map[string]any{"type": "ConfigMap", "name": "vault-ca", "key": "ca.crt"},
		"headers":    map[string]any{"X-Tenant": "a"},
	})
	full["controller"] = "tenant-a"
	// An authored 0 is kept: the store is not retried.
	full["retrySettings"] = map[string]any{"maxRetries": 0, "retryInterval": "10s"}
	full["refreshInterval"] = 300
	full["conditions"] = []any{
		map[string]any{
			"namespaceSelector": map[string]any{"matchLabels": map[string]any{"tenant": "a"}},
			"namespaces":        []any{"shop", "billing"},
		},
		map[string]any{"namespaceRegexes": []any{"^tenant-a-.*$"}},
	}
	return full
}

// externalSecretFull sets every top-level field of esv1.ExternalSecretSpec.
func externalSecretFull() map[string]any {
	return map[string]any{
		"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"},
		"target": map[string]any{
			"name": "web-credentials", "creationPolicy": "Merge", "deletionPolicy": "Delete", "immutable": true,
			"template": map[string]any{
				"type": "Opaque", "engineVersion": "v2", "mergePolicy": "Merge",
				"metadata": map[string]any{"labels": map[string]any{"tier": "web"}, "annotations": map[string]any{"owner": "shop"}},
				"data":     map[string]any{"config.yaml": "user: {{ .username }}"},
				"templateFrom": []any{
					map[string]any{"target": "Data", "configMap": map[string]any{
						"name": "web-templates", "items": []any{map[string]any{"key": "config.tpl", "templateAs": "Values"}},
					}},
					map[string]any{"secret": map[string]any{"name": "web-secret-templates", "items": []any{map[string]any{"key": "tls.tpl"}}}},
				},
			},
		},
		"refreshPolicy":   "Periodic",
		"refreshInterval": "0s",
		"syncWindows": map[string]any{"kind": "allow", "windows": []any{
			map[string]any{"schedule": "0 2 * * *", "duration": "1h"},
			map[string]any{"schedule": "@daily", "duration": "30m"},
		}},
		"data": []any{
			map[string]any{"secretKey": "username", "remoteRef": map[string]any{"key": "apps/web", "property": "username", "version": "3"}},
			map[string]any{
				"secretKey": "password", "remoteRef": map[string]any{"key": "apps/web", "property": "password"},
				"sourceRef": map[string]any{"storeRef": map[string]any{"name": "other", "kind": "SecretStore"}},
			},
		},
		"dataFrom": []any{
			map[string]any{"extract": map[string]any{"key": "apps/web/tls"}},
			map[string]any{
				"find": map[string]any{"path": "apps", "name": map[string]any{"regexp": "^web-"}, "tags": map[string]any{"tier": "web"}},
				"rewrite": []any{
					map[string]any{"regexp": map[string]any{"source": "^web-(.*)$", "target": "$1"}},
					map[string]any{"transform": map[string]any{"template": "{{ .value | upper }}"}},
				},
			},
			map[string]any{"sourceRef": map[string]any{"generatorRef": map[string]any{
				"apiVersion": "generators.external-secrets.io/v1alpha1", "kind": "Password", "name": "db-password",
			}}},
		},
	}
}

// clusterExternalSecretOf returns a ClusterExternalSecret's properties around
// the given spec of the ExternalSecrets it creates.
func clusterExternalSecretOf(spec map[string]any) map[string]any {
	return map[string]any{"externalSecretSpec": spec}
}

// clusterExternalSecretFull sets every top-level field of
// esv1.ClusterExternalSecretSpec.
func clusterExternalSecretFull() map[string]any {
	full := clusterExternalSecretOf(externalSecretFull())
	full["externalSecretName"] = "web-credentials"
	full["externalSecretMetadata"] = map[string]any{"labels": map[string]any{"tier": "web"}, "annotations": map[string]any{"owner": "shop"}}
	full["namespaceSelector"] = map[string]any{"matchLabels": map[string]any{"tenant": "a"}}
	full["namespaceSelectors"] = []any{
		map[string]any{"matchLabels": map[string]any{"tenant": "a"}},
		map[string]any{"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"web", "api"}}}},
	}
	full["namespaces"] = []any{"shop", "billing"}
	full["refreshTime"] = "1m"
	return full
}

// externalSecretIn returns the properties of one of the externalSecrets kinds
// around the given ExternalSecret spec.
func externalSecretIn(at string, spec map[string]any) map[string]any {
	if at == "" {
		return spec
	}
	return clusterExternalSecretOf(spec)
}

// secretStoreRefusals are the refusal cases of the two store kinds.
func secretStoreRefusals(notA string) []struct {
	name  string
	props map[string]any
	want  string
} {
	const requires = "required (the external-secrets API requires this field)"
	const requiresName = "required (the external-secrets API requires the name of what this refers to)"
	kubernetesAuth := func(auth map[string]any) map[string]any {
		return vaultStore(map[string]any{"auth": map[string]any{"kubernetes": auth}})
	}
	return []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"no properties", nil, "provider: " + requires},
		{"null provider", map[string]any{"provider": nil}, "provider: " + requires},
		{"a controller alone", map[string]any{"controller": "tenant-a"}, "provider: " + requires},
		// The API's property-count rule on the provider.
		{"an empty provider", map[string]any{"provider": map[string]any{}}, "provider: configures no provider; the API takes exactly one"},
		{"a null provider key", map[string]any{"provider": map[string]any{"aws": nil}}, "provider: configures no provider; the API takes exactly one"},
		{"two providers", map[string]any{"provider": map[string]any{
			"aws":   map[string]any{"service": "SecretsManager", "region": "eu-west-1"},
			"vault": map[string]any{"server": "https://vault.example.com", "version": "v2"},
		}}, "provider: configures 2 providers (aws, vault); the API takes exactly one"},
		// What the API requires inside the authored provider.
		{"aws without a region", storeProvider("aws", map[string]any{"service": "SecretsManager"}), "provider.aws.region: " + requires},
		{"aws without a service", storeProvider("aws", map[string]any{"region": "eu-west-1"}), "provider.aws.service: " + requires},
		{"vault without a server", storeProvider("vault", map[string]any{"version": "v2"}), "provider.vault.server: " + requires},
		{"service account reference without a name", kubernetesAuth(map[string]any{
			"mountPath": "kubernetes", "role": "reader", "serviceAccountRef": map[string]any{"audiences": []any{"vault"}},
		}), "provider.vault.auth.kubernetes.serviceAccountRef.name: " + requiresName},
		{"kubernetes auth without a role", kubernetesAuth(map[string]any{"mountPath": "kubernetes"}), "provider.vault.auth.kubernetes.role: " + requires},
		{"a later session tag without a value", storeProvider("aws", map[string]any{
			"service": "SecretsManager", "region": "eu-west-1",
			"sessionTags": []any{map[string]any{"key": "team", "value": "shop"}, map[string]any{"key": "tier"}},
		}), "provider.aws.sessionTags[1].value: " + requires},
		// A field the API would default and the object always carries.
		{"vault without a version", storeProvider("vault", map[string]any{"server": "https://vault.example.com"}),
			"provider.vault.version: required (the object always carries this field, so the external-secrets API's default v2 never applies: write the value)"},
		{"vault with a null version", storeProvider("vault", map[string]any{"server": "https://vault.example.com", "version": nil}),
			"provider.vault.version: required (the object always carries this field, so the external-secrets API's default v2 never applies: write the value)"},
		{"certificate auth without a path", vaultStore(map[string]any{"auth": map[string]any{"cert": map[string]any{}}}),
			"provider.vault.auth.cert.path: required (the object always carries this field, so the external-secrets API's default cert never applies: write the value)"},
		// An authored false the object cannot carry.
		{"a defaulted false", storeProvider("infisical", map[string]any{
			"auth": map[string]any{},
			"secretsScope": map[string]any{
				"projectSlug": "shop", "environmentSlug": "prod", "organizationSlug": "acme", "expandSecretReferences": false,
			},
		}), "provider.infisical.secretsScope.expandSecretReferences: false cannot be carried by the external-secrets API types (the field is omitted when zero, so the API server would apply its default true)"},
		{"unknown key", withProperty(secretStoreMinimal(), "providers", map[string]any{}), notA + "external-secrets.io/v1 SecretStoreSpec"},
		{"the object's spec", map[string]any{"spec": secretStoreMinimal()}, notA},
		{"unknown provider", storeProvider("keyring", map[string]any{}), notA},
		{"provider sub-key", storeProvider("aws", map[string]any{"service": "SecretsManager", "region": "eu-west-1", "zone": "a"}), notA},
		{"provider a string", map[string]any{"provider": "aws"}, notA},
		{"retries a string", withProperty(secretStoreMinimal(), "retrySettings", map[string]any{"maxRetries": "3"}), notA},
		{"refresh interval a boolean", withProperty(secretStoreMinimal(), "refreshInterval", true), notA},
		{"null condition namespace", withProperty(secretStoreMinimal(), "conditions", []any{map[string]any{"namespaces": []any{"shop", nil}}}), "conditions[0].namespaces[1]"},
		{"two spellings", withProperty(secretStoreMinimal(), "Provider", map[string]any{}), "sets the same field as"},
	}
}

// externalSecretRefusals are the refusal cases of the externalsecret kind and,
// under externalSecretSpec, of the clusterexternalsecret kind.
func externalSecretRefusals(notA, upstream, at string) []struct {
	name  string
	props map[string]any
	want  string
} {
	in := func(spec map[string]any) map[string]any { return externalSecretIn(at, spec) }
	data := func(entries ...any) map[string]any { return in(map[string]any{"data": entries}) }
	dataFrom := func(entries ...any) map[string]any { return in(map[string]any{"dataFrom": entries}) }
	username := map[string]any{"secretKey": "username", "remoteRef": map[string]any{"key": "apps/web"}}
	generator := map[string]any{"kind": "Password", "name": "db-password"}
	return []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"key without a secretKey", data(map[string]any{"remoteRef": map[string]any{"key": "apps/web"}}), at + "data[0].secretKey: required"},
		{"key without a remoteRef", data(username, map[string]any{"secretKey": "password"}), at + "data[1].remoteRef: required"},
		{"remoteRef without a key", data(map[string]any{"secretKey": "username", "remoteRef": map[string]any{"property": "username"}}), at + "data[0].remoteRef.key: required"},
		{"extract without a key", dataFrom(map[string]any{"extract": map[string]any{"property": "tls"}}), at + "dataFrom[0].extract.key: required"},
		{"rewrite without a target", dataFrom(map[string]any{
			"find": map[string]any{"name": map[string]any{"regexp": "^web-"}}, "rewrite": []any{map[string]any{"regexp": map[string]any{"source": "^web-"}}},
		}), at + "dataFrom[0].rewrite[0].regexp.target: required"},
		{"generator without a name", dataFrom(map[string]any{"sourceRef": map[string]any{"generatorRef": map[string]any{"kind": "Password"}}}), at + "dataFrom[0].sourceRef.generatorRef.name: required"},
		{"sync windows without a kind", in(map[string]any{"syncWindows": map[string]any{
			"windows": []any{map[string]any{"schedule": "@daily", "duration": "1h"}},
		}}), at + "syncWindows.kind: required"},
		{"sync windows without windows", in(map[string]any{"syncWindows": map[string]any{"kind": "allow"}}), at + "syncWindows.windows: required"},
		{"window without a duration", in(map[string]any{"syncWindows": map[string]any{
			"kind": "deny", "windows": []any{map[string]any{"schedule": "@daily"}},
		}}), at + "syncWindows.windows[0].duration: required"},
		{"manifest without a kind", in(map[string]any{"target": map[string]any{"manifest": map[string]any{"apiVersion": "v1"}}}), at + "target.manifest.kind: required"},
		{"template source without items", in(map[string]any{"target": map[string]any{"template": map[string]any{
			"templateFrom": []any{map[string]any{"configMap": map[string]any{"name": "web-templates"}}},
		}}}), at + "target.template.templateFrom[0].configMap.items: required"},
		// The API's property-count rule on a key's source: the object always
		// carries its storeRef.
		{"a generator as the source of a key", data(username, map[string]any{
			"secretKey": "password", "remoteRef": map[string]any{"key": "apps/web"}, "sourceRef": map[string]any{"generatorRef": generator},
		}), at + "data[1].sourceRef.generatorRef: cannot be carried: the API takes exactly one of storeRef and generatorRef here, and the object always carries a storeRef; read from a generator with " + at + "dataFrom[].sourceRef.generatorRef"},
		{"a generator beside a store", data(map[string]any{
			"secretKey": "password", "remoteRef": map[string]any{"key": "apps/web"},
			"sourceRef": map[string]any{"storeRef": map[string]any{"name": "other"}, "generatorRef": generator},
		}), at + "data[0].sourceRef.generatorRef: cannot be carried"},
		{"unknown key", in(map[string]any{"secretStore": map[string]any{"name": "vault"}}), notA + upstream},
		{"the object's spec", map[string]any{"spec": in(map[string]any{})}, notA},
		{"target sub-key", in(map[string]any{"target": map[string]any{"secretName": "web-credentials"}}), notA},
		{"data a map", in(map[string]any{"data": username}), notA},
		{"refresh interval a number", in(map[string]any{"refreshInterval": 3600}), notA},
		{"immutable a string", in(map[string]any{"target": map[string]any{"immutable": "true"}}), notA},
		{"null key", data(username, nil), at + "data[1]"},
		{"two spellings", in(map[string]any{"data": []any{}, "Data": []any{}}), "sets the same field as"},
	}
}

// TestSecretStoreKinds_InlineCredential: a credential written into a store as
// the `value` of a union with a `secretRef` is refused under a policy that
// forbids explicit secrets, and the refusal names the field and its
// replacement without the value. The same union holding an identifier is not a
// credential and builds. A policy that allows explicit secrets, one that does
// not answer the question and none passed build each of them, and a credential
// taken from a Secret is built under every one.
func TestSecretStoreKinds_InlineCredential(t *testing.T) {
	refusal := func(at string) string {
		return fmt.Sprintf("%s.value: holds the credential in the object, and the environment policy forbids explicit secrets; "+
			"name the key of a Secret created out of band in %s.secretRef instead", at, at)
	}
	literal := map[string]any{"value": esSentinel}
	fromSecret := map[string]any{"secretRef": secretKey("provider-credentials", "secret")}
	delinea := func(id, secret map[string]any) map[string]any {
		return storeProvider("delinea", map[string]any{"tenant": "acme", "clientId": id, "clientSecret": secret})
	}
	secretServer := func(over map[string]any) map[string]any {
		server := map[string]any{"serverURL": "https://secretserver.example.com"}
		for name, value := range over {
			server[name] = value
		}
		return storeProvider("secretserver", server)
	}
	scaleway := func(access, secret map[string]any) map[string]any {
		return storeProvider("scaleway", map[string]any{"region": "fr-par", "projectId": "0000", "accessKey": access, "secretKey": secret})
	}
	beyondtrust := func(auth map[string]any) map[string]any {
		return storeProvider("beyondtrust", map[string]any{
			"auth": auth, "server": map[string]any{"apiUrl": "https://beyondtrust.example.com", "verifyCA": true},
		})
	}
	policies := map[string]oam.Policy{
		"forbidding":       esForbidding(),
		"allowing":         esPolicy{stubPolicy: &stubPolicy{}, allow: true},
		"no answer":        &stubPolicy{},
		"no policy passed": nil,
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string // under the forbidding policy; "" when the component builds
	}{
		"delinea client secret":            {delinea(fromSecret, literal), refusal("provider.delinea.clientSecret")},
		"a value beside a reference":       {delinea(fromSecret, map[string]any{"value": esSentinel, "secretRef": secretKey("delinea", "secret")}), refusal("provider.delinea.clientSecret")},
		"secret server password":           {secretServer(map[string]any{"username": map[string]any{"value": "reader"}, "password": literal}), refusal("provider.secretserver.password")},
		"secret server token":              {secretServer(map[string]any{"token": literal}), refusal("provider.secretserver.token")},
		"scaleway secret key":              {scaleway(fromSecret, literal), refusal("provider.scaleway.secretKey")},
		"beyondtrust api key":              {beyondtrust(map[string]any{"apiKey": literal}), refusal("provider.beyondtrust.auth.apiKey")},
		"beyondtrust client secret":        {beyondtrust(map[string]any{"clientId": map[string]any{"value": "reader"}, "clientSecret": literal}), refusal("provider.beyondtrust.auth.clientSecret")},
		"beyondtrust certificate key":      {beyondtrust(map[string]any{"certificate": map[string]any{"value": "public"}, "certificateKey": literal}), refusal("provider.beyondtrust.auth.certificateKey")},
		"the first of two reported":        {beyondtrust(map[string]any{"apiKey": literal, "clientSecret": literal}), refusal("provider.beyondtrust.auth.apiKey")},
		"an identifier written as a value": {delinea(map[string]any{"value": "client-0000"}, fromSecret), ""},
		"identifiers only":                 {scaleway(map[string]any{"value": "SCW0000"}, fromSecret), ""},
		"credentials from a Secret":        {secretServer(map[string]any{"username": fromSecret, "password": fromSecret}), ""},
		"no union at all":                  {secretStoreMinimal(), ""},
	} {
		for _, kind := range secretStores {
			for policyName, policy := range policies {
				t.Run(kind.component+"/"+name+"/"+policyName, func(t *testing.T) {
					objs, err := pvTransform(kind.component, kind.handler, tc.props, policy)
					if tc.want != "" && policyName == "forbidding" {
						htWantViolation(t, err, `component "web": `+tc.want)
						rcWantClass(t, err, oam.RefusalExplicitSecret)
						if strings.Contains(err.Error(), esSentinel) {
							t.Errorf("the refusal carries the value: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("transform: %v", err)
					}
					if len(objs) != 1 {
						t.Fatalf("generated %d objects, want one", len(objs))
					}
				})
			}
		}
	}
}

// TestSecretStoreKinds_FakeData: the `fake` provider serves values written
// into the store. An author who writes no `data` is told the API requires it,
// at decode and under any policy; one who writes entries is refused under a
// policy that forbids explicit secrets, without the values; an empty list is
// what both checks let through. So of the two refusals of this one field, the
// required one is the first an author sees, and the policy's the second.
func TestSecretStoreKinds_FakeData(t *testing.T) {
	const required = "provider.fake.data: required (the external-secrets API requires this field)"
	const forbidden = "provider.fake.data: holds the values the store serves in the object, and the environment policy forbids explicit secrets; " +
		"the fake provider has no reference to a Secret, so use a provider that reads the values from outside the object"
	entries := []any{map[string]any{"key": "apps/web", "value": esSentinel}}
	for _, kind := range secretStores {
		t.Run(kind.component, func(t *testing.T) {
			for name, props := range map[string]map[string]any{
				"no data": storeProvider("fake", map[string]any{}), "null data": storeProvider("fake", map[string]any{"data": nil}),
			} {
				// Refused at decode, before any policy is asked.
				if err := coreKindErr(kind.handler, kind.component, "web", props); err == nil || !strings.Contains(err.Error(), required) {
					t.Errorf("%s: err = %v, want one mentioning %q", name, err, required)
				}
				for _, policy := range []oam.Policy{esForbidding(), nil} {
					if _, err := pvTransform(kind.component, kind.handler, props, policy); err == nil || !strings.Contains(err.Error(), required) {
						t.Errorf("%s under %v: err = %v, want one mentioning %q", name, policy, err, required)
					}
				}
			}

			withEntries := storeProvider("fake", map[string]any{"data": entries})
			if err := coreKindErr(kind.handler, kind.component, "web", withEntries); err != nil {
				t.Fatalf("entries decode: %v, want the policy to be the one that refuses them", err)
			}
			_, err := pvTransform(kind.component, kind.handler, withEntries, esForbidding())
			htWantViolation(t, err, `component "web": `+forbidden)
			rcWantClass(t, err, oam.RefusalExplicitSecret)
			if strings.Contains(err.Error(), esSentinel) {
				t.Errorf("the refusal carries a value: %v", err)
			}
			for name, policy := range map[string]oam.Policy{
				"allowing": esPolicy{stubPolicy: &stubPolicy{}, allow: true}, "no answer": &stubPolicy{}, "no policy passed": nil,
			} {
				if objs, err := pvTransform(kind.component, kind.handler, withEntries, policy); err != nil || len(objs) != 1 {
					t.Errorf("entries under the %s policy: %d objects, err %v; want the store", name, len(objs), err)
				}
			}

			// An empty list is authored, and holds no value.
			empty := storeProvider("fake", map[string]any{"data": []any{}})
			if objs, err := pvTransform(kind.component, kind.handler, empty, esForbidding()); err != nil || len(objs) != 1 {
				t.Errorf("an empty list under the forbidding policy: %d objects, err %v; want the store", len(objs), err)
			}
		})
	}
}

// TestExternalSecretsKinds_WrittenUnauthored: what the API's types write into
// the object that the author did not: an empty object where the type holds one
// by value, and an empty string where a string field is not omitted. The API
// accepts each as written here, and the kinds' README entries state them. A
// field the API would refuse or misread empty is not among them: the kind
// refuses it unauthored (secretStoreRefusals, "vault without a version").
func TestExternalSecretsKinds_WrittenUnauthored(t *testing.T) {
	spec := func(component string, h oam.ComponentHandler, props map[string]any) string {
		objs, err := pvTransform(component, h, props, nil)
		if err != nil || len(objs) != 1 {
			t.Fatalf("transform: %d objects, err %v", len(objs), err)
		}
		return fmt.Sprint(policyFreeJSON(t, objs[0])["spec"])
	}
	for name, tc := range map[string]struct {
		component string
		handler   oam.ComponentHandler
		props     map[string]any
		want      string
	}{
		"an ExternalSecret that authors nothing": {"externalsecret", &components.ExternalSecretHandler{}, map[string]any{},
			"map[secretStoreRef:map[] target:map[]]"},
		"a key read from the ExternalSecret's own store": {"externalsecret", &components.ExternalSecretHandler{},
			map[string]any{"data": []any{map[string]any{
				"secretKey": "username", "remoteRef": map[string]any{"key": "apps/web"}, "sourceRef": map[string]any{},
			}}},
			"map[data:[map[remoteRef:map[key:apps/web] secretKey:username sourceRef:map[storeRef:map[]]]] secretStoreRef:map[] target:map[]]"},
		"a template with no metadata": {"externalsecret", &components.ExternalSecretHandler{},
			map[string]any{"target": map[string]any{"template": map[string]any{"type": "Opaque"}}},
			"map[secretStoreRef:map[] target:map[template:map[metadata:map[] type:Opaque]]]"},
		"a ClusterExternalSecret with an empty spec": {"clusterexternalsecret", &components.ClusterExternalSecretHandler{},
			clusterExternalSecretOf(map[string]any{}),
			"map[externalSecretMetadata:map[] externalSecretSpec:map[secretStoreRef:map[] target:map[]]]"},
		"an AWS store": {"secretstore", &components.SecretStoreHandler{}, secretStoreMinimal(),
			"map[provider:map[aws:map[auth:map[] region:eu-west-1 service:SecretsManager]]]"},
		"a Vault store": {"clustersecretstore", &components.ClusterSecretStoreHandler{}, vaultStore(nil),
			"map[provider:map[vault:map[server:https://vault.example.com tls:map[] version:v2]]]"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := spec(tc.component, tc.handler, tc.props); got != tc.want {
				t.Errorf("spec = %s\nwant   %s", got, tc.want)
			}
		})
	}
}

// TestExternalSecretsKinds_AuthoredValuesArriveTyped reads a few authored
// values back from the typed objects: an authored 0 is kept where the type can
// carry one, an interval written as a number stays a number, and lists keep
// their order.
func TestExternalSecretsKinds_AuthoredValuesArriveTyped(t *testing.T) {
	store := generateCoreKindUnder(t, &components.SecretStoreHandler{}, "secretstore", "vault", secretStoreFull(),
		esPolicy{stubPolicy: ptStrictPolicy()}, nil).(*esv1.SecretStore)
	if retries := store.Spec.RetrySettings; retries == nil || retries.MaxRetries == nil || *retries.MaxRetries != 0 {
		t.Errorf("retrySettings = %+v, want the authored maxRetries 0", retries)
	}
	if interval := store.Spec.RefreshInterval; interval == nil || interval.IntValue() != 300 || interval.StrVal != "" {
		t.Errorf("refreshInterval = %+v, want the authored number 300", interval)
	}
	if vault := store.Spec.Provider.Vault; vault == nil || vault.Version != esv1.VaultKVStoreV2 || vault.Auth.Kubernetes.Path != "kubernetes" {
		t.Errorf("provider.vault = %+v, want the authored version and mount path", vault)
	}
	if got := store.Spec.Conditions[0].Namespaces; len(got) != 2 || got[0] != "shop" || got[1] != "billing" {
		t.Errorf("conditions[0].namespaces = %v, want them in authored order", got)
	}

	secret := generateCoreKind(t, &components.ExternalSecretHandler{}, "externalsecret", "web", externalSecretFull()).(*esv1.ExternalSecret)
	if interval := secret.Spec.RefreshInterval; interval == nil || interval.Duration != 0 {
		t.Errorf("refreshInterval = %v, want the authored 0s", interval)
	}
	if !secret.Spec.Target.Immutable || secret.Spec.Target.CreationPolicy != esv1.CreatePolicyMerge {
		t.Errorf("target = %+v, want it immutable with the authored creation policy", secret.Spec.Target)
	}
	if got := secret.Spec.Data; len(got) != 2 || got[0].SecretKey != "username" || got[1].SecretKey != "password" || got[1].SourceRef.SecretStoreRef.Name != "other" {
		t.Errorf("data = %+v, want the keys in authored order and the second read from its own store", got)
	}
	if got := secret.Spec.DataFrom; len(got) != 3 || got[2].SourceRef.GeneratorRef == nil || got[2].SourceRef.SecretStoreRef != nil {
		t.Errorf("dataFrom = %+v, want the third source a generator alone", got)
	}

	cluster := generateCoreKind(t, &components.ClusterExternalSecretHandler{}, "clusterexternalsecret", "web", clusterExternalSecretFull()).(*esv1.ClusterExternalSecret)
	if cluster.Spec.RefreshInterval == nil || cluster.Spec.RefreshInterval.Duration.String() != "1m0s" {
		t.Errorf("refreshTime = %v, want the authored 1m", cluster.Spec.RefreshInterval)
	}
	if got := cluster.Spec.NamespaceSelectors; len(got) != 2 || got[0].MatchLabels["tenant"] != "a" || len(got[1].MatchExpressions) != 1 {
		t.Errorf("namespaceSelectors = %+v, want both in authored order", got)
	}
}
