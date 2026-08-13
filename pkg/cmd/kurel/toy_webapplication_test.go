package kurel

import (
	"os"
	"testing"

	kio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/lowering"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
)

// findNodeByName searches every cluster's node tree for a node with the given name.
func findNodeByName(clusters []*stack.Cluster, name string) *stack.Node {
	var search func(node *stack.Node) *stack.Node
	search = func(node *stack.Node) *stack.Node {
		if node == nil {
			return nil
		}
		if node.Name == name {
			return node
		}
		for _, child := range node.Children {
			if found := search(child); found != nil {
				return found
			}
		}
		return nil
	}
	for _, cluster := range clusters {
		if found := search(cluster.Node); found != nil {
			return found
		}
	}
	return nil
}

// toyLoweringTransformer builds a Transformer with the production built-in
// component/trait handlers PLUS the spike's toy lowering rules (pkg/oam/builtin/
// lowering) and terminal "dependency" policy handler (pkg/oam/builtin/policies).
// These are never registered by newBuiltinTransformer (build.go) — this is the one
// place in the repo that exercises the full toy chain end to end through the real
// pipeline, proving D1-D5 with running code rather than the engine skeleton alone.
func toyLoweringTransformer() *oam.Transformer {
	tr := oam.NewTransformer(builtinComponentHandlers(), builtinTraitHandlers())
	tr.RegisterDocumentLowering(lowering.WebApplicationRule{})
	tr.RegisterComponentLowering(lowering.WebAndCacheRule{})
	tr.RegisterPolicyLowering(lowering.OrderedRule{})
	tr.RegisterPolicy("dependency", policies.DependencyHandler{})
	return tr
}

// TestToyWebApplication_LoweringChain is the C4 proof: a "WebApplication" document
// (D1 document position) splits 1->2 (D2), one half's "web-and-cache" component (D1
// component position) lowers 1->2 components plus an emitted "ordered" policy (D2,
// component-emits-policy), which itself (D1 policy position) lowers into a terminal
// "dependency" policy that the real pipeline wires into a stack.Bundle.DependsOn
// edge — observable in the golden output as two documents' worth of objects plus
// the dependency ordering. Set UPDATE_GOLDEN=1 to regenerate expected.yaml, mirroring
// TestFixtures' convention.
func TestToyWebApplication_LoweringChain(t *testing.T) {
	const dir = "testdata/toy-webapplication"

	appData, err := os.ReadFile(dir + "/app.yaml")
	if err != nil {
		t.Fatalf("reading app.yaml: %v", err)
	}
	profileData, err := os.ReadFile(dir + "/cluster.yaml")
	if err != nil {
		t.Fatalf("reading cluster.yaml: %v", err)
	}

	tr := toyLoweringTransformer()

	app, err := oam.ParseWithExtraTypes(appData, nil, tr.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing app.yaml: %v", err)
	}

	profile, err := oam.ParseClusterProfile(profileData)
	if err != nil {
		t.Fatalf("parsing cluster.yaml: %v", err)
	}
	evaluatedProfile, err := tr.EvaluateProfile(profile)
	if err != nil {
		t.Fatalf("evaluating profile: %v", err)
	}

	ctx := oam.TransformContext{
		Capabilities: evaluatedProfile.Spec.Capabilities,
		Domain:       kurelDomain,
	}

	clusters, err := tr.TransformAll(app, ctx)
	if err != nil {
		t.Fatalf("TransformAll: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters (one per split document), got %d", len(clusters))
	}

	// The golden YAML below is a flat client.Object dump — collectFromNode never
	// surfaces stack.Bundle.DependsOn — so the "dependsOn edges" half of this test's
	// proof is asserted directly against the Node/Bundle tree instead: the
	// "ordered"->"dependency" lowering chain (webandcache.go, ordered.go,
	// builtin/policies/dependency.go) must have wired shop-web's bundle to depend on
	// shop-cache's bundle.
	webNode := findNodeByName(clusters, "shop-web")
	if webNode == nil || webNode.Bundle == nil {
		t.Fatal("expected a bundle node named \"shop-web\"")
	}
	cacheNode := findNodeByName(clusters, "shop-cache")
	if cacheNode == nil || cacheNode.Bundle == nil {
		t.Fatal("expected a bundle node named \"shop-cache\"")
	}
	dependsOnCache := false
	for _, dep := range webNode.Bundle.DependsOn {
		if dep == cacheNode.Bundle {
			dependsOnCache = true
		}
	}
	if !dependsOnCache {
		t.Fatalf("expected shop-web's bundle to DependsOn shop-cache's bundle (via the ordered->dependency lowering chain), got DependsOn=%v", webNode.Bundle.DependsOn)
	}

	var objects []*client.Object
	for _, cluster := range clusters {
		clusterObjects, err := collectFromNode(cluster.Node)
		if err != nil {
			t.Fatalf("collecting manifests: %v", err)
		}
		objects = append(objects, clusterObjects...)
	}

	got, err := kio.EncodeObjectsToYAML(objects)
	if err != nil {
		t.Fatalf("encoding YAML output: %v", err)
	}

	expectedPath := dir + "/expected.yaml"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(expectedPath, got, 0644); err != nil {
			t.Fatalf("writing golden file %q: %v", expectedPath, err)
		}
		return
	}

	want, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("reading expected file %q: %v (run with UPDATE_GOLDEN=1 to generate)", expectedPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("toy-webapplication output mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}
