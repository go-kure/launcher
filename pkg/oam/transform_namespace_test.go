package oam

import (
	"strings"
	"testing"
)

// TestTransform_ContextNamespacesMustBeDNS1123Labels guards go-kure/launcher#616: the
// namespace override and the Flux namespace are stamped onto metadata.namespace as given,
// so TransformWithPolicy refuses either one unless it is a DNS-1123 label, before lowering,
// with a message that names the value as a CLI reader knows it.
func TestTransform_ContextNamespacesMustBeDNS1123Labels(t *testing.T) {
	long := strings.Repeat("a", 64)
	cases := []struct {
		name string
		ctx  TransformContext
		want string
	}{
		{"dotted namespace", TransformContext{Namespace: "team.prod"}, `namespace "team.prod" is not a valid DNS-1123 label`},
		{"long namespace", TransformContext{Namespace: long}, `namespace "` + long + `" is not a valid DNS-1123 label`},
		{"dotted flux namespace", TransformContext{FluxNamespace: "flux.system"}, `flux namespace "flux.system" is not a valid DNS-1123 label`},
		{"long flux namespace", TransformContext{FluxNamespace: long}, `flux namespace "` + long + `" is not a valid DNS-1123 label`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			_, err := tr.Transform(makeApp("app", makeComponent("web", "webservice")), tc.ctx)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			want := tc.want + " (" + namespaceLabelRule + ")"
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}
