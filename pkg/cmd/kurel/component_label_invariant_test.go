package kurel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// This file is the output-invariant guard for go-kure/launcher#572. A component
// name is a DNS-1123 subdomain (up to 253 characters) while a label value is at
// most 63, so the component-identity labels and selectors launcher generates —
// the `app` label, the `app` selectors that pick a component's pods, and the
// synthesized NetworkPolicies' `<domain>/component` selector — take their value
// from oam.ComponentLabelValue. Selectors an operator owns (a component type
// that selects the pods an operator creates, by the operator's own label and
// naming contract) are not component-identity labels and are out of scope here;
// the fixtures below do not exercise them. The test renders every registered
// component type and every registered trait through the real `kurel build` path
// and checks the emitted manifests, so a new emitter that writes the raw name
// into an `app` label — or a selector that stops agreeing with the labels it
// targets — fails here. The fixture tables below are checked against the
// registries (builtinComponentHandlers, builtinTraitHandlers,
// builtinTraitLoweringRules), so a type registered without a fixture fails too.

// labelInvariantProfile carries every capability a fixture below needs. The
// expose rendering also injects networkPolicy.trafficSources, so the routing
// traits exercise the synthesized ingress NetworkPolicy and its component
// selector.
const labelInvariantProfile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
        networkPolicy:
          trafficSources:
            - namespace: ingress-nginx
    certificate:
      rendering:
        issuerRef:
          name: letsencrypt-prod
          kind: ClusterIssuer
    external-secret:
      rendering:
        secretStoreRef:
          name: vault-cluster-store
          kind: ClusterSecretStore
`

// longLabelComponentName is a valid component name (a DNS-1123 subdomain) of
// 200 characters, dotted, far over the 63-character label-value limit.
func longLabelComponentName(t *testing.T) string {
	t.Helper()
	segment := "component-label-invariant-guard" // 31 characters
	var parts []string
	for len(strings.Join(parts, ".")) < 200 {
		parts = append(parts, segment)
	}
	name := strings.Join(parts, ".")[:199] + "z"
	if len(name) != 200 {
		t.Fatalf("long test name is %d characters, want 200", len(name))
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		t.Fatalf("long test name %q is not a valid component name: %v", name, errs)
	}
	return name
}

// boundaryLabelComponentName is a 63-character name that every component type
// accepts: a DNS-1035 label, so also a valid Service and container name. It is
// the longest name whose label value is the name itself.
func boundaryLabelComponentName(t *testing.T) string {
	t.Helper()
	name := "a" + strings.Repeat("b-", 30) + "cd"
	if len(name) != validation.LabelValueMaxLength {
		t.Fatalf("boundary test name is %d characters, want %d", len(name), validation.LabelValueMaxLength)
	}
	if errs := validation.IsDNS1035Label(name); len(errs) > 0 {
		t.Fatalf("boundary test name %q is not a DNS-1035 label: %v", name, errs)
	}
	return name
}

// componentLabelFixture renders one component type on its own.
type componentLabelFixture struct {
	props map[string]any
	// longRefusal is empty when the type accepts a name over 63 characters.
	// Otherwise it is a substring of the refusal the type already gives such a
	// name (go-kure/launcher#407 container name, go-kure/launcher#546 Service
	// name); the test pins that the refusal is unchanged.
	longRefusal string
	// labelled says the component's own output carries an `app` label, so the
	// invariant is checked on at least one value rather than vacuously — at
	// the boundary name, and at the long name when the type accepts it.
	labelled bool
	// selectors is the minimum number of pod selectors the boundary render must
	// match against its own pod template (0 for a type with no pods, or a Job
	// kind, which authors no spec.selector).
	selectors int
}

const (
	containerNameRefusal = "container name"
	serviceNameRefusal   = "is not a valid Service name"
)

func workloadProps(extra map[string]any) map[string]any {
	p := map[string]any{"image": "ghcr.io/example/app:v1.0.0"}
	maps.Copy(p, extra)
	return p
}

var componentLabelFixtures = map[string]componentLabelFixture{
	// replicas 3 with topologySpread and pod anti-affinity puts every scheduling
	// selector the workload kinds build into the output.
	"webservice": {props: workloadProps(map[string]any{"port": 8080, "replicas": 3, "topologySpread": true,
		"affinity": map[string]any{"enablePodAntiAffinity": true}}), longRefusal: serviceNameRefusal, labelled: true, selectors: 5},
	"worker": {props: workloadProps(map[string]any{"replicas": 3, "topologySpread": true,
		"affinity": map[string]any{"enablePodAntiAffinity": true}}), longRefusal: containerNameRefusal, labelled: true, selectors: 4},
	"deployment":  {props: workloadProps(map[string]any{"replicas": 3}), longRefusal: containerNameRefusal, labelled: true, selectors: 1},
	"cronjob":     {props: workloadProps(map[string]any{"schedule": "0 2 * * *"}), longRefusal: containerNameRefusal, labelled: true},
	"job":         {props: workloadProps(nil), longRefusal: containerNameRefusal, labelled: true},
	"daemonset":   {props: workloadProps(map[string]any{"port": 9090}), longRefusal: serviceNameRefusal, labelled: true, selectors: 2},
	"statefulset": {props: workloadProps(map[string]any{"port": 5432, "affinity": map[string]any{"enablePodAntiAffinity": true}}), longRefusal: serviceNameRefusal, labelled: true, selectors: 3},
	"service": {props: map[string]any{"ports": []any{map[string]any{"name": "http", "port": 80, "targetPort": 8080}}},
		longRefusal: serviceNameRefusal, labelled: true},
	"postgresql": {props: map[string]any{"version": "16", "storageSize": "10Gi"}},
	"helmchart": {props: map[string]any{"version": "v1.17.2",
		"source": map[string]any{"kind": "OCIRepository", "url": "oci://ghcr.io/example/charts/app"}}},
	// valuesMode configMap with non-empty values emits the values ConfigMap,
	// the one object of this type carrying an `app` label.
	"helmrelease": {props: map[string]any{
		"chart": map[string]any{"spec": map[string]any{"chart": "app",
			"sourceRef": map[string]any{"kind": "HelmRepository", "name": "example"}}},
		"valuesMode": "configMap", "values": map[string]any{"replicaCount": 2}}, labelled: true},
	"passthrough": {props: map[string]any{"object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "data": map[string]any{"k": "v"}}}},
	"crd": {props: map[string]any{"inline": `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
`}},
	"manifests": {props: map[string]any{"inline": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  k: v\n"}},
	"oci":       {props: map[string]any{"source": map[string]any{"url": "oci://registry.example.com/manifests/app"}, "version": "0.3.0"}},
}

// traitLabelFixture renders one trait on a host component.
type traitLabelFixture struct {
	props map[string]any // nil: the trait is authored with no properties
	// host is the component type (and props) the trait is attached to at the
	// 63-character boundary name.
	host      string
	hostProps map[string]any
	// longHost is true when the trait also renders on a component type that
	// accepts a name over 63 characters (a passthrough ConfigMap); false for a
	// trait that needs a workload host, which already refuses such a name.
	longHost bool
	// longLabelled says the trait itself emits an `app` label, so on the long
	// host the projected value must actually appear.
	longLabelled bool
	// selectors is the minimum number of pod selectors the boundary render must
	// match against the host's pod template; 0 means the host's own two (the
	// workload selector and its Service, or its topology spread).
	selectors int
}

var passthroughHostProps = map[string]any{"object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "data": map[string]any{"k": "v"}}}

func webserviceHost() (string, map[string]any) {
	return "webservice", workloadProps(map[string]any{"port": 8080})
}

var traitLabelFixtures = map[string]traitLabelFixture{
	"ingress": {props: map[string]any{"ingressClassName": "nginx", "rules": []any{map[string]any{
		"host": "app.example.com", "paths": []any{map[string]any{"path": "/", "backend": "other-svc", "port": 8080}}}}},
		longHost: true, longLabelled: true},
	"httproute": {props: map[string]any{"parentRefs": []any{map[string]any{"name": "gw"}}, "rules": []any{map[string]any{
		"backendRefs": []any{map[string]any{"name": "other-svc", "port": 8080}}}}},
		longHost: true, longLabelled: true},
	"expose":      {props: map[string]any{"rules": []any{map[string]any{"host": "app.example.com", "paths": []any{map[string]any{"path": "/"}}}}}},
	"certificate": {props: map[string]any{"secretName": "app-tls", "dnsNames": []any{"app.example.com"}}, longHost: true},
	"scaler":      {props: map[string]any{"minReplicas": 2, "maxReplicas": 5, "enablePDB": true}, selectors: 3},
	"pvc":         {props: map[string]any{"name": "shared-data", "size": "5Gi"}, longHost: true, longLabelled: true},
	"external-secret": {props: map[string]any{"secretName": "app-credentials", "data": []any{map[string]any{
		"secretKey": "PASSWORD", "remoteRef": map[string]any{"key": "prod/app", "property": "password"}}}},
		longHost: true, longLabelled: true},
	"configmap": {props: map[string]any{"name": "app-config", "data": map[string]any{"K": "v"}}, longHost: true, longLabelled: true},
	"networkpolicy": {props: map[string]any{"ingress": []any{map[string]any{
		"from":  []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"role": "frontend"}}}},
		"ports": []any{map[string]any{"port": 8080, "protocol": "TCP"}}}}},
		longHost: true, longLabelled: true, selectors: 3},
	"cilium-networkpolicy": {props: map[string]any{"name": "app-allow",
		"endpointSelector": map[string]any{"matchLabels": map[string]any{"role": "api"}},
		"ingress":          []any{map[string]any{"fromEndpoints": []any{map[string]any{"matchLabels": map[string]any{"role": "frontend"}}}}}},
		longHost: true},
	"volsync": {props: map[string]any{"sourcePVC": "data", "schedule": "@daily"},
		host: "statefulset", hostProps: workloadProps(map[string]any{"port": 5432,
			"volumeClaimTemplates": []any{map[string]any{"name": "data", "size": "10Gi", "mountPath": "/data"}}})},
	"rbac": {props: map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps"}, "verbs": []any{"get"}}},
		"clusterWide": true}, longHost: true, longLabelled: true},
	"fluxcd-patches": {props: map[string]any{"patches": []any{map[string]any{
		"patch":  "- op: add\n  path: /metadata/annotations/example.com~1patched\n  value: \"true\"\n",
		"target": map[string]any{"kind": "ConfigMap"}}}}, longHost: true},
	"fluxcd-postbuild": {props: map[string]any{"substitute": map[string]any{"CLUSTER": "local"}}, longHost: true},
	"prune-protection": {longHost: true},
	"force-replace":    {longHost: true},
	"security-context": {props: map[string]any{"psaLevel": "restricted"}},
	"topology-spread":  {host: "deployment", hostProps: workloadProps(map[string]any{"replicas": 3}), selectors: 3},
}

// TestComponentLabelInvariant_FixturesCoverRegistry: every registered component
// type, trait handler and trait lowering rule has a fixture, and no fixture names
// an unregistered type. A new emitter cannot join the registry without joining
// the invariant test.
func TestComponentLabelInvariant_FixturesCoverRegistry(t *testing.T) {
	wantComponents := slices.Sorted(maps.Keys(builtinComponentHandlers()))
	if got := slices.Sorted(maps.Keys(componentLabelFixtures)); !slices.Equal(got, wantComponents) {
		t.Errorf("componentLabelFixtures covers %v, registry has %v — add a fixture for every registered component type", got, wantComponents)
	}
	wantTraits := slices.Sorted(maps.Keys(builtinTraitHandlers()))
	wantTraits = append(wantTraits, slices.Collect(maps.Keys(builtinTraitLoweringRules()))...)
	slices.Sort(wantTraits)
	if got := slices.Sorted(maps.Keys(traitLabelFixtures)); !slices.Equal(got, wantTraits) {
		t.Errorf("traitLabelFixtures covers %v, registry has %v — add a fixture for every registered trait", got, wantTraits)
	}
}

// TestComponentLabelInvariant_ComponentTypes renders every component type at the
// 63-character boundary and, where the type accepts it, with a 200-character
// name; a type that refuses the long name must still refuse it the same way.
func TestComponentLabelInvariant_ComponentTypes(t *testing.T) {
	boundary, long := boundaryLabelComponentName(t), longLabelComponentName(t)
	for _, typ := range slices.Sorted(maps.Keys(componentLabelFixtures)) {
		fx := componentLabelFixtures[typ]
		t.Run(typ, func(t *testing.T) {
			app := labelInvariantApp(boundary, typ, fx.props, "", nil)
			docs := renderLabelInvariant(t, app)
			n := checkComponentLabelInvariant(t, docs, boundary)
			if fx.labelled {
				requireAppLabel(t, n, typ+" at the boundary name")
			}
			if n.selectors < fx.selectors {
				t.Errorf("%s at the boundary name matched %d pod selectors against its pod template, want at least %d", typ, n.selectors, fx.selectors)
			}

			app = labelInvariantApp(long, typ, fx.props, "", nil)
			if fx.longRefusal != "" {
				_, err := renderLabelInvariantErr(t, app)
				if err == nil {
					t.Fatalf("%s accepted a 200-character name; want the existing refusal %q", typ, fx.longRefusal)
				}
				if !strings.Contains(err.Error(), fx.longRefusal) || !strings.Contains(err.Error(), long) {
					t.Errorf("%s refused the long name with %q, want it to mention %q and the name", typ, err, fx.longRefusal)
				}
				return
			}
			// A labelled type must keep its label past 63 characters too:
			// omitting it would pass the value checks vacuously.
			if n := checkComponentLabelInvariant(t, renderLabelInvariant(t, app), long); fx.labelled {
				requireAppLabel(t, n, typ+" with the 200-character name")
			}
		})
	}
}

// TestComponentLabelInvariant_Traits renders every trait on a host at the
// 63-character boundary and, where the trait can attach to a component type
// that accepts it, on a host with a 200-character name.
func TestComponentLabelInvariant_Traits(t *testing.T) {
	boundary, long := boundaryLabelComponentName(t), longLabelComponentName(t)
	for _, trait := range slices.Sorted(maps.Keys(traitLabelFixtures)) {
		fx := traitLabelFixtures[trait]
		t.Run(trait, func(t *testing.T) {
			host, hostProps := fx.host, fx.hostProps
			if host == "" {
				host, hostProps = webserviceHost()
			}
			docs := renderLabelInvariant(t, labelInvariantApp(boundary, host, hostProps, trait, fx.props))
			n := checkComponentLabelInvariant(t, docs, boundary)
			requireAppLabel(t, n, fmt.Sprintf("%s on a %s host", trait, host))
			if want := max(fx.selectors, 2); n.selectors < want {
				t.Errorf("%s on a %s host matched %d pod selectors against its pod template, want at least %d", trait, host, n.selectors, want)
			}
			if !fx.longHost {
				return
			}
			docs = renderLabelInvariant(t, labelInvariantApp(long, "passthrough", passthroughHostProps, trait, fx.props))
			if n := checkComponentLabelInvariant(t, docs, long); fx.longLabelled {
				requireAppLabel(t, n, trait+" on a long-named host")
			}
		})
	}
}

// recordingReporter collects what checkComponentLabelInvariant reports, so a
// test can assert the check rejects a broken output.
type recordingReporter struct{ errs []string }

func (r *recordingReporter) Helper() {}

func (r *recordingReporter) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// findDoc returns the first emitted object of kind.
func findDoc(t *testing.T, docs []map[string]any, kind string) map[string]any {
	t.Helper()
	for _, d := range docs {
		if d["kind"] == kind {
			return d
		}
	}
	t.Fatalf("no %s in the rendered output", kind)
	return nil
}

// childMap returns m[key] as a map, failing the test when it is not one.
func childMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	c, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is %T, want a map", key, m[key])
	}
	return c
}

// synthesizedPodSelector returns spec.podSelector of the emitted NetworkPolicy
// that selects on the platform component key — the one NetworkPolicy synthesis
// generates for a routed component.
func synthesizedPodSelector(t *testing.T, docs []map[string]any) map[string]any {
	t.Helper()
	const componentKey = kurelDomain + "/component"
	for _, d := range docs {
		if d["kind"] != "NetworkPolicy" {
			continue
		}
		spec, _ := d["spec"].(map[string]any)
		sel, _ := spec["podSelector"].(map[string]any)
		ml, _ := sel["matchLabels"].(map[string]any)
		if _, ok := ml[componentKey]; ok {
			return sel
		}
	}
	t.Fatalf("no NetworkPolicy selecting on %s in the rendered output", componentKey)
	return nil
}

// TestComponentLabelInvariant_RejectsBrokenSelectors: the invariant check
// fails on selector shapes it once let through — a matchExpressions value it
// never read, an expression it never evaluated, and a synthesized
// component-key selector it skipped before validating or matching it — so a
// generated selector carrying any of them cannot pass the guard. Each case
// breaks one emitted selector of a real render and names the report it must
// produce.
func TestComponentLabelInvariant_RejectsBrokenSelectors(t *testing.T) {
	boundary := boundaryLabelComponentName(t)
	host, hostProps := webserviceHost()
	scaler := traitLabelFixtures["scaler"]
	netpol := traitLabelFixtures["networkpolicy"]
	expose := traitLabelFixtures["expose"]
	tooLong := strings.Repeat("v", 200)

	cases := []struct {
		name  string
		trait string
		props map[string]any
		// mutate breaks one selector of the rendered output.
		mutate func(t *testing.T, docs []map[string]any)
		want   []string // substrings every one of which some report must carry
	}{
		{
			name: "pdb expression contradicts its matchLabels", trait: "scaler", props: scaler.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := childMap(t, childMap(t, findDoc(t, docs, "PodDisruptionBudget"), "spec"), "selector")
				sel["matchExpressions"] = []any{map[string]any{"key": "app", "operator": "In", "values": []any{"wrong-component"}}}
			},
			want: []string{"matches no pod template", "want oam.ComponentLabelValue"},
		},
		{
			name: "pdb expression on another key selects no pod", trait: "scaler", props: scaler.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := childMap(t, childMap(t, findDoc(t, docs, "PodDisruptionBudget"), "spec"), "selector")
				sel["matchExpressions"] = []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"backend"}}}
			},
			want: []string{"PodDisruptionBudget", "matches no pod template"},
		},
		{
			name: "pdb selector with only matchExpressions selects no pod", trait: "scaler", props: scaler.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				spec := childMap(t, findDoc(t, docs, "PodDisruptionBudget"), "spec")
				spec["selector"] = map[string]any{"matchExpressions": []any{
					map[string]any{"key": "app", "operator": "In", "values": []any{oam.ComponentLabelValue(boundary)}},
					map[string]any{"key": "tier", "operator": "Exists"},
				}}
			},
			want: []string{"PodDisruptionBudget", "matches no pod template"},
		},
		{
			name: "pdb expression value over 63 characters", trait: "scaler", props: scaler.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := childMap(t, childMap(t, findDoc(t, docs, "PodDisruptionBudget"), "spec"), "selector")
				sel["matchExpressions"] = []any{map[string]any{"key": "tier", "operator": "NotIn", "values": []any{tooLong}}}
			},
			want: []string{"matchExpressions[0].values[0]", "is not a valid label value", "is not a valid label selector"},
		},
		{
			name: "networkpolicy peer expression value over 63 characters", trait: "networkpolicy", props: netpol.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				spec := childMap(t, findDoc(t, docs, "NetworkPolicy"), "spec")
				ingress := asSlice(spec["ingress"])
				if len(ingress) == 0 {
					t.Fatal("rendered NetworkPolicy has no ingress rule")
				}
				from := asSlice(ingress[0].(map[string]any)["from"])
				if len(from) == 0 {
					t.Fatal("rendered NetworkPolicy ingress rule has no peer")
				}
				peer := childMap(t, from[0].(map[string]any), "podSelector")
				peer["matchExpressions"] = []any{map[string]any{"key": "role", "operator": "In", "values": []any{tooLong}}}
			},
			want: []string{"from[0].podSelector.matchExpressions[0].values[0]", "is not a valid label value"},
		},
		{
			name: "synthesized component selector with an invalid operator", trait: "expose", props: expose.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := synthesizedPodSelector(t, docs)
				sel["matchExpressions"] = []any{map[string]any{"key": "tier", "operator": "Bogus", "values": []any{"backend"}}}
			},
			want: []string{"NetworkPolicy", "spec.podSelector", "is not a valid label selector"},
		},
		{
			name: "synthesized component selector contradicts the pods", trait: "expose", props: expose.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := synthesizedPodSelector(t, docs)
				sel["matchExpressions"] = []any{map[string]any{"key": "app", "operator": "NotIn", "values": []any{oam.ComponentLabelValue(boundary)}}}
			},
			want: []string{"NetworkPolicy", "spec.podSelector", "matches no pod template"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docs := renderLabelInvariant(t, labelInvariantApp(boundary, host, hostProps, tc.trait, tc.props))

			var clean recordingReporter
			checkComponentLabelInvariant(&clean, docs, boundary)
			if len(clean.errs) > 0 {
				t.Fatalf("the unbroken render already fails the invariant: %v", clean.errs)
			}

			tc.mutate(t, docs)
			var got recordingReporter
			checkComponentLabelInvariant(&got, docs, boundary)
			for _, w := range tc.want {
				if !slices.ContainsFunc(got.errs, func(e string) bool { return strings.Contains(e, w) }) {
					t.Errorf("no report mentions %q; reports: %v", w, got.errs)
				}
			}
		})
	}
}

// TestComponentLabelInvariant_SelectorsDoNotCountAsLabels: the vacuity check a
// labelled fixture relies on counts emitted `app` labels, not selector values.
// A trait output whose `app` labels are gone but whose selectors still carry the
// projected value must fail it, or the label an emitter stopped writing would go
// unnoticed.
func TestComponentLabelInvariant_SelectorsDoNotCountAsLabels(t *testing.T) {
	long := longLabelComponentName(t)
	fx := traitLabelFixtures["networkpolicy"]
	docs := renderLabelInvariant(t, labelInvariantApp(long, "passthrough", passthroughHostProps, "networkpolicy", fx.props))

	var clean recordingReporter
	requireAppLabel(&clean, checkComponentLabelInvariant(&clean, docs, long), "unbroken render")
	if len(clean.errs) > 0 {
		t.Fatalf("the unbroken render already fails: %v", clean.errs)
	}

	for _, d := range docs {
		stripAppLabels(d)
	}
	sel := childMap(t, childMap(t, findDoc(t, docs, "NetworkPolicy"), "spec"), "podSelector")
	if got := childMap(t, sel, "matchLabels")["app"]; got != oam.ComponentLabelValue(long) {
		t.Fatalf("NetworkPolicy spec.podSelector app = %v, want the projected value still present", got)
	}

	var got recordingReporter
	requireAppLabel(&got, checkComponentLabelInvariant(&got, docs, long), "stripped render")
	if !slices.ContainsFunc(got.errs, func(e string) bool { return strings.Contains(e, "emitted no `app` label") }) {
		t.Errorf("an output with selectors but no `app` label passed the vacuity check; reports: %v", got.errs)
	}
}

// stripAppLabels deletes the `app` key from every map under a `labels` key in
// obj, leaving selectors untouched.
func stripAppLabels(obj any) {
	switch v := obj.(type) {
	case map[string]any:
		for k, child := range v {
			if m, ok := child.(map[string]any); ok && k == "labels" {
				delete(m, "app")
				continue
			}
			stripAppLabels(child)
		}
	case []any:
		for _, child := range v {
			stripAppLabels(child)
		}
	}
}

// labelInvariantApp builds a one-component Application document.
func labelInvariantApp(name, typ string, props map[string]any, trait string, traitProps map[string]any) map[string]any {
	comp := map[string]any{"name": name, "type": typ, "properties": props}
	if trait != "" {
		tr := map[string]any{"type": trait}
		if traitProps != nil {
			tr["properties"] = traitProps
		}
		comp["traits"] = []any{tr}
	}
	return map[string]any{
		"apiVersion": "launcher.gokure.dev/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "label-invariant", "namespace": "default"},
		"spec":       map[string]any{"components": []any{comp}},
	}
}

func renderLabelInvariant(t *testing.T, app map[string]any) []map[string]any {
	t.Helper()
	docs, err := renderLabelInvariantErr(t, app)
	if err != nil {
		t.Fatalf("kurel build failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("kurel build emitted no documents")
	}
	return docs
}

// renderLabelInvariantErr runs `kurel build` on app with labelInvariantProfile
// and returns the emitted objects.
func renderLabelInvariantErr(t *testing.T, app map[string]any) ([]map[string]any, error) {
	t.Helper()
	appYAML, err := yaml.Marshal(app)
	if err != nil {
		t.Fatalf("marshaling test application: %v", err)
	}
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", string(appYAML))
	profilePath := writeTempFile(t, dir, "cluster.yaml", labelInvariantProfile)

	cmd := NewKurelCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		return nil, err
	}
	var docs []map[string]any
	for _, raw := range strings.Split(out.String(), "\n---\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding output document: %v\n%s", err, raw)
		}
		docs = append(docs, obj)
	}
	return docs, nil
}

// invariantReporter is the part of *testing.T the invariant check reports
// through, so the negative tests can run the check on a deliberately broken
// output and assert that it fails.
type invariantReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// checkComponentLabelInvariant asserts, over every emitted object:
//
//   - every label value and every selector value — matchLabels and
//     matchExpressions values alike, in any selector anywhere in the object —
//     is a valid label value;
//   - every `app` label and `app` selector value, and every synthesized
//     `launcher.gokure.dev/component` selector value, equals
//     oam.ComponentLabelValue(name) — labels and selectors agree because both
//     come from that one function;
//   - every selector that targets the component's pods (a workload's own
//     selector and scheduling selectors, a Service, a PodDisruptionBudget, a
//     NetworkPolicy) is a valid label selector — checked for every such
//     selector, before anything else — and, when the output carries pod
//     templates, evaluated whole (matchLabels and matchExpressions), matches a
//     pod template's labels emitted with it;
//   - a synthesized NetworkPolicy selector on the platform component key is
//     evaluated against the pod templates as the platform contract leaves them:
//     with `<domain>/component` = oam.ComponentLabelValue(name) stamped on
//     (TransformContext.ComponentLabelKey; kurel itself stamps nothing).
//
// It returns how many `app` labels it saw — label values only, never selector
// values — and how many pod selectors it matched against a pod template, so a
// caller can reject a vacuous pass.
func checkComponentLabelInvariant(t invariantReporter, docs []map[string]any, name string) invariantCounts {
	t.Helper()
	want := oam.ComponentLabelValue(name)
	const componentKey = kurelDomain + "/component"
	appLabels, selectorsMatched := 0, 0
	checkValue := func(where, key string, v any, label bool) {
		s, ok := v.(string)
		if !ok {
			t.Errorf("%s: %s = %v (%T), want a string", where, key, v, v)
			return
		}
		if errs := validation.IsValidLabelValue(s); len(errs) > 0 {
			t.Errorf("%s: %s = %q (%d characters) is not a valid label value: %v", where, key, s, len(s), errs)
		}
		if key == "app" || key == componentKey {
			if label && key == "app" {
				appLabels++
			}
			if s != want {
				t.Errorf("%s: %s = %q, want oam.ComponentLabelValue(%d-character name) = %q", where, key, s, len(name), want)
			}
		}
	}

	var templates, stamped []labels.Set
	for _, doc := range docs {
		kind, _ := doc["kind"].(string)
		md, _ := doc["metadata"].(map[string]any)
		where := fmt.Sprintf("%s/%v", kind, md["name"])
		walkLabelMaps(doc, where, checkValue)
		if tl := podTemplateLabels(doc); tl != nil {
			set := labelSet(tl)
			templates = append(templates, set)
			withStamp := maps.Clone(set)
			withStamp[componentKey] = want
			stamped = append(stamped, withStamp)
		}
	}

	for _, doc := range docs {
		kind, _ := doc["kind"].(string)
		md, _ := doc["metadata"].(map[string]any)
		where := fmt.Sprintf("%s/%v", kind, md["name"])
		for _, ps := range podSelectors(doc) {
			if ps.err != nil {
				t.Errorf("%s: %s does not decode as a label selector: %v", where, ps.path, ps.err)
				continue
			}
			sel, err := metav1.LabelSelectorAsSelector(ps.selector)
			if err != nil {
				t.Errorf("%s: %s is not a valid label selector: %v", where, ps.path, err)
				continue
			}
			if len(templates) == 0 {
				continue // no pods in this output (e.g. a Service fronting another component)
			}
			candidates := templates
			if kind == "NetworkPolicy" && selectsOnKey(ps.selector, componentKey) {
				candidates = stamped
			}
			selectorsMatched++
			if !slices.ContainsFunc(candidates, func(tl labels.Set) bool { return sel.Matches(tl) }) {
				t.Errorf("%s: %s %q matches no pod template emitted with it (templates: %v)", where, ps.path, sel.String(), candidates)
			}
		}
	}
	return invariantCounts{appLabels: appLabels, selectors: selectorsMatched}
}

// selectsOnKey reports whether sel names key in its matchLabels or in any
// matchExpressions requirement.
func selectsOnKey(sel *metav1.LabelSelector, key string) bool {
	if _, ok := sel.MatchLabels[key]; ok {
		return true
	}
	return slices.ContainsFunc(sel.MatchExpressions, func(r metav1.LabelSelectorRequirement) bool { return r.Key == key })
}

// requireAppLabel reports a vacuous pass: output that must carry an `app`
// label, of which the invariant check saw none. Only emitted labels count; a
// selector carrying the value does not stand in for the label it targets.
func requireAppLabel(t invariantReporter, n invariantCounts, what string) {
	t.Helper()
	if n.appLabels == 0 {
		t.Errorf("%s emitted no `app` label; the invariant check is vacuous for it", what)
	}
}

// labelSet converts decoded pod template labels to a labels.Set. A non-string
// value is left out here; walkLabelMaps has already reported it.
func labelSet(m map[string]any) labels.Set {
	out := make(labels.Set, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// invariantCounts is what checkComponentLabelInvariant actually checked.
type invariantCounts struct {
	// appLabels counts emitted `app` labels (maps under a `labels` key: object
	// metadata and pod templates) compared with the projection. Selector values
	// are compared too but not counted: a selector alone does not show that the
	// label it targets was emitted.
	appLabels int
	selectors int // pod selectors compared with an emitted pod template
}

// walkLabelMaps calls check for every value of every map found under a
// `labels` or `matchLabels` key, for every value of every requirement in a
// `matchExpressions` list (with the requirement's key), and for every value of
// a Service's `spec.selector`, anywhere in obj — so the selectors nested in
// scheduling terms, namespaceSelectors and NetworkPolicy peers are covered as
// well as the top-level ones. label is true only for a value of a map under a
// `labels` key, an emitted label rather than a selector value.
func walkLabelMaps(obj any, where string, check func(where, key string, v any, label bool)) {
	switch v := obj.(type) {
	case map[string]any:
		for k, child := range v {
			if m, ok := child.(map[string]any); ok && (k == "labels" || k == "matchLabels") {
				for lk, lv := range m {
					check(where+"."+k, lk, lv, k == "labels")
				}
				continue
			}
			if exprs, ok := child.([]any); ok && k == "matchExpressions" {
				for i, e := range exprs {
					em, _ := e.(map[string]any)
					key, _ := em["key"].(string)
					for j, ev := range asSlice(em["values"]) {
						check(fmt.Sprintf("%s.%s[%d].values[%d]", where, k, i, j), key, ev, false)
					}
				}
				continue
			}
			walkLabelMaps(child, where+"."+k, check)
		}
		if v["kind"] == "Service" {
			if spec, ok := v["spec"].(map[string]any); ok {
				if sel, ok := spec["selector"].(map[string]any); ok {
					for lk, lv := range sel {
						check(where+".spec.selector", lk, lv, false)
					}
				}
			}
		}
	case []any:
		for i, child := range v {
			walkLabelMaps(child, fmt.Sprintf("%s[%d]", where, i), check)
		}
	}
}

// podTemplateLabels returns the pod template labels of a workload object.
func podTemplateLabels(doc map[string]any) map[string]any {
	spec, _ := doc["spec"].(map[string]any)
	if doc["kind"] == "CronJob" {
		jt, _ := spec["jobTemplate"].(map[string]any)
		spec, _ = jt["spec"].(map[string]any)
	}
	tmpl, _ := spec["template"].(map[string]any)
	md, _ := tmpl["metadata"].(map[string]any)
	labels, _ := md["labels"].(map[string]any)
	return labels
}

// podSelector is one emitted selector that picks the component's pods, decoded
// whole — matchLabels and matchExpressions — or the reason it would not decode.
type podSelector struct {
	path     string
	selector *metav1.LabelSelector
	err      error
}

// podSelectors returns every selector in doc that picks the component's own
// pods: a workload's own selector, its topology-spread and pod-(anti-)affinity
// selectors, a Service's selector, a PodDisruptionBudget's and a
// NetworkPolicy's spec.podSelector. A selector carrying only matchExpressions
// is included. Selectors that pick something else — a namespaceSelector, or a
// NetworkPolicy peer, which selects traffic sources — are not pod selectors of
// this component; walkLabelMaps still checks their values.
func podSelectors(doc map[string]any) []podSelector {
	var out []podSelector
	add := func(path string, sel any) {
		if m, ok := sel.(map[string]any); ok {
			ls, err := decodeLabelSelector(m)
			out = append(out, podSelector{path, ls, err})
		}
	}
	spec, _ := doc["spec"].(map[string]any)
	switch doc["kind"] {
	case "Deployment", "StatefulSet", "DaemonSet":
		add("spec.selector", spec["selector"])
		tmpl, _ := spec["template"].(map[string]any)
		ps, _ := tmpl["spec"].(map[string]any)
		for i, c := range asSlice(ps["topologySpreadConstraints"]) {
			cm, _ := c.(map[string]any)
			add(fmt.Sprintf("topologySpreadConstraints[%d].labelSelector", i), cm["labelSelector"])
		}
		aff, _ := ps["affinity"].(map[string]any)
		for _, kind := range []string{"podAffinity", "podAntiAffinity"} {
			pa, _ := aff[kind].(map[string]any)
			for i, term := range asSlice(pa["requiredDuringSchedulingIgnoredDuringExecution"]) {
				tm, _ := term.(map[string]any)
				add(fmt.Sprintf("%s.required[%d].labelSelector", kind, i), tm["labelSelector"])
			}
			for i, w := range asSlice(pa["preferredDuringSchedulingIgnoredDuringExecution"]) {
				wm, _ := w.(map[string]any)
				tm, _ := wm["podAffinityTerm"].(map[string]any)
				add(fmt.Sprintf("%s.preferred[%d].labelSelector", kind, i), tm["labelSelector"])
			}
		}
	case "Service":
		if sel, ok := spec["selector"].(map[string]any); ok {
			add("spec.selector", map[string]any{"matchLabels": sel})
		}
	case "PodDisruptionBudget":
		add("spec.selector", spec["selector"])
	case "NetworkPolicy":
		add("spec.podSelector", spec["podSelector"])
	}
	return out
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// decodeLabelSelector decodes an emitted selector into a metav1.LabelSelector,
// refusing a field LabelSelector does not have, so an unexpected selector shape
// fails the check instead of being dropped from it.
func decodeLabelSelector(m map[string]any) (*metav1.LabelSelector, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var ls metav1.LabelSelector
	if err := dec.Decode(&ls); err != nil {
		return nil, err
	}
	return &ls, nil
}
