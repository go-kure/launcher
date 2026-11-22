package components

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/go-kure/kure/pkg/stack/helm"

	"github.com/go-kure/launcher/pkg/oam"
)

// The adapter converts the key and the content field for field, an OCI chart's
// manifest and digest included, which the HTTP chart tests cannot reach.
func TestChartContent_ConvertsAnOCIChartField_ForField(t *testing.T) {
	const url = "oci://registry.example.test/org/chart"
	stored := oam.Content{Data: []byte("archive"), Manifest: []byte("manifest"), Hosts: []string{"registry.example.test", "token.example.test"}}
	src := &fakeSource{copies: map[string]oam.Content{url: stored}}
	cc := chartContent{src: src}
	key := helm.ContentKey{Kind: "chart", URL: url, Version: "1.2.3", Digest: "sha256:abc"}

	got, ok, err := cc.Lookup(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("Lookup = %v, %v, want the copy", ok, err)
	}
	if want := (helm.Content{Data: stored.Data, Manifest: stored.Manifest, Hosts: stored.Hosts}); !reflect.DeepEqual(got, want) {
		t.Errorf("Lookup content = %+v, want %+v", got, want)
	}
	if want := (oam.ContentKey{Kind: "chart", URL: url, Version: "1.2.3", Digest: "sha256:abc"}); src.looked[0] != want {
		t.Errorf("source asked for %+v, want %+v", src.looked[0], want)
	}

	if err := cc.Store(context.Background(), key, got); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if !reflect.DeepEqual(src.stored[0], stored) || src.storedKey[0] != src.looked[0] {
		t.Errorf("Store handed %+v with key %+v, want the copy and the same key", src.stored[0], src.storedKey[0])
	}
}

// A copy with no recorded host, a missing copy and a failing Lookup are each a
// miss and none is an error, so a source never fails a render.
func TestChartContent_MissCases(t *testing.T) {
	const url = "oci://registry.example.test/org/chart"
	key := helm.ContentKey{Kind: "chart", URL: url, Version: "1.2.3"}
	for name, src := range map[string]*fakeSource{
		"no hosts":     {copies: map[string]oam.Content{url: {Data: []byte("archive")}}},
		"no copy":      {},
		"lookup error": {copies: map[string]oam.Content{url: {Data: []byte("a"), Hosts: []string{"registry.example.test"}}}, lookupErr: errors.New("down")},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok, err := chartContent{src: src}.Lookup(context.Background(), key)
			if ok || err != nil || got.Data != nil {
				t.Errorf("Lookup = %+v, %v, %v, want a miss with no error", got, ok, err)
			}
		})
	}
}
