package kurel

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the delivery boundary (go-kure/launcher#781): launcher returns
// bundles with objects and ordering only, and leaves every Flux delivery field of
// a stack.Bundle for the consumer that delivers the application.

// permissivePolicy admits what NoopPolicy's default-deny refuses, so every example
// transforms: example 12 mounts a hostPath volume.
type permissivePolicy struct{ *oam.NoopPolicy }

func (permissivePolicy) AllowHostNetwork() bool     { return true }
func (permissivePolicy) AllowPrivileged() bool      { return true }
func (permissivePolicy) AllowHostPID() bool         { return true }
func (permissivePolicy) AllowHostIPC() bool         { return true }
func (permissivePolicy) AllowHostPathVolumes() bool { return true }

// allBundles returns every bundle under node: each node's bundle and, below an
// umbrella, its children.
func allBundles(node *stack.Node) []*stack.Bundle {
	var out []*stack.Bundle
	var walkBundle func(b *stack.Bundle)
	walkBundle = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		out = append(out, b)
		for _, c := range b.Children {
			walkBundle(c)
		}
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

// assertNoDeliveryFields fails for every Flux delivery field a bundle of cluster
// carries.
func assertNoDeliveryFields(t *testing.T, cluster *stack.Cluster) {
	t.Helper()
	bundles := allBundles(cluster.Node)
	if len(bundles) == 0 {
		t.Fatal("the cluster has no bundle to check")
	}
	for _, b := range bundles {
		set := map[string]bool{
			"HealthChecks":  len(b.HealthChecks) > 0,
			"Interval":      b.Interval != "",
			"RetryInterval": b.RetryInterval != "",
			"Timeout":       b.Timeout != "",
			"Prune":         b.Prune != nil,
			"Wait":          b.Wait != nil,
			"Force":         b.Force != nil,
			"Suspend":       b.Suspend != nil,
			"Patches":       len(b.Patches) > 0,
			"PostBuild":     b.PostBuild != nil,
		}
		for field, isSet := range set {
			if isSet {
				t.Errorf("bundle %q: launcher set the Flux delivery field %s", b.Name, field)
			}
		}
	}
}

// transformDocument runs the path runBuild takes on one document against one
// ClusterProfile, with a permissive policy and the given Flux namespace.
func transformDocument(appData, profileData []byte, fluxNamespace string) (*stack.Cluster, error) {
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes(appData, nil, transformer.LowerableTypes())
	if err != nil {
		return nil, err
	}
	profile, err := oam.ParseClusterProfile(profileData)
	if err != nil {
		return nil, err
	}
	evaluated, err := transformer.EvaluateProfile(profile)
	if err != nil {
		return nil, err
	}
	if err := transformer.ValidateAuthoredPropertiesWithCapabilities(app, evaluated.Spec.Capabilities); err != nil {
		return nil, err
	}
	return transformer.Transform(app, oam.TransformContext{
		ClusterID:     "local",
		Capabilities:  evaluated.Spec.Capabilities,
		Domain:        kurelDomain,
		FluxNamespace: fluxNamespace,
		Policy:        permissivePolicy{&oam.NoopPolicy{}},
	})
}

// TestTransform_ExamplesCarryNoDeliveryFields walks the transform result of every
// example, built against each example ClusterProfile that accepts it and both
// without and with a Flux namespace: no bundle carries a Flux delivery field.
func TestTransform_ExamplesCarryNoDeliveryFields(t *testing.T) {
	examples, err := filepath.Glob("../../../examples/*.yaml")
	if err != nil {
		t.Fatalf("globbing the examples: %v", err)
	}
	if len(examples) < 15 {
		t.Fatalf("found %d examples under examples/, want at least 15", len(examples))
	}
	profiles := []string{"minimal.yaml", "nginx-certmanager-vault.yaml", "gateway-certmanager-aws.yaml"}

	for _, example := range examples {
		t.Run(filepath.Base(example), func(t *testing.T) {
			appData, err := os.ReadFile(example)
			if err != nil {
				t.Fatalf("reading the example: %v", err)
			}
			transformed := 0
			var lastErr error
			for _, profile := range profiles {
				profileData, err := os.ReadFile(filepath.Join("../../../examples/cluster-profiles", profile))
				if err != nil {
					t.Fatalf("reading the profile: %v", err)
				}
				for _, fluxNamespace := range []string{"", "flux-system"} {
					cluster, err := transformDocument(appData, profileData, fluxNamespace)
					if err != nil {
						lastErr = err
						continue
					}
					transformed++
					assertNoDeliveryFields(t, cluster)
				}
			}
			if transformed == 0 {
				t.Fatalf("no example ClusterProfile transforms this example: %v", lastErr)
			}
		})
	}
}

// TestTransform_FixturesCarryNoDeliveryFields walks the transform result of every
// kurel fixture the same way, which adds the component kinds and traits the
// examples do not use.
func TestTransform_FixturesCarryNoDeliveryFields(t *testing.T) {
	scenarios, err := filepath.Glob("testdata/*/app.yaml")
	if err != nil {
		t.Fatalf("globbing fixture scenarios: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no fixture scenarios found under testdata/*/app.yaml")
	}
	checked := 0
	for _, appPath := range scenarios {
		dir := filepath.Dir(appPath)
		// A parameterized fixture needs its values resolved first; TestFixtures
		// builds it, and its handlers are the ones the other fixtures cover.
		if fileExists(filepath.Join(dir, "kurel.yaml")) {
			continue
		}
		t.Run(filepath.Base(dir), func(t *testing.T) {
			appData, err := os.ReadFile(appPath)
			if err != nil {
				t.Fatalf("reading the fixture: %v", err)
			}
			profileData, err := os.ReadFile(filepath.Join(dir, "cluster.yaml"))
			if err != nil {
				t.Fatalf("reading the fixture's profile: %v", err)
			}
			for _, fluxNamespace := range []string{"", "flux-system"} {
				cluster, err := transformDocument(appData, profileData, fluxNamespace)
				if err != nil {
					t.Fatalf("transforming (flux namespace %q): %v", fluxNamespace, err)
				}
				assertNoDeliveryFields(t, cluster)
			}
		})
		checked++
	}
	if checked == 0 {
		t.Fatal("every fixture was skipped")
	}
}

// TestBuildCommand_FluxDeliveryFlagsAreGone: kurel writes plain YAML only, so the
// two flags of the former Flux delivery mode are unknown.
func TestBuildCommand_FluxDeliveryFlagsAreGone(t *testing.T) {
	for _, flag := range []string{"--oci-repository", "--oci-tag"} {
		t.Run(flag, func(t *testing.T) {
			cmd := NewKurelCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"build", "testdata/app.yaml", "--profile", "testdata/cluster.yaml", flag, "oci://registry.example/shop"})
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("build succeeded, want an unknown-flag error\noutput:\n%s", out.String())
			}
			if want := "unknown flag: " + flag; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want to contain %q", err, want)
			}
		})
	}
}

// TestBuildCommand_DeliveryConcernsHaveNoBuiltin: the two policies and two traits
// that configured Flux delivery are not launcher built-ins. A document using one
// fails with the "no handler" error, which says whose concern it is.
func TestBuildCommand_DeliveryConcernsHaveNoBuiltin(t *testing.T) {
	const hint = "a consumer that delivers through Flux registers its own handler"
	traitApp := func(trait string) string {
		return `apiVersion: launcher.gokure.dev/v1alpha1
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
      traits:
` + trait
	}
	cases := []struct {
		name, position, app string
	}{
		{"reconciliation", "policy", policyAppHeader + `    - name: flux
      type: reconciliation
      properties:
        interval: 5m
`},
		{"health-checks", "policy", policyAppHeader + `    - name: extra
      type: health-checks
      properties:
        checks:
          - apiVersion: batch/v1
            kind: Job
            name: db-migrate
            namespace: default
`},
		{"fluxcd-patches", "trait", traitApp(`        - type: fluxcd-patches
          properties:
            patches:
              - patch: |
                  - op: add
                    path: /metadata/labels/patched
                    value: "true"
                target:
                  kind: Deployment
`)},
		{"fluxcd-postbuild", "trait", traitApp(`        - type: fluxcd-postbuild
          properties:
            substitute:
              region: eu-west-1
`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := builtinPolicyHandlers()[tc.name]; ok {
				t.Fatalf("builtinPolicyHandlers() registers %q", tc.name)
			}
			if _, ok := builtinTraitHandlers()[tc.name]; ok {
				t.Fatalf("builtinTraitHandlers() registers %q", tc.name)
			}
			_, out, err := buildDocs(t, tc.app)
			if err == nil {
				t.Fatalf("build succeeded, want an error\noutput:\n%s", out)
			}
			for _, want := range []string{fmt.Sprintf("no handler for %s type %q", tc.position, tc.name), hint} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want to contain %q", err, want)
				}
			}
		})
	}
}

// TestTransform_PolicyShapedClustersCarryNoDeliveryFields covers the two cluster
// shapes the examples do not reach: one bundle per component (a dependency
// policy) and one bundle per tier (a placement policy).
func TestTransform_PolicyShapedClustersCarryNoDeliveryFields(t *testing.T) {
	for name, policy := range map[string]string{
		"dependency": `    - name: order
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: [api]
`,
		"placement": `    - name: api-first
      type: placement
      properties:
        component: api
        tier: infra
`,
	} {
		t.Run(name, func(t *testing.T) {
			cluster, _, err := transformWithBuiltins(t, policyAppHeader+policy)
			if err != nil {
				t.Fatalf("transforming: %v", err)
			}
			if got := len(allBundles(cluster.Node)); got < 2 {
				t.Fatalf("the cluster has %d bundle(s); the policy should have split it", got)
			}
			assertNoDeliveryFields(t, cluster)
		})
	}
}
