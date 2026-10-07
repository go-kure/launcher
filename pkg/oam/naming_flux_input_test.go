package oam

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// FluxInput is a trait's to set, on a namespaced ConfigMap or Secret: only a
// trait's object follows a Flux object that reads it, and a lowering rule's
// Flux object is FluxScoped instead.
func TestFluxInput_Refusals(t *testing.T) {
	secret := schema.GroupKind{Kind: "Secret"}
	for _, tt := range []struct {
		name string
		spec NameSpec
		want string
	}{
		{"an object that is no ConfigMap or Secret", NameSpec{Role: NameRoleHPA, Kind: hpaKind, Namespace: "default", FluxInput: true, Default: "web-hpa"},
			`naming: the NameSpec for role "hpa" reads a Flux namespace input of kind`},
		{"beside ClusterScoped", NameSpec{Role: NameRoleExternalSecret, Kind: secret, ClusterScoped: true, FluxInput: true, Default: "creds"},
			`naming: the NameSpec for role "external-secret" is ClusterScoped and FluxInput; a cluster-scoped object is in no namespace`},
	} {
		t.Run("on a trait's spec: "+tt.name, func(t *testing.T) {
			h := newNamingHarness(nil)
			_, err := h.trait("web", "external-secret", 0).ResolveName(tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant one containing %s", err, tt.want)
			}
			if len(h.asked) != 0 {
				t.Error("the hook was asked about a refused spec")
			}
		})
	}
	t.Run("on a lowering rule's spec", func(t *testing.T) {
		h := newLoweringHarness(nil)
		spec := NameSpec{Role: NameRoleValuesSecret, Kind: secret, Namespace: "default", FluxInput: true, Default: "chart-values"}
		_, err := h.lctx("chart").ResolveName("chart", "values", spec)
		const want = `naming: the NameSpec for role "values-secret" is FluxInput, which only a trait's is`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %s", err, want)
		}
	})
	t.Run("a claim read by an object that is no ConfigMap or Secret", func(t *testing.T) {
		h := newNamingHarness(nil)
		err := h.trait("web", "certificate", 0).ClaimFluxInputName(secret, "default", "tls", "secretName", "Deployment", "tls")
		const want = `naming: the claim of Secret "tls" reads a Flux namespace input of kind`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %s", err, want)
		}
	})
}
