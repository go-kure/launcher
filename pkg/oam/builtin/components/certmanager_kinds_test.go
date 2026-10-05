package components_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind components of cert-manager's API (go-kure/launcher#790): issuer,
// clusterissuer and certificate. What they share with the kinds the policy
// does not reach is held by the policyFreeKinds table
// (kind_policy_free_test.go), in which they are the held ones; this file holds
// their fixtures and what the environment policy asks of them.

// secretKey is a reference to one key of a Secret.
func secretKey(name, key string) map[string]any {
	return map[string]any{"name": name, "key": key}
}

// solverPodTemplate is the pod template of an ACME HTTP01 solver, with a value
// of every field of its spec and the given resources.
func solverPodTemplate(resources map[string]any) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"labels":      map[string]any{"acme": "solver"},
			"annotations": map[string]any{"example.com/purpose": "challenge"},
		},
		"spec": map[string]any{
			"nodeSelector":       map[string]any{"kubernetes.io/os": "linux"},
			"affinity":           map[string]any{"nodeAffinity": map[string]any{}},
			"tolerations":        []any{map[string]any{"key": "edge", "operator": "Exists"}},
			"priorityClassName":  "challenge",
			"serviceAccountName": "acme-solver",
			"imagePullSecrets":   []any{map[string]any{"name": "registry"}},
			"securityContext":    map[string]any{"runAsNonRoot": true, "runAsUser": 0, "fsGroup": 65534},
			"resources":          resources,
		},
	}
}

// issuerFull is a value of every top-level field of an Issuer's spec, and
// under it of what the policy and the required list read. cert-manager lets an
// issuer be of one type; the kind leaves that to it, and the fixture sets all
// five. The solver pods stay inside ptStrictPolicy, one of them at its maxima.
func issuerFull() map[string]any {
	return map[string]any{
		"acme": map[string]any{
			"server":         "https://acme-v02.api.letsencrypt.org/directory",
			"email":          "ops@example.com",
			"preferredChain": "ISRG Root X1",
			"profile":        "tlsserver",
			// A bundle is bytes, authored in base64.
			"caBundle":      "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t",
			"skipTLSVerify": false,
			"externalAccountBinding": map[string]any{
				"keyID": "kid-1", "keySecretRef": secretKey("acme-eab", "hmac"), "keyAlgorithm": "HS256",
			},
			"privateKeySecretRef":         map[string]any{"name": "letsencrypt-account"},
			"disableAccountKeyGeneration": false,
			"enableDurationFeature":       true,
			"solvers": []any{
				map[string]any{
					"selector": map[string]any{
						"matchLabels": map[string]any{"use": "http01"},
						"dnsNames":    []any{"shop.example.com"},
						"dnsZones":    []any{"example.com"},
					},
					"http01": map[string]any{"ingress": map[string]any{
						"ingressClassName": "nginx", "serviceType": "ClusterIP",
						"podTemplate": solverPodTemplate(map[string]any{
							"limits":   map[string]any{"cpu": "2", "memory": "1Gi"},
							"requests": map[string]any{"cpu": "10m", "memory": 67108864},
						}),
						"ingressTemplate": map[string]any{"metadata": map[string]any{
							"labels":      map[string]any{"acme": "solver"},
							"annotations": map[string]any{"nginx.ingress.kubernetes.io/whitelist-source-range": "0.0.0.0/0"},
						}},
					}},
				},
				map[string]any{"http01": map[string]any{"gatewayHTTPRoute": map[string]any{
					"serviceType": "ClusterIP",
					"labels":      map[string]any{"acme": "solver"},
					"parentRefs":  []any{map[string]any{"name": "public", "namespace": "gateways", "sectionName": "http"}},
					"podTemplate": solverPodTemplate(map[string]any{"limits": map[string]any{"cpu": "100m", "memory": "64Mi"}}),
				}}},
				map[string]any{
					"dns01": map[string]any{
						"cnameStrategy": "Follow",
						"cloudflare":    map[string]any{"email": "ops@example.com", "apiTokenSecretRef": secretKey("cloudflare", "token")},
					},
					"waitInsteadOfSelfCheck": "2m",
				},
				// A webhook solver's configuration is free JSON.
				map[string]any{"dns01": map[string]any{"webhook": map[string]any{
					"groupName": "acme.example.com", "solverName": "internal",
					"config": map[string]any{"zone": "example.com", "ttl": 120},
				}}},
			},
		},
		"ca": map[string]any{
			"secretName":             "ca-key-pair",
			"crlDistributionPoints":  []any{"http://crl.example.com/ca.crl"},
			"ocspServers":            []any{"http://ocsp.example.com"},
			"issuingCertificateURLs": []any{"http://ca.example.com/ca.crt"},
		},
		"vault": map[string]any{
			"server": "https://vault.example.com:8200", "serverName": "vault.example.com",
			"path": "pki_int/sign/example-dot-com", "namespace": "platform",
			"caBundleSecretRef": secretKey("vault-ca", "ca.crt"),
			"auth": map[string]any{"kubernetes": map[string]any{
				"role": "issuer", "mountPath": "/v1/auth/kubernetes",
				"serviceAccountRef": map[string]any{"name": "vault-issuer", "audiences": []any{"vault"}},
			}},
		},
		"selfSigned": map[string]any{"crlDistributionPoints": []any{"http://crl.example.com/self.crl"}},
		"venafi": map[string]any{
			"zone": `DevOps\cert-manager`,
			"tpp": map[string]any{
				"url": "https://tpp.example.com/vedsdk", "credentialsRef": map[string]any{"name": "tpp-credentials"},
				"caBundleSecretRef": secretKey("tpp-ca", "ca.crt"),
			},
		},
	}
}

// certificateMinimal is the least a certificate may author: the two fields
// the API requires.
func certificateMinimal() map[string]any {
	return map[string]any{"secretName": "web-tls", "issuerRef": map[string]any{"name": "ca"}}
}

// certificateFull is a value of every top-level field of a Certificate's
// spec. cert-manager lets a subject be written one way and a renewal moment
// one way; the kind leaves both to it, and the fixture sets every field. The
// keystores take their passwords from a Secret, which every policy allows.
func certificateFull() map[string]any {
	return map[string]any{
		"subject": map[string]any{
			"organizations": []any{"Example Corp"}, "countries": []any{"BE"},
			"organizationalUnits": []any{"Platform"}, "serialNumber": "42",
		},
		"literalSubject":        "CN=shop.example.com,O=Example Corp,C=BE",
		"commonName":            "shop.example.com",
		"duration":              "2160h",
		"renewBefore":           "360h",
		"renewBeforePercentage": 20,
		"renewal": map[string]any{
			"policy":  "RenewBefore",
			"windows": []any{map[string]any{"cron": "0 2 * * 6", "windowDuration": "4h", "timezone": "Europe/Brussels"}},
		},
		"dnsNames":       []any{"shop.example.com", "www.shop.example.com"},
		"ipAddresses":    []any{"192.0.2.10"},
		"uris":           []any{"spiffe://cluster.local/ns/shop/sa/web"},
		"otherNames":     []any{map[string]any{"oid": "1.3.6.1.4.1.311.20.2.3", "utf8Value": "web@example.com"}},
		"emailAddresses": []any{"ops@example.com"},
		"secretName":     "shop-tls",
		"secretTemplate": map[string]any{
			"labels":      map[string]any{"team": "shop"},
			"annotations": map[string]any{"example.com/replicate": "true"},
		},
		"keystores": map[string]any{
			"jks":    map[string]any{"create": false, "alias": "shop", "passwordSecretRef": secretKey("keystore", "jks")},
			"pkcs12": map[string]any{"create": true, "profile": "Modern2023", "passwordSecretRef": secretKey("keystore", "pkcs12")},
		},
		"issuerRef":               map[string]any{"name": "letsencrypt", "kind": "ClusterIssuer", "group": "cert-manager.io"},
		"isCA":                    true,
		"usages":                  []any{"server auth", "client auth"},
		"privateKey":              map[string]any{"algorithm": "ECDSA", "size": 256, "encoding": "PKCS8", "rotationPolicy": "Always"},
		"signatureAlgorithm":      "ECDSAWithSHA256",
		"encodeUsagesInRequest":   false,
		"revisionHistoryLimit":    3,
		"additionalOutputFormats": []any{map[string]any{"type": "DER"}, map[string]any{"type": "CombinedPEM"}},
		"nameConstraints": map[string]any{
			"critical":  true,
			"permitted": map[string]any{"dnsDomains": []any{"example.com"}, "ipRanges": []any{"192.0.2.0/24"}},
			"excluded":  map[string]any{"emailAddresses": []any{".example.org"}, "uriDomains": []any{"example.net"}},
		},
	}
}

// certificateWith is certificateMinimal with one more property.
func certificateWith(name string, value any) map[string]any {
	return withProperty(certificateMinimal(), name, value)
}

// acmeIssuer is the properties of an ACME issuer with the two fields the API
// requires of one and the solvers.
func acmeIssuer(solvers ...any) map[string]any {
	return map[string]any{"acme": acmeWith("solvers", append([]any{}, solvers...))}
}

// acmeWith is an ACME issuer type with the two fields the API requires of one
// and one more.
func acmeWith(name string, value any) map[string]any {
	return map[string]any{
		"server":              "https://acme.example.com/directory",
		"privateKeySecretRef": map[string]any{"name": "acme-account"},
		name:                  value,
	}
}

// http01Solver is an ACME HTTP01 solver that answers the given way ("ingress"
// or "gatewayHTTPRoute") from a pod with the resources.
func http01Solver(way string, resources map[string]any) map[string]any {
	return map[string]any{"http01": map[string]any{way: map[string]any{
		"podTemplate": map[string]any{"spec": map[string]any{"resources": resources}},
	}}}
}

// certManagerIssuers are the two kinds an Issuer's spec builds.
var certManagerIssuers = []struct {
	component string
	handler   oam.ComponentHandler
}{
	{"issuer", &components.IssuerHandler{}},
	{"clusterissuer", &components.ClusterIssuerHandler{}},
}

// TestIssuerKinds_SolverPodResources: the cpu and memory an ACME HTTP01
// solver's pod template asks for, as a limit or as a request, are held to the
// policy's maxima, on both ways a solver answers and on every solver, and the
// violation names the component and the template. Nothing else of an issuer is
// held: a request over its own limit passes (cert-manager lays the block over
// its controller's defaults), and so does an issuer of any other type.
func TestIssuerKinds_SolverPodResources(t *testing.T) {
	limits := func(name, value string) map[string]any {
		return map[string]any{"limits": map[string]any{name: value}}
	}
	requests := func(name, value string) map[string]any {
		return map[string]any{"requests": map[string]any{name: value}}
	}
	inside := limits("cpu", "100m")
	for _, kind := range certManagerIssuers {
		for name, tc := range map[string]struct {
			props  map[string]any
			policy oam.Policy
			want   string // "" when the component builds
		}{
			"ingress, cpu limit": {acmeIssuer(http01Solver("ingress", limits("cpu", "4"))), ptStrictPolicy(),
				`acme.solvers[0].http01.ingress.podTemplate.spec.resources: cpu limit "4" exceeds enforced maximum "2"`},
			"ingress, cpu request": {acmeIssuer(http01Solver("ingress", requests("cpu", "3"))), ptStrictPolicy(),
				`acme.solvers[0].http01.ingress.podTemplate.spec.resources: cpu request "3" exceeds enforced maximum "2"`},
			"ingress, memory limit": {acmeIssuer(http01Solver("ingress", limits("memory", "2Gi"))), ptStrictPolicy(),
				`acme.solvers[0].http01.ingress.podTemplate.spec.resources: memory limit "2Gi" exceeds enforced maximum "1Gi"`},
			"gateway route, memory request": {acmeIssuer(http01Solver("gatewayHTTPRoute", requests("memory", "2Gi"))), ptStrictPolicy(),
				`acme.solvers[0].http01.gatewayHTTPRoute.podTemplate.spec.resources: memory request "2Gi" exceeds enforced maximum "1Gi"`},
			"gateway route, cpu limit": {acmeIssuer(http01Solver("gatewayHTTPRoute", limits("cpu", "2500m"))), ptStrictPolicy(),
				`acme.solvers[0].http01.gatewayHTTPRoute.podTemplate.spec.resources: cpu limit "2500m" exceeds enforced maximum "2"`},
			"a later solver": {acmeIssuer(
				http01Solver("ingress", inside),
				map[string]any{"dns01": map[string]any{"cloudflare": map[string]any{"apiTokenSecretRef": secretKey("cloudflare", "token")}}},
				http01Solver("gatewayHTTPRoute", limits("memory", "2Gi")),
			), ptStrictPolicy(),
				`acme.solvers[2].http01.gatewayHTTPRoute.podTemplate.spec.resources: memory limit "2Gi" exceeds enforced maximum "1Gi"`},
			"at the maxima": {acmeIssuer(http01Solver("ingress", map[string]any{
				"limits": map[string]any{"cpu": "2", "memory": "1Gi"}, "requests": map[string]any{"cpu": "2", "memory": "1Gi"},
			})), ptStrictPolicy(), ""},
			"a request over its limit, both inside": {acmeIssuer(http01Solver("ingress", map[string]any{
				"limits": map[string]any{"cpu": "100m"}, "requests": map[string]any{"cpu": "1"},
			})), ptStrictPolicy(), ""},
			"a solver with no pod template": {acmeIssuer(map[string]any{"http01": map[string]any{"ingress": map[string]any{"ingressClassName": "nginx"}}}), ptStrictPolicy(), ""},
			"a pod template with no resources": {acmeIssuer(map[string]any{"http01": map[string]any{"ingress": map[string]any{
				"podTemplate": map[string]any{"spec": map[string]any{"priorityClassName": "challenge"}},
			}}}), ptStrictPolicy(), ""},
			"an issuer of another type":   {map[string]any{"selfSigned": map[string]any{}}, ptStrictPolicy(), ""},
			"over it with no maximum set": {acmeIssuer(http01Solver("ingress", limits("cpu", "64"))), &stubPolicy{}, ""},
			"over it under a policy that forbids explicit secrets and sets no maximum": {
				acmeIssuer(http01Solver("ingress", limits("cpu", "64"))), esForbidding(), ""},
			"over it with no policy given": {acmeIssuer(http01Solver("ingress", limits("cpu", "64"))), nil, ""},
		} {
			t.Run(kind.component+"/"+name, func(t *testing.T) {
				objs, err := pvTransform(kind.component, kind.handler, tc.props, tc.policy)
				if tc.want != "" {
					htWantViolation(t, err, `component "web": `+tc.want)
					return
				}
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if len(objs) != 1 || objs[0].GetName() != "web" {
					t.Fatalf("generated %v, want the one object web", objs)
				}
			})
		}
	}
}

// TestIssuerKinds_PolicyFillsNoDefault: the policy's default requests and
// limits are a workload's. A solver pod template takes none of them: the
// object built under a policy that sets them is the one built under none.
func TestIssuerKinds_PolicyFillsNoDefault(t *testing.T) {
	defaults := &stubPolicy{
		maxCPU: "2", maxMemory: "1Gi",
		defaultCPURequest: "50m", defaultMemoryRequest: "32Mi", defaultCPULimit: "1", defaultMemoryLimit: "512Mi",
	}
	for _, kind := range certManagerIssuers {
		t.Run(kind.component, func(t *testing.T) {
			build := func(policy oam.Policy) map[string]any {
				props := acmeIssuer(
					http01Solver("ingress", map[string]any{"limits": map[string]any{"cpu": "100m"}}),
					map[string]any{"http01": map[string]any{"gatewayHTTPRoute": map[string]any{"podTemplate": map[string]any{}}}},
				)
				objs, err := pvTransform(kind.component, kind.handler, props, policy)
				if err != nil || len(objs) != 1 {
					t.Fatalf("transform: %d objects, err %v", len(objs), err)
				}
				return policyFreeJSON(t, objs[0])
			}
			if got, want := build(defaults), build(nil); !reflect.DeepEqual(got, want) {
				t.Errorf("under a policy with defaults the object is %v\nwant the one built under none: %v", got, want)
			}
		})
	}
}

// TestCertificateKind_KeystorePassword: a keystore password written into the
// Certificate is refused under a policy that forbids explicit secrets, for
// either keystore, and the refusal names the field and its replacement without
// the value. A policy that allows explicit secrets, one that does not answer
// the question and none passed build it, and a password taken from a Secret is
// built under every one of them.
func TestCertificateKind_KeystorePassword(t *testing.T) {
	refusal := func(at string) string {
		return fmt.Sprintf("%s.password: holds the keystore password in the object, and the environment policy forbids explicit secrets; "+
			"name the key of a Secret created out of band in %s.passwordSecretRef instead", at, at)
	}
	literal := map[string]any{"create": true, "password": esSentinel}
	fromSecret := map[string]any{"create": true, "passwordSecretRef": secretKey("keystore", "password")}
	policies := map[string]oam.Policy{
		"forbidding":       esForbidding(),
		"allowing":         esPolicy{stubPolicy: &stubPolicy{}, allow: true},
		"no answer":        &stubPolicy{},
		"no policy passed": nil,
	}
	for name, tc := range map[string]struct {
		keystores map[string]any
		want      string // under the forbidding policy; "" when the component builds
	}{
		"jks password":             {map[string]any{"jks": literal}, refusal("keystores.jks")},
		"pkcs12 password":          {map[string]any{"pkcs12": literal}, refusal("keystores.pkcs12")},
		"both, the first reported": {map[string]any{"jks": literal, "pkcs12": literal}, refusal("keystores.jks")},
		"a password beside a reference": {map[string]any{"pkcs12": map[string]any{
			"create": true, "password": esSentinel, "passwordSecretRef": secretKey("keystore", "password"),
		}}, refusal("keystores.pkcs12")},
		"one from a Secret, one literal": {map[string]any{"jks": fromSecret, "pkcs12": literal}, refusal("keystores.pkcs12")},
		"an empty password":              {map[string]any{"jks": map[string]any{"create": true, "password": ""}}, refusal("keystores.jks")},
		"passwords from a Secret":        {map[string]any{"jks": fromSecret, "pkcs12": fromSecret}, ""},
		"no keystore":                    {nil, ""},
	} {
		for policyName, policy := range policies {
			t.Run(name+"/"+policyName, func(t *testing.T) {
				props := certificateMinimal()
				if tc.keystores != nil {
					props["keystores"] = tc.keystores
				}
				objs, err := pvTransform("certificate", &components.CertificateHandler{}, props, policy)
				if tc.want != "" && policyName == "forbidding" {
					htWantViolation(t, err, `component "web": `+tc.want)
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
				if _, ok := objs[0].(*certv1.Certificate); !ok {
					t.Fatalf("generated a %T, want the Certificate", objs[0])
				}
			})
		}
	}
}

// TestCertManagerKinds_WrittenUnauthored: what the API's types write into the
// object that the author did not: an empty reference where the type holds one
// by value. The API accepts each, and cert-manager reads an empty name as no
// reference. The kind's README entry states them.
func TestCertManagerKinds_WrittenUnauthored(t *testing.T) {
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
		"a keystore with its password in the object": {"certificate", &components.CertificateHandler{},
			certificateWith("keystores", map[string]any{"jks": map[string]any{"create": true, "password": "changeit"}}),
			"map[issuerRef:map[name:ca] keystores:map[jks:map[create:true password:changeit passwordSecretRef:map[name:]]] secretName:web-tls]"},
		"a Vault issuer that authenticates with a service account": {"issuer", &components.IssuerHandler{},
			map[string]any{"vault": map[string]any{"server": "https://vault.example.com", "path": "pki/sign/web", "auth": map[string]any{
				"kubernetes": map[string]any{"role": "issuer", "serviceAccountRef": map[string]any{"name": "vault-issuer"}},
			}}},
			"map[vault:map[auth:map[kubernetes:map[role:issuer secretRef:map[name:] serviceAccountRef:map[name:vault-issuer]]] path:pki/sign/web server:https://vault.example.com]]"},
		"an HTTP01 solver with an empty pod template": {"clusterissuer", &components.ClusterIssuerHandler{},
			acmeIssuer(map[string]any{"http01": map[string]any{"ingress": map[string]any{"podTemplate": map[string]any{}}}}),
			"map[acme:map[privateKeySecretRef:map[name:acme-account] server:https://acme.example.com/directory " +
				"solvers:[map[http01:map[ingress:map[podTemplate:map[metadata:map[] spec:map[]]]]]]]]"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := spec(tc.component, tc.handler, tc.props); got != tc.want {
				t.Errorf("spec = %s\nwant   %s", got, tc.want)
			}
		})
	}
}

// TestCertManagerKinds_AuthoredValuesArriveTyped reads a few authored values
// back from the typed objects: lists keep their order, a bundle its bytes, an
// authored false and 0 are kept where the type can carry them, and a duration
// is the authored one, in Go's spelling.
func TestCertManagerKinds_AuthoredValuesArriveTyped(t *testing.T) {
	build := func(component string, props map[string]any) any {
		for _, kind := range policyFreeKinds {
			if kind.component == component {
				return kind.generate(t, "fast", props)
			}
		}
		t.Fatalf("%s is no kind of policyFreeKinds", component)
		return nil
	}

	issuer := build("issuer", issuerFull()).(*certv1.Issuer)
	acme := issuer.Spec.ACME
	if acme == nil || len(acme.Solvers) != 4 || acme.Solvers[0].HTTP01 == nil || acme.Solvers[1].HTTP01 == nil || acme.Solvers[2].DNS01 == nil || acme.Solvers[3].DNS01 == nil {
		t.Fatalf("acme = %+v, want the four authored solvers in order", acme)
	}
	if string(acme.CABundle) != "-----BEGIN CERTIFICATE-----" {
		t.Errorf("caBundle = %q, want the bytes the authored base64 holds", acme.CABundle)
	}
	pod := acme.Solvers[0].HTTP01.Ingress.PodTemplate
	if got := pod.Spec.Resources.Requests.Memory().String(); got != "67108864" {
		t.Errorf("solver memory request = %s, want the authored number 67108864", got)
	}
	if got := pod.Spec.SecurityContext.RunAsUser; got == nil || *got != 0 {
		t.Errorf("solver runAsUser = %v, want the authored 0", got)
	}
	if got := acme.Solvers[2].WaitInsteadOfSelfCheck; got == nil || got.Duration.String() != "2m0s" {
		t.Errorf("waitInsteadOfSelfCheck = %v, want the authored 2m", got)
	}
	if got := string(acme.Solvers[3].DNS01.Webhook.Config.Raw); got != `{"ttl":120,"zone":"example.com"}` {
		t.Errorf("webhook config = %s, want the authored JSON", got)
	}
	// An issuer type authored empty is the issuer's type: it is in the object
	// as written, and the ones left out are not.
	selfSigned := policyFreeJSON(t, build("issuer", map[string]any{"selfSigned": map[string]any{}}))["spec"]
	if got, want := fmt.Sprint(selfSigned), "map[selfSigned:map[]]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}
	// The API's schema accepts an issuer of no type, and cert-manager's webhook
	// refuses it. The kind builds what was authored.
	if none := build("clusterissuer", map[string]any{}).(*certv1.ClusterIssuer); !reflect.DeepEqual(none.Spec, certv1.IssuerSpec{}) {
		t.Errorf("spec = %+v, want an empty one on an issuer that authors no type", none.Spec)
	}

	cert := build("certificate", certificateFull()).(*certv1.Certificate)
	if !slices.Equal(cert.Spec.DNSNames, []string{"shop.example.com", "www.shop.example.com"}) {
		t.Errorf("dnsNames = %v, want them in authored order", cert.Spec.DNSNames)
	}
	if !slices.Equal(cert.Spec.Usages, []certv1.KeyUsage{certv1.UsageServerAuth, certv1.UsageClientAuth}) {
		t.Errorf("usages = %v, want them in authored order", cert.Spec.Usages)
	}
	if got := cert.Spec.EncodeUsagesInRequest; got == nil || *got {
		t.Errorf("encodeUsagesInRequest = %v, want the authored false", got)
	}
	if ks := cert.Spec.Keystores; ks == nil || ks.JKS == nil || ks.JKS.Create || ks.PKCS12 == nil || !ks.PKCS12.Create {
		t.Errorf("keystores = %+v, want jks with the authored create false and pkcs12 with true", ks)
	}
	if got := cert.Spec.IssuerRef; got.Name != "letsencrypt" || got.Kind != "ClusterIssuer" || got.Group != "cert-manager.io" {
		t.Errorf("issuerRef = %+v, want the authored reference", got)
	}
	// A duration is carried in Go's spelling of the authored one.
	encoded := policyFreeJSON(t, cert)["spec"].(map[string]any)
	if encoded["duration"] != "2160h0m0s" || encoded["renewBefore"] != "360h0m0s" {
		t.Errorf("duration = %v, renewBefore = %v; want 2160h0m0s and 360h0m0s", encoded["duration"], encoded["renewBefore"])
	}
	// With the two required fields alone the object holds those and nothing else.
	least := policyFreeJSON(t, build("certificate", certificateMinimal()))["spec"]
	if got, want := fmt.Sprint(least), "map[issuerRef:map[name:ca] secretName:web-tls]"; got != want {
		t.Errorf("spec = %s, want %s", got, want)
	}
}
