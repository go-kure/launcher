package kurel

import (
	"maps"
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// Under TransformContext.FluxNamespace a Flux object moves to that namespace,
// and the ConfigMaps and Secrets it reads by name from its own namespace must
// land there with it; a trait's object it does not read stays with the
// workloads in the application namespace (go-kure/launcher#740). kurel never
// sets a Flux namespace, so these tests drive the built-in transformer
// directly.

const fluxNSTarget = "custom-flux"

// fluxNSComponent is the registry fixture for typ, with extra properties set
// over it and traits attached.
func fluxNSComponent(t *testing.T, name, typ string, extra map[string]any, traits ...oam.Trait) oam.Component {
	t.Helper()
	fx, ok := componentLabelFixtures[typ]
	if !ok || fx.props == nil {
		t.Fatalf("no static fixture for component type %q", typ)
	}
	props := maps.Clone(fx.props)
	maps.Copy(props, extra)
	return oam.Component{Name: name, Type: typ, Properties: props, Traits: traits}
}

// fluxNSObjects builds comps under the Flux namespace and returns the
// namespace of every generated object, keyed "Kind/name".
func fluxNSObjects(t *testing.T, comps ...oam.Component) map[string]string {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	capabilities := map[string]oam.CapabilityBinding{
		"certificate": {Rendering: map[string]any{"issuerRef": map[string]any{"name": "letsencrypt-prod", "kind": "ClusterIssuer"}}},
	}
	cluster, err := newBuiltinTransformer().Transform(app, oam.TransformContext{
		FluxNamespace: fluxNSTarget, Domain: kurelDomain, Capabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	got := map[string]string{}
	var walk func(n *stack.Node)
	walk = func(n *stack.Node) {
		if n == nil {
			return
		}
		if n.Bundle != nil {
			for _, a := range n.Bundle.Applications {
				objs, err := a.Config.Generate(a)
				if err != nil {
					t.Fatalf("Generate %s: %v", a.Name, err)
				}
				for _, o := range objs {
					got[(*o).GetObjectKind().GroupVersionKind().Kind+"/"+(*o).GetName()] = (*o).GetNamespace()
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(cluster.Node)
	return got
}

func configMapTrait(name string) oam.Trait {
	return oam.Trait{Type: "configmap", Properties: map[string]any{"name": name, "data": map[string]any{"K": "v"}}}
}

func externalSecretTrait(secretName string) oam.Trait {
	return oam.Trait{Type: "external-secret", Properties: map[string]any{
		"secretName": secretName, "provider": "vault",
		"data": []any{map[string]any{"secretKey": "PASSWORD", "remoteRef": map[string]any{"key": "prod/app", "property": "password"}}},
	}}
}

func certificateTrait(secretName string) oam.Trait {
	return oam.Trait{Type: "certificate", Properties: map[string]any{"secretName": secretName, "dnsNames": []any{"app.example.com"}}}
}

func secretRef(name string) map[string]any { return map[string]any{"name": name} }

func hrChart(sourceRef map[string]any, verify map[string]any) map[string]any {
	spec := map[string]any{"chart": "app", "sourceRef": sourceRef}
	if verify != nil {
		spec["verify"] = verify
	}
	return map[string]any{"spec": spec}
}

// TestFluxNamespace_ReadInputsFollow: for every same-namespace ConfigMap or
// Secret reference of every moving Flux kind, the trait object it names moves
// with it, and the Flux object itself moves.
func TestFluxNamespace_ReadInputsFollow(t *testing.T) {
	es := externalSecretTrait("creds")
	cases := []struct {
		name   string
		typ    string
		extra  map[string]any
		trait  oam.Trait
		reader string // the Flux object, "Kind/name"
		input  string // the trait's object, "Kind/name"
	}{
		{"helmrelease valuesFrom ConfigMap", "helmrelease",
			map[string]any{"valuesFrom": []any{map[string]any{"kind": "ConfigMap", "name": "vals"}}},
			configMapTrait("vals"), "HelmRelease/c", "ConfigMap/vals"},
		{"helmrelease valuesFrom Secret", "helmrelease",
			map[string]any{"valuesFrom": []any{map[string]any{"kind": "Secret", "name": "creds"}}},
			es, "HelmRelease/c", "ExternalSecret/creds"},
		{"helmrelease kubeConfig secretRef", "helmrelease",
			map[string]any{"kubeConfig": map[string]any{"secretRef": secretRef("creds")}},
			es, "HelmRelease/c", "ExternalSecret/creds"},
		{"helmrelease kubeConfig configMapRef", "helmrelease",
			map[string]any{"kubeConfig": map[string]any{"configMapRef": secretRef("kc")}},
			configMapTrait("kc"), "HelmRelease/c", "ConfigMap/kc"},
		{"helmrelease chart verify secretRef", "helmrelease",
			map[string]any{"chart": hrChart(map[string]any{"kind": "HelmRepository", "name": "example"},
				map[string]any{"provider": "cosign", "secretRef": secretRef("creds")})},
			es, "HelmRelease/c", "ExternalSecret/creds"},
		{"helmrepository secretRef", "helmrepository", map[string]any{"secretRef": secretRef("creds")},
			es, "HelmRepository/c", "ExternalSecret/creds"},
		{"helmrepository certSecretRef", "helmrepository", map[string]any{"certSecretRef": secretRef("creds")},
			es, "HelmRepository/c", "ExternalSecret/creds"},
		// cert-manager writes the Secret beside its Certificate.
		{"helmrepository certSecretRef from a certificate", "helmrepository", map[string]any{"certSecretRef": secretRef("repo-tls")},
			certificateTrait("repo-tls"), "HelmRepository/c", "Certificate/repo-tls"},
		{"ocirepository secretRef", "ocirepository", map[string]any{"secretRef": secretRef("creds")},
			es, "OCIRepository/c", "ExternalSecret/creds"},
		{"ocirepository certSecretRef", "ocirepository", map[string]any{"certSecretRef": secretRef("creds")},
			es, "OCIRepository/c", "ExternalSecret/creds"},
		{"ocirepository certSecretRef from a certificate", "ocirepository", map[string]any{"certSecretRef": secretRef("repo-tls")},
			certificateTrait("repo-tls"), "OCIRepository/c", "Certificate/repo-tls"},
		{"ocirepository proxySecretRef", "ocirepository", map[string]any{"proxySecretRef": secretRef("creds")},
			es, "OCIRepository/c", "ExternalSecret/creds"},
		{"ocirepository verify secretRef", "ocirepository",
			map[string]any{"verify": map[string]any{"provider": "cosign", "secretRef": secretRef("creds")}},
			es, "OCIRepository/c", "ExternalSecret/creds"},
		{"ocirepository verify trustedRootSecretRef", "ocirepository",
			map[string]any{"verify": map[string]any{"provider": "cosign", "trustedRootSecretRef": secretRef("creds")}},
			es, "OCIRepository/c", "ExternalSecret/creds"},
		{"gitrepository secretRef", "gitrepository", map[string]any{"secretRef": secretRef("creds")},
			es, "GitRepository/c", "ExternalSecret/creds"},
		{"gitrepository proxySecretRef", "gitrepository", map[string]any{"proxySecretRef": secretRef("creds")},
			es, "GitRepository/c", "ExternalSecret/creds"},
		{"gitrepository verify secretRef", "gitrepository",
			map[string]any{"verify": map[string]any{"mode": "HEAD", "secretRef": secretRef("creds")}},
			es, "GitRepository/c", "ExternalSecret/creds"},
		{"bucket secretRef", "bucket", map[string]any{"secretRef": secretRef("creds")},
			es, "Bucket/c", "ExternalSecret/creds"},
		{"bucket certSecretRef", "bucket", map[string]any{"certSecretRef": secretRef("creds")},
			es, "Bucket/c", "ExternalSecret/creds"},
		{"bucket proxySecretRef", "bucket", map[string]any{"proxySecretRef": secretRef("creds")},
			es, "Bucket/c", "ExternalSecret/creds"},
		{"bucket sts secretRef", "bucket",
			map[string]any{"provider": "generic", "sts": map[string]any{"provider": "ldap", "endpoint": "https://sts.example.com", "secretRef": secretRef("creds")}},
			es, "Bucket/c", "ExternalSecret/creds"},
		{"bucket sts certSecretRef", "bucket",
			map[string]any{"provider": "generic", "sts": map[string]any{"provider": "ldap", "endpoint": "https://sts.example.com", "certSecretRef": secretRef("creds")}},
			es, "Bucket/c", "ExternalSecret/creds"},
		{"helmchart verify secretRef", "helmchart",
			map[string]any{"sourceRef": map[string]any{"kind": "HelmRepository", "name": "podinfo"},
				"verify": map[string]any{"provider": "cosign", "secretRef": secretRef("creds")}},
			es, "HelmChart/c", "ExternalSecret/creds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fluxNSObjects(t, fluxNSComponent(t, "c", tc.typ, tc.extra, tc.trait))
			for _, key := range []string{tc.reader, tc.input} {
				if ns, ok := got[key]; !ok {
					t.Errorf("no %s emitted; got %v", key, got)
				} else if ns != fluxNSTarget {
					t.Errorf("%s namespace = %q, want %q", key, ns, fluxNSTarget)
				}
			}
		})
	}
}

// TestFluxNamespace_UnreadTraitObjectsStay: on a moving helmrelease, every trait
// object the HelmRelease does not read stays in the application namespace with
// the release's workloads (targetNamespace), whatever its kind: a ConfigMap or
// Secret it does not name, a Certificate, a claim, a NetworkPolicy, a route.
func TestFluxNamespace_UnreadTraitObjectsStay(t *testing.T) {
	traits := []oam.Trait{
		configMapTrait("read"),
		configMapTrait("unread"),
		externalSecretTrait("unread-creds"),
	}
	for _, typ := range []string{"certificate", "pvc", "networkpolicy", "cilium-networkpolicy", "ingress", "httproute"} {
		traits = append(traits, oam.Trait{Type: typ, Properties: traitLabelFixtures[typ].props})
	}
	hr := fluxNSComponent(t, "c", "helmrelease", map[string]any{
		"valuesFrom": []any{map[string]any{"kind": "ConfigMap", "name": "read"}}}, traits...)
	got := fluxNSObjects(t, hr)
	for key, ns := range got {
		want := "default"
		if key == "HelmRelease/c" || key == "ConfigMap/read" {
			want = fluxNSTarget
		}
		if ns != want {
			t.Errorf("%s namespace = %q, want %q", key, ns, want)
		}
	}
	// Every trait above emitted something, so the loop checked each of them.
	for _, key := range []string{"ConfigMap/unread", "ExternalSecret/unread-creds", "Certificate/app-tls", "PersistentVolumeClaim/shared-data",
		"NetworkPolicy/c-allow", "CiliumNetworkPolicy/app-allow", "Ingress/c-ingress", "HTTPRoute/c-httproute"} {
		if _, ok := got[key]; !ok {
			t.Errorf("no %s emitted; got %v", key, slices.Sorted(maps.Keys(got)))
		}
	}
}

// TestFluxNamespace_ChartVerifyFollowsHelmChartNamespace: a chart template whose
// sourceRef names a namespace has helm-controller create its HelmChart there, so
// the verification Secret is read from that namespace. It follows the release
// only when that namespace is the Flux namespace.
func TestFluxNamespace_ChartVerifyFollowsHelmChartNamespace(t *testing.T) {
	for _, tc := range []struct{ sourceNS, want string }{
		{"charts", "default"},
		{"default", "default"},
		{fluxNSTarget, fluxNSTarget},
	} {
		t.Run(tc.sourceNS, func(t *testing.T) {
			hr := fluxNSComponent(t, "c", "helmrelease",
				map[string]any{"chart": hrChart(map[string]any{"kind": "HelmRepository", "name": "example", "namespace": tc.sourceNS},
					map[string]any{"provider": "cosign", "secretRef": secretRef("creds")})},
				externalSecretTrait("creds"))
			got := fluxNSObjects(t, hr)
			if ns := got["ExternalSecret/creds"]; ns != tc.want {
				t.Errorf("ExternalSecret/creds namespace = %q, want %q", ns, tc.want)
			}
		})
	}
}

// TestFluxNamespace_NonMovingComponentInputsStay: a component that does not
// move keeps its trait objects in the application namespace, even when a moving
// component elsewhere reads an object of the same name.
func TestFluxNamespace_NonMovingComponentInputsStay(t *testing.T) {
	dep := fluxNSComponent(t, "web", "deployment", nil, configMapTrait("vals"))
	hr := fluxNSComponent(t, "c", "helmrelease",
		map[string]any{"valuesFrom": []any{map[string]any{"kind": "ConfigMap", "name": "vals"}}})
	got := fluxNSObjects(t, dep, hr)
	if ns := got["ConfigMap/vals"]; ns != "default" {
		t.Errorf("deployment's ConfigMap/vals namespace = %q, want %q", ns, "default")
	}
	if ns := got["HelmRelease/c"]; ns != fluxNSTarget {
		t.Errorf("HelmRelease/c namespace = %q, want %q", ns, fluxNSTarget)
	}
}

// TestFluxNamespace_ReadsSurviveDecoration: a decorating trait on the
// helmrelease (security-context wraps its config) still forwards what the
// release reads, so the read ConfigMap follows it.
func TestFluxNamespace_ReadsSurviveDecoration(t *testing.T) {
	hr := fluxNSComponent(t, "c", "helmrelease",
		map[string]any{"valuesFrom": []any{map[string]any{"kind": "ConfigMap", "name": "vals"}}},
		oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "baseline"}}, configMapTrait("vals"))
	got := fluxNSObjects(t, hr)
	if ns := got["ConfigMap/vals"]; ns != fluxNSTarget {
		t.Errorf("ConfigMap/vals namespace = %q, want %q", ns, fluxNSTarget)
	}
}

// TestFluxNamespace_SettableConfigsReportReads: every registered component
// config that moves to the Flux namespace also reports what its Flux object
// reads there, so a new Flux kind cannot skip the contract silently. The known
// moving kinds must be seen, so the walk is not vacuous.
func TestFluxNamespace_SettableConfigsReportReads(t *testing.T) {
	type settable interface{ SetFluxNamespace(string) }
	type reader interface {
		FluxNamespaceReads() (configMaps, secrets []string)
	}
	var moving []string
	for _, typ := range slices.Sorted(maps.Keys(builtinComponentHandlers())) {
		fx := componentLabelFixtures[typ]
		props := fx.props
		if fx.propsFor != nil {
			props = fx.propsFor(t)
		}
		cfg, err := builtinComponentHandlers()[typ].ToApplicationConfig(&oam.Component{Name: "c", Type: typ, Properties: props}, "default")
		if err != nil {
			t.Errorf("%s: ToApplicationConfig: %v", typ, err)
			continue
		}
		if _, ok := cfg.(settable); !ok {
			continue
		}
		moving = append(moving, typ)
		if _, ok := cfg.(reader); !ok {
			t.Errorf("%s: %T moves to the Flux namespace but does not report FluxNamespaceReads", typ, cfg)
		}
	}
	want := []string{"bucket", "gitrepository", "helmchart", "helmrelease", "helmrepository", "oci", "ocirepository"}
	for _, typ := range want {
		if !slices.Contains(moving, typ) {
			t.Errorf("%s: config does not move to the Flux namespace; moving types: %v", typ, moving)
		}
	}
}
