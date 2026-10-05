package components

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kureio "github.com/go-kure/kure/pkg/io"
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

// wantDecoderPanicReported fails unless err is the parser's report of a decoder
// panic (go-kure/kure#1009) and holds each of wants.
func wantDecoderPanicReported(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("got no error, want the decoder's panic reported as one")
	}
	for _, want := range append(wants, "the decoder panicked on", "nil pointer dereference") {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not hold %q: %v", want, err)
		}
	}
}

// TestDecodeManifestDocuments_DecoderPanicIsAnError: a document the decoder of
// its kind panics on is a build error that names the object by its kind and
// name, for both Cilium kinds, and the same document with the field written
// decodes.
func TestDecodeManifestDocuments_DecoderPanicIsAnError(t *testing.T) {
	for _, kind := range ciliumPolicyKinds {
		for name, field := range map[string]string{"absent": icmpTypeAbsent, "null": icmpTypeNull} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				_, err := decodeManifestDocuments([]byte(panickingPolicy(kind, field)))
				wantDecoderPanicReported(t, err, kind+` "demo/p"`)
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

// TestDecodeManifestDocuments_DecoderPanicNamesTheObject: the error names the
// object wherever it sits in the input, and an item of a list by its position
// in the list as well.
func TestDecodeManifestDocuments_DecoderPanicNamesTheObject(t *testing.T) {
	configMap := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n"
	policy := panickingPolicy("CiliumNetworkPolicy", icmpTypeAbsent)
	item := "  - " + strings.ReplaceAll(strings.TrimSuffix(policy, "\n"), "\n", "\n    ") + "\n"
	const object = `CiliumNetworkPolicy "demo/p"`
	cases := map[string]struct {
		raw   string
		wants []string
	}{
		"second document":      {configMap + "---\n" + policy, []string{object}},
		"item of a List":       {"apiVersion: v1\nkind: List\nitems:\n" + item, []string{"item 0 of List", object}},
		"item of a typed list": {"apiVersion: cilium.io/v2\nkind: CiliumNetworkPolicyList\nitems:\n" + item, []string{"item 0 of CiliumNetworkPolicyList", object}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeManifestDocuments([]byte(tc.raw))
			wantDecoderPanicReported(t, err, tc.wants...)
		})
	}
}

// TestDecodeManifestDocuments_DecoderPanicBesideOtherErrors: a document the
// decoder panics on is one bad document among the others. The error holds every
// document's own, in the order of the input, whichever comes first.
func TestDecodeManifestDocuments_DecoderPanicBesideOtherErrors(t *testing.T) {
	bad := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\nspec:\n  replicas: many\n"
	policy := panickingPolicy("CiliumNetworkPolicy", icmpTypeAbsent)
	for name, raw := range map[string]string{
		"a document that does not decode first": bad + "---\n" + policy,
		"the panicking document first":          policy + "---\n" + bad,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeManifestDocuments([]byte(raw))
			wantDecoderPanicReported(t, err, "replicas", `CiliumNetworkPolicy "demo/p"`)
		})
	}
}

// TestDecodeManifestDocuments_FailureAheadOfMalformedYAML: input that stops being
// YAML after its first documents is an error that holds the errors of the
// documents ahead of that point beside the YAML error; input whose only fault is
// the YAML keeps the parser's error for it.
func TestDecodeManifestDocuments_FailureAheadOfMalformedYAML(t *testing.T) {
	const malformed = "---\nkey: [unclosed\n"
	const yamlError = "error converting YAML to JSON"
	bad := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\nspec:\n  replicas: many\n"
	configMap := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n"
	policy := panickingPolicy("CiliumNetworkPolicy", icmpTypeAbsent)

	// The control the cases stand on: the tail alone is not YAML to the splitter.
	if docs, err := splitManifestDocuments([]byte(configMap + malformed)); err == nil || len(docs) != 1 {
		t.Fatalf("split = %d documents, %v; want the one document ahead of the malformed YAML and an error", len(docs), err)
	}

	t.Run("a panicking document", func(t *testing.T) {
		_, err := decodeManifestDocuments([]byte(configMap + "---\n" + policy + malformed))
		wantDecoderPanicReported(t, err, `CiliumNetworkPolicy "demo/p"`, yamlError)
	})
	t.Run("a document that does not decode, then a panicking one", func(t *testing.T) {
		_, err := decodeManifestDocuments([]byte(bad + "---\n" + policy + malformed))
		wantDecoderPanicReported(t, err, "replicas", `CiliumNetworkPolicy "demo/p"`, yamlError)
	})
	t.Run("malformed YAML alone", func(t *testing.T) {
		raw := []byte(configMap + malformed)
		_, err := decodeManifestDocuments(raw)
		_, want := kureio.ParseYAMLWithOptions(raw, manifestParseOptions)
		if err == nil || want == nil {
			t.Fatalf("decodeManifestDocuments: %v, the parser: %v; want an error from both", err, want)
		}
		if err.Error() != want.Error() {
			t.Errorf("error = %v, want the parser's own for the input: %v", err, want)
		}
	})
}

// TestDecodeManifestDocuments_MalformedJSONIsAnError: malformed JSON that the
// decoder cannot read past is a build error. The parser returned the decoder's
// error for it on every further call and never the end of the input, so the
// decode did not return (go-kure/kure#1012): alone, after one JSON document,
// and after two, from where the decoder reads JSON only. Malformed JSON behind
// a separator line was an error before and is one still.
func TestDecodeManifestDocuments_MalformedJSONIsAnError(t *testing.T) {
	const configMap = `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"c"}}` + "\n"
	for name, raw := range map[string]string{
		"alone":                    "{]",
		"after one JSON document":  configMap + "{]",
		"after two JSON documents": configMap + configMap + "{]",
		"behind a separator line":  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n---\n{]\n",
	} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				_, err := decodeManifestDocuments([]byte(raw))
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("got no error, want the malformed JSON reported")
				}
				if !strings.Contains(err.Error(), "failed to decode document") {
					t.Errorf("error is not the parser's for a document it cannot decode: %v", err)
				}
			case <-time.After(10 * time.Second):
				// A parse that reads on gathers one more error on every turn. It
				// would grow for as long as the other tests run, so the test
				// binary ends here.
				panic("decodeManifestDocuments did not return on " + name)
			}
		})
	}
	t.Run("manifests component", func(t *testing.T) {
		_, err := generateManifests(t, "demo", configMap+"{]")
		if err == nil || !strings.Contains(err.Error(), "manifest source: parse manifests: ") {
			t.Fatalf("error = %v, want the manifest source's parse error", err)
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
		const source = "manifest source: parse manifests: "
		object := kind + ` "demo/p"`
		t.Run(kind+"/rendered chart", func(t *testing.T) {
			_, err := decodeChartManifests([]byte(doc))
			wantDecoderPanicReported(t, err, "decoding rendered manifests: ", object)
		})
		t.Run(kind+"/manifests component", func(t *testing.T) {
			_, err := generateManifests(t, "demo", doc)
			wantDecoderPanicReported(t, err, source, object)
		})
		t.Run(kind+"/crd component", func(t *testing.T) {
			_, err := (&CRDHandler{}).ToApplicationConfig(&oam.Component{
				Name: "c", Type: "crd", Properties: map[string]any{"inline": doc},
			}, "demo")
			wantDecoderPanicReported(t, err, source, object)
		})
		t.Run(kind+"/url source", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(doc))
			}))
			defer srv.Close()
			_, err := (&manifestSource{url: srv.URL}).resolve()
			wantDecoderPanicReported(t, err, source, object)
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
			wantDecoderPanicReported(t, err, "the object cannot be read", kind+` "demo/p"`)
		})
	}
}

// TestUndeclaredFields_DecoderPanicIsAnError: the strict decode runs the same
// decoders over the same scheme, called by launcher itself, and does not crash
// on such a document either: the panic comes back as errDecoderPanicked. Every
// reader parses a document first, so a build shows the parser's error for it;
// this is the strict decode asked directly.
func TestUndeclaredFields_DecoderPanicIsAnError(t *testing.T) {
	for _, kind := range ciliumPolicyKinds {
		t.Run(kind, func(t *testing.T) {
			doc, err := yaml.YAMLToJSON([]byte(panickingPolicy(kind, icmpTypeAbsent)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = undeclaredFields(doc)
			if !errors.Is(err, errDecoderPanicked) {
				t.Fatalf("error is not errDecoderPanicked: %v", err)
			}
			if !strings.Contains(err.Error(), "nil pointer dereference") {
				t.Errorf("error does not hold the panic's value: %v", err)
			}
		})
	}
}
