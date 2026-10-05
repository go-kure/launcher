package components

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// panickingPolicy is a document of the given Cilium kind whose one ICMP field
// writes FIELD where its `type` belongs. With nothing there, or a null, Cilium's
// (*ICMPField).UnmarshalJSON dereferences a nil pointer: a panic inside the
// decode of a registered kind, which no code of launcher writes or checks.
func panickingPolicy(kind, field string) string {
	return "apiVersion: cilium.io/v2\nkind: " + kind + "\nmetadata:\n  name: p\n  namespace: demo\n" +
		"spec:\n  endpointSelector: {}\n  egress:\n    - icmps:\n        - fields:\n            - family: IPv4\n" + field
}

const (
	icmpTypeAbsent = ""
	icmpTypeNull   = "              type: null\n"
	icmpTypeSet    = "              type: 8\n"
)

var ciliumPolicyKinds = []string{"CiliumNetworkPolicy", "CiliumClusterwideNetworkPolicy"}

// wantDecoderPanicError fails unless err is the error a recovered decoder panic
// is turned into and holds each of wants.
func wantDecoderPanicError(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("got no error, want the decoder's panic reported as one")
	}
	if !errors.Is(err, errDecoderPanicked) {
		t.Fatalf("error is not errDecoderPanicked: %v", err)
	}
	for _, want := range append(wants, "nil pointer dereference") {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not hold %q: %v", want, err)
		}
	}
}

// TestDecodeManifestDocuments_DecoderPanicIsAnError: a document kure's parser
// panics on is a build error that names the document by its position, kind and
// name, for both Cilium kinds, and the same document with the field written
// decodes.
func TestDecodeManifestDocuments_DecoderPanicIsAnError(t *testing.T) {
	for _, kind := range ciliumPolicyKinds {
		for name, field := range map[string]string{"absent": icmpTypeAbsent, "null": icmpTypeNull} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				_, err := decodeManifestDocuments([]byte(panickingPolicy(kind, field)))
				wantDecoderPanicError(t, err, `document 1 (`+kind+` "demo/p")`)
			})
		}
		t.Run(kind+"/control", func(t *testing.T) {
			objs, err := decodeManifestDocuments([]byte(panickingPolicy(kind, icmpTypeSet)))
			if err != nil {
				t.Fatalf("the policy with its ICMP type written does not decode: %v", err)
			}
			if len(objs) != 1 || objs[0].GetObjectKind().GroupVersionKind().Kind != kind {
				t.Fatalf("decoded %v, want one %s", objs, kind)
			}
		})
	}
}

// TestDecodeManifestDocuments_DecoderPanicNamesTheDocument: the document is
// named by its place among the documents that are not empty, and a list by its
// kind, which has no name.
func TestDecodeManifestDocuments_DecoderPanicNamesTheDocument(t *testing.T) {
	configMap := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n"
	policy := panickingPolicy("CiliumNetworkPolicy", icmpTypeAbsent)
	item := "  - " + strings.ReplaceAll(strings.TrimSuffix(policy, "\n"), "\n", "\n    ") + "\n"
	cases := map[string]struct{ raw, want string }{
		"second document":             {configMap + "---\n" + policy, `document 2 (CiliumNetworkPolicy "demo/p")`},
		"empty documents not counted": {"---\n# nothing\n---\n" + configMap + "---\n---\n" + policy, `document 2 (CiliumNetworkPolicy "demo/p")`},
		"item of a List":              {"apiVersion: v1\nkind: List\nitems:\n" + item, "document 1 (List)"},
		"item of a typed list":        {"apiVersion: cilium.io/v2\nkind: CiliumNetworkPolicyList\nitems:\n" + item, "document 1 (CiliumNetworkPolicyList)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeManifestDocuments([]byte(tc.raw))
			wantDecoderPanicError(t, err, tc.want)
		})
	}
}

// TestDecodeManifestDocuments_OrdinaryErrorBeforeADecoderPanic: a document that
// does not decode, ahead of one the parser panics on, is reported with its own
// error. The parse of the whole input, which gathers every error, would panic
// on the later document; its turn comes once the first decodes.
func TestDecodeManifestDocuments_OrdinaryErrorBeforeADecoderPanic(t *testing.T) {
	bad := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\nspec:\n  replicas: many\n"
	_, err := decodeManifestDocuments([]byte(bad + "---\n" + panickingPolicy("CiliumNetworkPolicy", icmpTypeAbsent)))
	if err == nil {
		t.Fatal("got no error")
	}
	if errors.Is(err, errDecoderPanicked) {
		t.Fatalf("the first document's own error is lost to the later document's panic: %v", err)
	}
	if !strings.Contains(err.Error(), "replicas") {
		t.Errorf("error does not name the first document's field: %v", err)
	}
}

// TestDecodeManifestDocuments_FailureAheadOfMalformedYAML: input that stops being
// YAML after its first documents is read in order. A document ahead of that
// point that the parser panics on is the error, named as it is alone; one that
// does not decode, ahead of both, is reported with its own error; and input
// whose only fault is the YAML keeps the parser's error for it.
func TestDecodeManifestDocuments_FailureAheadOfMalformedYAML(t *testing.T) {
	const malformed = "---\nkey: [unclosed\n"
	bad := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\nspec:\n  replicas: many\n"
	configMap := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n"
	policy := panickingPolicy("CiliumNetworkPolicy", icmpTypeAbsent)

	// The control the cases stand on: the tail alone is not YAML to the splitter.
	if docs, err := splitManifestDocuments([]byte(configMap + malformed)); err == nil || len(docs) != 1 {
		t.Fatalf("split = %d documents, %v; want the one document ahead of the malformed YAML and an error", len(docs), err)
	}

	t.Run("a panicking document", func(t *testing.T) {
		_, err := decodeManifestDocuments([]byte(configMap + "---\n" + policy + malformed))
		wantDecoderPanicError(t, err, `document 2 (CiliumNetworkPolicy "demo/p")`)
	})
	t.Run("a document that does not decode, then a panicking one", func(t *testing.T) {
		_, err := decodeManifestDocuments([]byte(bad + "---\n" + policy + malformed))
		if err == nil {
			t.Fatal("got no error")
		}
		if errors.Is(err, errDecoderPanicked) {
			t.Fatalf("the first document's own error is lost to the later document's panic: %v", err)
		}
		if !strings.Contains(err.Error(), "replicas") {
			t.Errorf("error does not name the first document's field: %v", err)
		}
	})
	t.Run("malformed YAML alone", func(t *testing.T) {
		raw := []byte(configMap + malformed)
		_, err := decodeManifestDocuments(raw)
		_, want := parseManifests(raw, manifestParseOptions)
		if err == nil || want == nil {
			t.Fatalf("decodeManifestDocuments: %v, the parser: %v; want an error from both", err, want)
		}
		if errors.Is(err, errDecoderPanicked) || err.Error() != want.Error() {
			t.Errorf("error = %v, want the parser's own for the input: %v", err, want)
		}
	})
}

// TestRenderedAndAuthoredManifests_DecoderPanicIsAnError: the readers of
// documents launcher does not author report the panic and do not crash:
// template delivery's decode of a rendered chart, the manifests component, the
// crd component, which decodes its source before it asks for its kind, and a
// source fetched from a url.
func TestRenderedAndAuthoredManifests_DecoderPanicIsAnError(t *testing.T) {
	for _, kind := range ciliumPolicyKinds {
		doc := panickingPolicy(kind, icmpTypeAbsent)
		source := "manifest source: parse manifests: document 1 (" + kind + ` "demo/p")`
		t.Run(kind+"/rendered chart", func(t *testing.T) {
			_, err := decodeChartManifests([]byte(doc))
			wantDecoderPanicError(t, err, "decoding rendered manifests: document 1 ("+kind+` "demo/p")`)
		})
		t.Run(kind+"/manifests component", func(t *testing.T) {
			_, err := generateManifests(t, "demo", doc)
			wantDecoderPanicError(t, err, source)
		})
		t.Run(kind+"/crd component", func(t *testing.T) {
			_, err := (&CRDHandler{}).ToApplicationConfig(&oam.Component{
				Name: "c", Type: "crd", Properties: map[string]any{"inline": doc},
			}, "demo")
			wantDecoderPanicError(t, err, source)
		})
		t.Run(kind+"/url source", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(doc))
			}))
			defer srv.Close()
			_, err := (&manifestSource{url: srv.URL}).resolve()
			wantDecoderPanicError(t, err, source)
		})
	}
}

// TestPolicyObject_DecoderPanicIsAnError: the passthrough policy check decodes
// an object of a registered kind with the same parser, and reports its panic as
// an object that cannot be read.
func TestPolicyObject_DecoderPanicIsAnError(t *testing.T) {
	for _, kind := range ciliumPolicyKinds {
		t.Run(kind, func(t *testing.T) {
			var object map[string]any
			if err := yaml.Unmarshal([]byte(panickingPolicy(kind, icmpTypeAbsent)), &object); err != nil {
				t.Fatal(err)
			}
			_, err := policyObject(&unstructured.Unstructured{Object: object})
			wantDecoderPanicError(t, err, "the object cannot be read")
		})
	}
}

// TestUndeclaredFields_DecoderPanicIsAnError: the strict decode over the same
// scheme runs the same decoders, and reports the panic the same way.
func TestUndeclaredFields_DecoderPanicIsAnError(t *testing.T) {
	for _, kind := range ciliumPolicyKinds {
		t.Run(kind, func(t *testing.T) {
			doc, err := yaml.YAMLToJSON([]byte(panickingPolicy(kind, icmpTypeAbsent)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = undeclaredFields(doc)
			wantDecoderPanicError(t, err)
		})
	}
}

// TestDocumentRef: a document is named by its position, with the kind and name
// it states when it states them.
func TestDocumentRef(t *testing.T) {
	cases := map[string]struct{ doc, want string }{
		"namespaced":     {`{"kind":"ConfigMap","metadata":{"name":"c","namespace":"demo"}}`, `document 3 (ConfigMap "demo/c")`},
		"cluster-scoped": {`{"kind":"Namespace","metadata":{"name":"n"}}`, `document 3 (Namespace "n")`},
		"no name":        {`{"kind":"List","items":[]}`, "document 3 (List)"},
		"no kind":        {`{"metadata":{"name":"c"}}`, "document 3"},
		"not an object":  {`[1]`, "document 3"},
		"name no string": {`{"kind":"ConfigMap","metadata":{"name":7}}`, "document 3 (ConfigMap)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := documentRef(2, []byte(tc.doc)); got != tc.want {
				t.Errorf("documentRef = %q, want %q", got, tc.want)
			}
		})
	}
}
