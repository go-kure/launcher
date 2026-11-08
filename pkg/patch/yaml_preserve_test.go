package patch

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestCommentPreservation tests that YAML comments are preserved during patching
func TestCommentPreservation(t *testing.T) {
	testYAML := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config  # This is a name comment
  namespace: default
  # This is a metadata comment
  labels:
    app: test      # App label comment
    version: "1.0" # Version comment
data:
  # Configuration data section
  config.yaml: |
    # Internal config comment
    server:
      port: "8080"  # Port comment
      host: localhost
  other.txt: "value"  # Other file comment
---
# Second resource comment
apiVersion: v1
kind: Service
metadata:
  name: test-service
  # Service metadata comment
spec:
  type: ClusterIP    # Service type comment
  ports:
  - name: http       # HTTP port comment
    port: "80"
    targetPort: "8080"
  selector:
    app: test        # Selector comment`

	// Load with structure preservation
	docSet, err := LoadResourcesWithStructure(strings.NewReader(testYAML))
	if err != nil {
		t.Fatalf("Failed to load YAML with structure: %v", err)
	}

	// Verify we loaded the documents correctly
	if len(docSet.Documents) != 2 {
		t.Fatalf("Expected 2 documents, got %d", len(docSet.Documents))
	}

	// Verify first document is ConfigMap
	configMap := docSet.Documents[0]
	if configMap.Resource.GetKind() != "ConfigMap" {
		t.Fatalf("Expected ConfigMap, got %s", configMap.Resource.GetKind())
	}

	// Verify second document is Service
	service := docSet.Documents[1]
	if service.Resource.GetKind() != "Service" {
		t.Fatalf("Expected Service, got %s", service.Resource.GetKind())
	}

	// Test that comments are preserved in the YAML nodes
	// Check for specific comments in the ConfigMap
	configMapComments := extractAllComments(configMap.Node)
	expectedComments := []string{
		"This is a name comment",
		"This is a metadata comment",
		"App label comment",
		"Version comment",
		"Configuration data section",
		"Other file comment",
	}

	for _, expected := range expectedComments {
		found := false
		for _, comment := range configMapComments {
			if strings.Contains(comment, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected comment '%s' not found in ConfigMap. Found comments: %v", expected, configMapComments)
		}
	}

	// Check for service comments
	serviceComments := extractAllComments(service.Node)
	serviceExpectedComments := []string{
		"Second resource comment",
		"Service metadata comment",
		"Service type comment",
		"HTTP port comment",
		"Selector comment",
	}

	for _, expected := range serviceExpectedComments {
		found := false
		for _, comment := range serviceComments {
			if strings.Contains(comment, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected comment '%s' not found in Service. Found comments: %v", expected, serviceComments)
		}
	}
}

// TestResourceOrderPreservation tests that resource order is maintained
func TestResourceOrderPreservation(t *testing.T) {
	testYAML := `apiVersion: v1
kind: Namespace
metadata:
  name: first-resource
---
apiVersion: apps/v1
kind: Deployment  
metadata:
  name: second-resource
spec:
  replicas: "1"
---
apiVersion: v1
kind: Service
metadata:
  name: third-resource
spec:
  type: ClusterIP
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: fourth-resource
data:
  key: value`

	// Load with structure preservation
	docSet, err := LoadResourcesWithStructure(strings.NewReader(testYAML))
	if err != nil {
		t.Fatalf("Failed to load YAML with structure: %v", err)
	}

	// Verify we have the expected number of resources
	if len(docSet.Documents) != 4 {
		t.Fatalf("Expected 4 documents, got %d", len(docSet.Documents))
	}

	// Verify the order is preserved
	expectedOrder := []struct {
		kind string
		name string
	}{
		{"Namespace", "first-resource"},
		{"Deployment", "second-resource"},
		{"Service", "third-resource"},
		{"ConfigMap", "fourth-resource"},
	}

	for i, expected := range expectedOrder {
		doc := docSet.Documents[i]
		if doc.Resource.GetKind() != expected.kind {
			t.Errorf("Position %d: expected kind %s, got %s", i, expected.kind, doc.Resource.GetKind())
		}
		if doc.Resource.GetName() != expected.name {
			t.Errorf("Position %d: expected name %s, got %s", i, expected.name, doc.Resource.GetName())
		}
		if doc.Order != i {
			t.Errorf("Position %d: expected order %d, got %d", i, i, doc.Order)
		}
	}

	// Test that GetResources preserves order
	resources := docSet.GetResources()
	if len(resources) != 4 {
		t.Fatalf("GetResources returned %d resources, expected 4", len(resources))
	}

	for i, expected := range expectedOrder {
		resource := resources[i]
		if resource.GetKind() != expected.kind {
			t.Errorf("GetResources position %d: expected kind %s, got %s", i, expected.kind, resource.GetKind())
		}
		if resource.GetName() != expected.name {
			t.Errorf("GetResources position %d: expected name %s, got %s", i, expected.name, resource.GetName())
		}
	}
}

// TestPatchWithStructurePreservation tests that patching preserves structure
func TestPatchWithStructurePreservation(t *testing.T) {
	testYAML := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config  # Name comment
  labels:
    app: test        # App comment
data:
  port: "8080"       # Port comment should be preserved
  host: localhost    # Host comment
---
apiVersion: v1  
kind: Service
metadata:
  name: test-service # Service name comment
spec:
  type: ClusterIP    # Type comment
  ports:
  - name: http       # Port name comment
    port: "80"       # Port value comment
    targetPort: "8080"`

	// Load with structure preservation
	docSet, err := LoadResourcesWithStructure(strings.NewReader(testYAML))
	if err != nil {
		t.Fatalf("Failed to load YAML with structure: %v", err)
	}

	// Create a simple patch that modifies the ConfigMap port
	patchYAML := `data.port: "9090"`
	patches, err := LoadPatchFile(strings.NewReader(patchYAML))
	if err != nil {
		t.Fatalf("Failed to load patch: %v", err)
	}

	// Create patchable set with structure preservation
	patchSet, err := NewPatchableAppSetWithStructure(docSet, patches)
	if err != nil {
		t.Fatalf("Failed to create patchable set: %v", err)
	}

	// Apply patches
	resolved, err := patchSet.Resolve()
	if err != nil {
		t.Fatalf("Failed to resolve patches: %v", err)
	}

	if len(resolved) != 1 {
		t.Fatalf("Expected 1 resolved patch target, got %d", len(resolved))
	}

	// Apply the patches to the resources
	for _, r := range resolved {
		if err := r.Apply(); err != nil {
			t.Fatalf("Failed to apply patches: %v", err)
		}
	}

	// Update document set with patched resources
	for _, r := range resolved {
		doc := docSet.FindDocumentByName(r.Name)
		if doc != nil {
			doc.Resource = r.Base
			if err := doc.UpdateDocumentFromResource(); err != nil {
				t.Fatalf("Failed to update document: %v", err)
			}
		}
	}

	// Verify the patch was applied
	configMapDoc := docSet.FindDocumentByName("test-config")
	if configMapDoc == nil {
		t.Fatal("ConfigMap document not found")
	}

	// Check that the port value was updated
	// Use NestedFieldNoCopy since we might have mixed types (strings and integers)
	portValue, found, err := unstructured.NestedFieldNoCopy(configMapDoc.Resource.Object, "data", "port")
	if err != nil || !found {
		t.Fatalf("Failed to get port from patched ConfigMap: %v", err)
	}

	// The port should be converted to integer (due to type inference)
	if portInt, ok := portValue.(int64); !ok || portInt != 9090 {
		t.Errorf("Expected port to be int64 9090, got %T with value %v", portValue, portValue)
	}

	// Verify that unpatched values remain unchanged
	hostValue, found, err := unstructured.NestedFieldNoCopy(configMapDoc.Resource.Object, "data", "host")
	if err != nil || !found {
		t.Fatalf("Failed to get host from patched ConfigMap: %v", err)
	}

	if hostStr, ok := hostValue.(string); !ok || hostStr != "localhost" {
		t.Errorf("Expected host to remain 'localhost', got %T with value %v", hostValue, hostValue)
	}

	// Verify that other resources were not affected
	serviceDoc := docSet.FindDocumentByName("test-service")
	if serviceDoc == nil {
		t.Fatal("Service document not found")
	}

	if serviceDoc.Resource.GetKind() != "Service" {
		t.Errorf("Service kind changed unexpectedly to %s", serviceDoc.Resource.GetKind())
	}
}

// quotedScalarCheck names one quoted scalar in a base resource: where it sits in
// the loaded object, the string it must load as, and the text it must keep when
// the document is written back.
type quotedScalarCheck struct {
	path []any // string = map key, int = list index
	want string
	text string
}

var quotedScalarCases = []struct {
	row    string
	doc    string
	checks []quotedScalarCheck
}{
	{
		row: "T1 quoted bool in a ConfigMap",
		doc: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\ndata:\n  enabled: \"true\"\n",
		checks: []quotedScalarCheck{
			{[]any{"data", "enabled"}, "true", `enabled: "true"`},
		},
	},
	{
		row: "T2 quoted integer in a ConfigMap",
		doc: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\ndata:\n  port: \"8080\"\n",
		checks: []quotedScalarCheck{
			{[]any{"data", "port"}, "8080", `port: "8080"`},
		},
	},
	{
		row: "T3 quoted integer in an annotation",
		doc: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n  annotations:\n    prometheus.io/port: \"9102\"\n",
		checks: []quotedScalarCheck{
			{[]any{"metadata", "annotations", "prometheus.io/port"}, "9102", `prometheus.io/port: "9102"`},
		},
	},
	{
		row: "T4 quoted bools in container env values",
		doc: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\nspec:\n  containers:\n  - name: app\n    env:\n    - name: A\n      value: \"true\"\n    - name: B\n      value: \"false\"\n    - name: C\n      value: \"True\"\n",
		checks: []quotedScalarCheck{
			{[]any{"spec", "containers", 0, "env", 0, "value"}, "true", `value: "true"`},
			{[]any{"spec", "containers", 0, "env", 1, "value"}, "false", `value: "false"`},
			{[]any{"spec", "containers", 0, "env", 2, "value"}, "True", `value: "True"`},
		},
	},
	{
		row: "T5 quoted upper-case bool in a label",
		doc: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n  labels:\n    x: \"TRUE\"\n",
		checks: []quotedScalarCheck{
			{[]any{"metadata", "labels", "x"}, "TRUE", `x: "TRUE"`},
		},
	},
	{
		row: "T6 write-back keeps case and quotes",
		doc: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\ndata:\n  flag: \"True\"\n  enabled: \"true\"\n",
		checks: []quotedScalarCheck{
			{[]any{"data", "flag"}, "True", `flag: "True"`},
			{[]any{"data", "enabled"}, "true", `enabled: "true"`},
		},
	},
	{
		row: "T7 a key that merely contains an integer-field name",
		doc: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\ndata:\n  support: \"1\"\n",
		checks: []quotedScalarCheck{
			{[]any{"data", "support"}, "1", `support: "1"`},
		},
	},
	{
		row: "T8 quoted ports in a Service",
		doc: "apiVersion: v1\nkind: Service\nmetadata:\n  name: s\nspec:\n  ports:\n  - name: http\n    port: \"80\"\n    targetPort: \"8080\"\n",
		checks: []quotedScalarCheck{
			{[]any{"spec", "ports", 0, "port"}, "80", `port: "80"`},
			{[]any{"spec", "ports", 0, "targetPort"}, "8080", `targetPort: "8080"`},
		},
	},
	{
		row: "T8 quoted replicas, hostPort and selector port in a Deployment",
		doc: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\nspec:\n  replicas: \"3\"\n  selector:\n    port: \"8080\"\n  template:\n    spec:\n      containers:\n      - name: app\n        ports:\n        - hostPort: \"8080\"\n",
		checks: []quotedScalarCheck{
			{[]any{"spec", "replicas"}, "3", `replicas: "3"`},
			{[]any{"spec", "selector", "port"}, "8080", `port: "8080"`},
			{[]any{"spec", "template", "spec", "containers", 0, "ports", 0, "hostPort"}, "8080", `hostPort: "8080"`},
		},
	},
}

// valueAtPath walks a loaded object by map keys (string) and list indexes (int).
func valueAtPath(t *testing.T, obj any, path []any) any {
	t.Helper()
	cur := obj
	for _, seg := range path {
		switch s := seg.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("path %v: %T is not a map at %q", path, cur, s)
			}
			cur, ok = m[s]
			if !ok {
				t.Fatalf("path %v: key %q not found", path, s)
			}
		case int:
			l, ok := cur.([]any)
			if !ok || s >= len(l) {
				t.Fatalf("path %v: %T has no index %d", path, cur, s)
			}
			cur = l[s]
		}
	}
	return cur
}

// TestQuotedScalarsStayStrings pins that a quoted scalar in a base resource keeps
// its authored string type and its quoting in both loaders and in the written file.
func TestQuotedScalarsStayStrings(t *testing.T) {
	for _, tc := range quotedScalarCases {
		t.Run(tc.row, func(t *testing.T) {
			multi, err := LoadResourcesFromMultiYAML(strings.NewReader(tc.doc))
			if err != nil || len(multi) != 1 {
				t.Fatalf("LoadResourcesFromMultiYAML: n=%d err=%v", len(multi), err)
			}
			set, err := LoadResourcesWithStructure(strings.NewReader(tc.doc))
			if err != nil || len(set.Documents) != 1 {
				t.Fatalf("LoadResourcesWithStructure: err=%v", err)
			}
			out := filepath.Join(t.TempDir(), "out.yaml")
			if err := set.WriteToFile(out); err != nil {
				t.Fatalf("WriteToFile: %v", err)
			}
			written, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read written file: %v", err)
			}
			for _, c := range tc.checks {
				for name, obj := range map[string]map[string]any{
					"LoadResourcesFromMultiYAML": multi[0].Object,
					"LoadResourcesWithStructure": set.Documents[0].Resource.Object,
				} {
					if got := valueAtPath(t, obj, c.path); got != c.want {
						t.Errorf("%s: %v = %v (%T), want string %q", name, c.path, got, got, c.want)
					}
				}
				if !strings.Contains(string(written), c.text) {
					t.Errorf("written file lacks %q:\n%s", c.text, written)
				}
			}
		})
	}
}

// TestQuotedScalarsLoadersAgree pins that both loaders produce deeply equal
// objects for the quoted-scalar inputs.
func TestQuotedScalarsLoadersAgree(t *testing.T) {
	for _, tc := range quotedScalarCases {
		t.Run(tc.row, func(t *testing.T) {
			multi, err := LoadResourcesFromMultiYAML(strings.NewReader(tc.doc))
			if err != nil || len(multi) != 1 {
				t.Fatalf("LoadResourcesFromMultiYAML: n=%d err=%v", len(multi), err)
			}
			set, err := LoadResourcesWithStructure(strings.NewReader(tc.doc))
			if err != nil || len(set.Documents) != 1 {
				t.Fatalf("LoadResourcesWithStructure: err=%v", err)
			}
			if !reflect.DeepEqual(multi[0].Object, set.Documents[0].Resource.Object) {
				t.Errorf("loaders differ:\nmulti:     %#v\nstructure: %#v", multi[0].Object, set.Documents[0].Resource.Object)
			}
		})
	}
}

// TestCopyFunctionality tests that the Copy method works correctly
func TestCopyFunctionality(t *testing.T) {
	testYAML := `apiVersion: v1
kind: ConfigMap
metadata:
  name: original    # Original comment
data:
  key: value        # Data comment
---
apiVersion: v1
kind: Service  
metadata:
  name: service     # Service comment`

	// Load original
	original, err := LoadResourcesWithStructure(strings.NewReader(testYAML))
	if err != nil {
		t.Fatalf("Failed to load original YAML: %v", err)
	}

	// Create a copy
	copied, err := original.Copy()
	if err != nil {
		t.Fatalf("Failed to copy document set: %v", err)
	}

	// Verify they have the same number of documents
	if len(copied.Documents) != len(original.Documents) {
		t.Errorf("Copy has %d documents, original has %d", len(copied.Documents), len(original.Documents))
	}

	// Verify the copies are independent (modifying copy doesn't affect original)
	if len(copied.Documents) > 0 {
		// Modify the copy
		copied.Documents[0].Resource.SetName("modified-name")

		// Verify original is unchanged
		if original.Documents[0].Resource.GetName() == "modified-name" {
			t.Error("Modifying copy affected the original - copy is not deep")
		}

		// Verify copy was actually modified
		if copied.Documents[0].Resource.GetName() != "modified-name" {
			t.Error("Copy was not modified as expected")
		}
	}
}

// TestMergeSequenceNodes_StaleFieldRemoval verifies that when a keyed list
// item is merged, fields present in the original but absent in the patched
// version are removed (i.e. the patched node is used as the base).
func TestMergeSequenceNodes_StaleFieldRemoval(t *testing.T) {
	originalYAML := `
- name: main
  image: nginx:1.24
  resources:
    limits:
      cpu: "1"
- name: sidecar
  image: envoy:latest
`
	patchedYAML := `
- name: main
  image: nginx:1.25
`

	var origNode, patchedNode yaml.Node
	if err := yaml.Unmarshal([]byte(originalYAML), &origNode); err != nil {
		t.Fatalf("unmarshal original: %v", err)
	}
	if err := yaml.Unmarshal([]byte(patchedYAML), &patchedNode); err != nil {
		t.Fatalf("unmarshal patched: %v", err)
	}

	// Get the sequence nodes (inside document nodes)
	origSeq := origNode.Content[0]
	patchedSeq := patchedNode.Content[0]

	if err := mergeSequenceNodes(origSeq, patchedSeq); err != nil {
		t.Fatalf("mergeSequenceNodes: %v", err)
	}

	// The result should have only the "main" item (the patched set),
	// and the "main" item should NOT have "resources".
	if len(origSeq.Content) != 1 {
		t.Fatalf("expected 1 item in merged sequence, got %d", len(origSeq.Content))
	}

	// Check that "resources" is gone from the merged "main" item
	mainItem := origSeq.Content[0]
	for i := 0; i < len(mainItem.Content)-1; i += 2 {
		if mainItem.Content[i].Value == "resources" {
			t.Fatal("stale 'resources' field was not removed from merged item")
		}
	}

	// Check that "image" was updated
	for i := 0; i < len(mainItem.Content)-1; i += 2 {
		if mainItem.Content[i].Value == "image" {
			if mainItem.Content[i+1].Value != "nginx:1.25" {
				t.Errorf("expected image nginx:1.25, got %s", mainItem.Content[i+1].Value)
			}
			return
		}
	}
	t.Error("image field not found in merged item")
}

// extractAllComments recursively extracts all comments from a YAML node
func extractAllComments(node *yaml.Node) []string {
	var comments []string

	if node == nil {
		return comments
	}

	// Collect comments from this node
	if node.HeadComment != "" {
		comments = append(comments, strings.TrimSpace(node.HeadComment))
	}
	if node.LineComment != "" {
		comments = append(comments, strings.TrimSpace(node.LineComment))
	}
	if node.FootComment != "" {
		comments = append(comments, strings.TrimSpace(node.FootComment))
	}

	// Recursively collect from child nodes
	for _, child := range node.Content {
		childComments := extractAllComments(child)
		comments = append(comments, childComments...)
	}

	return comments
}
