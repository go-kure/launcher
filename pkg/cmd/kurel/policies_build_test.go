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
    - name: flux
      type: reconciliation
      properties:
        interval: 5m
        prune: true
    - name: extra
      type: health-checks
      properties:
        checks:
          - apiVersion: batch/v1
            kind: Job
            name: db-migrate
            namespace: default
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

// TestBuildCommand_BuiltinPolicies: a document carrying all four built-in policy
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

// TestBuiltinPolicies_ShapeTheCluster checks that each registered handler's
// result actually reaches the cluster tree: the dependency edge becomes a
// bundle DependsOn, the reconciliation settings and the extra health check land
// on every leaf bundle, and the placement override is recorded.
func TestBuiltinPolicies_ShapeTheCluster(t *testing.T) {
	cluster, result, err := transformWithBuiltins(t, allBuiltinPoliciesYAML)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := result.TierOverrides["api"]; got != oam.TierInfra {
		t.Errorf("TierOverrides[api] = %q, want %q", got, oam.TierInfra)
	}

	bundles := leafBundles(cluster.Node)
	web, api := bundles["shop-web"], bundles["shop-api"]
	if web == nil || api == nil {
		t.Fatalf("want per-component bundles shop-web and shop-api (dependency-aware cluster), got %v", slices.Sorted(maps.Keys(bundles)))
	}
	if !slices.Contains(web.DependsOn, api) {
		t.Errorf("shop-web does not depend on shop-api")
	}
	if slices.Contains(api.DependsOn, web) {
		t.Errorf("shop-api depends on shop-web; the edge is reversed")
	}

	extra := stack.HealthCheck{APIVersion: "batch/v1", Kind: "Job", Name: "db-migrate", Namespace: "default"}
	for name, b := range bundles {
		if b.Interval != "5m" {
			t.Errorf("%s: Interval = %q, want 5m", name, b.Interval)
		}
		if b.Prune == nil || !*b.Prune {
			t.Errorf("%s: Prune = %v, want true", name, b.Prune)
		}
		if !slices.Contains(b.HealthChecks, extra) {
			t.Errorf("%s: HealthChecks %v lack the policy's entry", name, b.HealthChecks)
		}
	}
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
			name: "placement unknown tier",
			policies: `    - name: odd
      type: placement
      properties:
        component: api
        tier: edge
`,
			wantSub: []string{`policy "odd"`, `unknown tier "edge"`},
		},
		{
			name: "reconciliation bad duration",
			policies: `    - name: flux
      type: reconciliation
      properties:
        interval: often
`,
			wantSub: []string{`policy "flux"`, `interval "often" is not a valid duration`},
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
