package components

import (
	"fmt"
	"strings"
	"testing"

	kureio "github.com/go-kure/kure/pkg/io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// bgpPeer is a MetalLB BGPPeer document at the given version of metallb.io,
// with extra, lines of its spec, after the three fields it always sets.
func bgpPeer(version, extra string) string {
	return "apiVersion: metallb.io/" + version + "\nkind: BGPPeer\nmetadata:\n  name: peer\n  namespace: metallb-system\n" +
		"spec:\n  myASN: 64512\n  peerASN: 64513\n  peerAddress: 10.0.0.1\n" + extra
}

// decodeBGPPeer decodes doc as a manifest source or a rendered chart does and
// returns the Go type of the one object and what kure's writer writes for it.
func decodeBGPPeer(t *testing.T, doc string) (goType, written string) {
	t.Helper()
	objs, err := decodeManifestDocuments([]byte(doc))
	if err != nil {
		t.Fatalf("decodeManifestDocuments: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("decoded %d objects, want 1", len(objs))
	}
	out, err := kureio.EncodeObjectsToYAML([]*client.Object{&objs[0]})
	if err != nil {
		t.Fatalf("EncodeObjectsToYAML: %v", err)
	}
	return fmt.Sprintf("%T", objs[0]), string(out)
}

// TestDecodeManifestDocuments_BGPPeerVersions: kure's scheme registers the
// MetalLB BGPPeer at metallb.io/v1beta2, the version MetalLB stores, beside
// v1beta1 (go-kure/kure#1007). A v1beta2 document is therefore a registered
// kind's: it is decoded into its Go type and written as that type writes it,
// one whose field has the wrong type does not build, and one that sets a field
// the type does not declare is written as authored, the field kept. Before, it
// was a document of an unregistered kind, written as authored whatever it
// held. A v1beta1 document is read as it was.
func TestDecodeManifestDocuments_BGPPeerVersions(t *testing.T) {
	t.Run("v1beta2 is its Go type", func(t *testing.T) {
		goType, written := decodeBGPPeer(t, bgpPeer("v1beta2", ""))
		if goType != "*v1beta2.BGPPeer" {
			t.Errorf("decoded a %s, want the registered type *v1beta2.BGPPeer", goType)
		}
		// The type's passwordSecret is a struct, which omitempty does not leave
		// out: the typed object writes it empty where the document left it out.
		for _, want := range []string{"apiVersion: metallb.io/v1beta2\n", "  peerAddress: 10.0.0.1\n", "  passwordSecret: {}\n"} {
			if !strings.Contains(written, want) {
				t.Errorf("written object lacks %q:\n%s", want, written)
			}
		}
	})
	t.Run("v1beta2 with a field of the wrong type", func(t *testing.T) {
		_, err := decodeManifestDocuments([]byte(bgpPeer("v1beta2", "  holdTime: [1]\n")))
		if err == nil || !strings.Contains(err.Error(), "BGPPeerSpec.spec.holdTime") {
			t.Fatalf("error = %v, want the decode error naming spec.holdTime", err)
		}
	})
	t.Run("v1beta2 with an undeclared field", func(t *testing.T) {
		goType, written := decodeBGPPeer(t, bgpPeer("v1beta2", "  fieldOfALaterVersion: x\n"))
		if goType != "*unstructured.Unstructured" {
			t.Errorf("decoded a %s, want the document as written, unstructured", goType)
		}
		if !strings.Contains(written, "  fieldOfALaterVersion: x\n") || strings.Contains(written, "passwordSecret") {
			t.Errorf("want the document as written, the undeclared field kept and nothing added:\n%s", written)
		}
	})
	t.Run("v1beta1 control", func(t *testing.T) {
		goType, written := decodeBGPPeer(t, bgpPeer("v1beta1", ""))
		if goType != "*v1beta1.BGPPeer" || !strings.Contains(written, "apiVersion: metallb.io/v1beta1\n") {
			t.Errorf("decoded a %s, want *v1beta1.BGPPeer at its own version:\n%s", goType, written)
		}
	})
}

// TestPolicyObject_BGPPeerAtTheStoredVersion: the passthrough policy check
// decodes a v1beta2 BGPPeer as its kind, as it does every registered kind, so
// one that does not decode cannot be read and is refused. What passthrough
// emits stays the authored object.
func TestPolicyObject_BGPPeerAtTheStoredVersion(t *testing.T) {
	object := func(extra string) *unstructured.Unstructured {
		var o map[string]any
		if err := yaml.Unmarshal([]byte(bgpPeer("v1beta2", extra)), &o); err != nil {
			t.Fatal(err)
		}
		return &unstructured.Unstructured{Object: o}
	}
	obj, err := policyObject(object(""))
	if err != nil {
		t.Fatalf("policyObject: %v", err)
	}
	if got := fmt.Sprintf("%T", obj); got != "*v1beta2.BGPPeer" {
		t.Errorf("read as a %s, want the registered type *v1beta2.BGPPeer", got)
	}
	_, err = policyObject(object("  holdTime: [1]\n"))
	if err == nil || !strings.Contains(err.Error(), "the object cannot be read") || !strings.Contains(err.Error(), "BGPPeerSpec.spec.holdTime") {
		t.Fatalf("error = %v, want the object refused as one that cannot be read, naming spec.holdTime", err)
	}
}
