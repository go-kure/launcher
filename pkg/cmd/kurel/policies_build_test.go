package kurel

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the built-in policy handlers (go-kure/launcher#272) through
// kurel's own registration: before builtinPolicyHandlers existed, every
// Application carrying a policy failed `kurel build` with "no handler for policy
// type".

const policyAppHeader = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 8080
    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
  policies:
`

const allBuiltinPoliciesYAML = policyAppHeader + `    - name: order
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: [api]
    - name: api-first
      type: placement
      properties:
        component: api
        tier: infra
`

// transformWithBuiltins runs parse -> ValidateAuthoredProperties -> Transform
// with kurel's own transformer, the path runBuild takes, and returns the cluster
// tree and policy result that `kurel build`'s flat manifest output does not show.
func transformWithBuiltins(t *testing.T, appYAML string) (*stack.Cluster, *oam.PolicyResult, error) {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating: %v", err)
	}
	return transformer.TransformWithPolicy(app, oam.TransformContext{Domain: kurelDomain})
}

// leafBundles returns every leaf bundle under node, keyed by name.
func leafBundles(node *stack.Node) map[string]*stack.Bundle {
	out := map[string]*stack.Bundle{}
	var walkBundle func(b *stack.Bundle)
	walkBundle = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		if b.IsUmbrella() {
			for _, c := range b.Children {
				walkBundle(c)
			}
			return
		}
		out[b.Name] = b
	}
	var walk func(n *stack.Node)
	walk = func(n *stack.Node) {
		if n == nil {
			return
		}
		walkBundle(n.Bundle)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(node)
	return out
}

// TestBuildCommand_BuiltinPolicies: a document carrying both built-in policy
// types builds through `kurel build` and still emits both components' workloads.
func TestBuildCommand_BuiltinPolicies(t *testing.T) {
	docs, out, err := buildDocs(t, allBuiltinPoliciesYAML)
	if err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out)
	}
	deployments := map[string]bool{}
	for _, d := range docs {
		if d["kind"] != "Deployment" {
			continue
		}
		md, _ := d["metadata"].(map[string]any)
		name, _ := md["name"].(string)
		deployments[name] = true
	}
	for _, want := range []string{"api", "web"} {
		if !deployments[want] {
			t.Errorf("output has no Deployment %q\noutput:\n%s", want, out)
		}
	}
}

// TestBuiltinDependencyPolicy_OrdersTheGroups proves the dependency handler's
// edge on its own. There is no placement policy, so the two groups, and the
// second one's dependsOn on the first, exist only because the dependency policy
// declared the order.
func TestBuiltinDependencyPolicy_OrdersTheGroups(t *testing.T) {
	cluster, result, err := transformWithBuiltins(t, policyAppHeader+`    - name: order
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: [api]
`)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(result.TierOverrides) != 0 {
		t.Fatalf("TierOverrides = %v, want none (the case must not rely on placement)", result.TierOverrides)
	}
	if got, want := groupNames(t, cluster), []string{"shop-00: api", "shop-01: web"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// TestBuiltinPlacementPolicy_RegroupsTheComponents proves placement is consumed
// on its own, with no dependency policy: api placed in infra and web in apps
// give the groups shop-infra and shop-apps, the second depending on the first.
func TestBuiltinPlacementPolicy_RegroupsTheComponents(t *testing.T) {
	cluster, result, err := transformWithBuiltins(t, policyAppHeader+`    - name: api-first
      type: placement
      properties:
        component: api
        tier: infra
    - name: web-last
      type: placement
      properties:
        component: web
        tier: apps
`)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if result.HasDependencies() {
		t.Fatalf("Dependencies = %v, want none (the case must not rely on a dependency policy)", result.Dependencies)
	}
	if got, want := groupNames(t, cluster), []string{"shop-infra: api", "shop-apps: web"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// TestBuiltinPlacementPolicy_OneTierOrdersNothing pins that a component nothing
// places is in no tier (go-kure/launcher#783): with api alone placed, no tier
// follows another, so the application stays one flat bundle.
func TestBuiltinPlacementPolicy_OneTierOrdersNothing(t *testing.T) {
	cluster, result, err := transformWithBuiltins(t, policyAppHeader+`    - name: api-first
      type: placement
      properties:
        component: api
        tier: infra
`)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := result.TierOverrides["api"]; got != oam.TierInfra {
		t.Fatalf("TierOverrides[api] = %q, want infra (the case must exercise placement)", got)
	}
	bundles := leafBundles(cluster.Node)
	if got := slices.Sorted(maps.Keys(bundles)); !slices.Equal(got, []string{"shop"}) {
		t.Errorf("bundles = %v, want the one flat bundle shop", got)
	}
}

// TestBuiltinPolicies_ShapeTheCluster checks, with both built-in policies in one
// document, that the placement override is recorded and that the policies shape
// the cluster without setting a Flux delivery field on any bundle. The dependency
// and placement effects are proven independently above.
func TestBuiltinPolicies_ShapeTheCluster(t *testing.T) {
	cluster, result, err := transformWithBuiltins(t, allBuiltinPoliciesYAML)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := result.TierOverrides["api"]; got != oam.TierInfra {
		t.Errorf("TierOverrides[api] = %q, want %q", got, oam.TierInfra)
	}

	if len(result.Extensions) != 0 {
		t.Errorf("Extensions = %v, want empty: no built-in policy writes one", result.Extensions)
	}
	if len(leafBundles(cluster.Node)) == 0 {
		t.Fatal("cluster has no leaf bundles")
	}
	assertNoDeliveryFields(t, cluster)
}

// TestBuildCommand_PolicyHandlerErrorsSurface: a handler's own validation is a
// build error, naming the policy.
func TestBuildCommand_PolicyHandlerErrorsSurface(t *testing.T) {
	cases := []struct {
		name     string
		policies string
		wantSub  []string
	}{
		{
			name: "dependency cycle",
			policies: `    - name: loop
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: [api]
          - component: api
            dependsOn: [web]
`,
			wantSub: []string{`policy "loop"`, "circular dependency detected: api -> web -> api"},
		},
		{
			name: "placement unknown component",
			policies: `    - name: odd
      type: placement
      properties:
        component: cache
        tier: infra
`,
			wantSub: []string{`policy "odd"`, `unknown component "cache"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := buildDocs(t, policyAppHeader+tc.policies)
			if err == nil {
				t.Fatalf("build succeeded, want an error\noutput:\n%s", out)
			}
			for _, s := range tc.wantSub {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error = %q, want to contain %q", err, s)
				}
			}
		})
	}
}

// TestBuildCommand_PolicyPropertiesAreChecked: an authored built-in policy is
// checked against its handler's PropertySchema before the transform, so a
// misspelt key or a wrongly typed value fails `kurel build` instead of being
// dropped by a handler that never reads it. The placement cases carry a valid
// component and tier beside the bad key on purpose: the handler reads both and
// never the misspelt one, so without this check the build would succeed.
func TestBuildCommand_PolicyPropertiesAreChecked(t *testing.T) {
	cases := []struct {
		name     string
		policies string
		wantSub  []string
	}{
		{
			name: "placement misspelt key",
			policies: `    - name: odd
      type: placement
      properties:
        component: api
        tier: infra
        teir: apps
`,
			wantSub: []string{`policy "odd" (type "placement")`, `unsupported field "teir"`},
		},
		{
			name: "dependency wrongly typed value",
			policies: `    - name: order
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: api
`,
			wantSub: []string{`policy "order" (type "dependency")`, `properties.rules[0].dependsOn: expected array`},
		},
		{
			name: "placement tier outside the enum",
			policies: `    - name: odd
      type: placement
      properties:
        component: api
        tier: edge
`,
			wantSub: []string{`policy "odd" (type "placement")`, `properties.tier: value edge not in allowed set`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := buildDocs(t, policyAppHeader+tc.policies)
			if err == nil {
				t.Fatalf("build succeeded, want an error\noutput:\n%s", out)
			}
			for _, s := range tc.wantSub {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error = %q, want to contain %q", err, s)
				}
			}
		})
	}
}

// TestBuildCommand_AppDependencyPolicyHasNoBuiltin pins the decision that
// app-dependency is not a built-in: it orders one application after others, a
// single-application build has nothing to order against, and accepting it would
// drop it in silence. It keeps failing loudly instead.
func TestBuildCommand_AppDependencyPolicyHasNoBuiltin(t *testing.T) {
	if _, ok := builtinPolicyHandlers()["app-dependency"]; ok {
		t.Fatal(`builtinPolicyHandlers() registers "app-dependency"`)
	}
	_, out, err := buildDocs(t, policyAppHeader+`    - name: after-payments
      type: app-dependency
      properties:
        dependsOn: [payments]
`)
	if err == nil {
		t.Fatalf("build succeeded, want an error\noutput:\n%s", out)
	}
	if want := `no handler for policy type "app-dependency"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want to contain %q", err, want)
	}
}
