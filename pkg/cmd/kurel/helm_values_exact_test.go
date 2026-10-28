package kurel

import (
	"encoding/json"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestTransform_HelmReleaseValuesNumberBeyondFloat64 pins
// go-kure/launcher#1085: a helmrelease whose values hold a number a float64
// cannot hold generates, under chart and under chartRef, and the HelmRelease
// keeps the number as written. The component keeps such a value exact; the
// component label check used to read the generated HelmRelease through a
// float64 decode of its values and refuse it.
//
// The number is set on the parsed document as a json.Number, as a caller that
// decodes its document with UseNumber hands it over: a YAML document cannot
// carry it, since yaml.v3 reads a number no float64 holds as a string.
func TestTransform_HelmReleaseValuesNumberBeyondFloat64(t *testing.T) {
	for _, tc := range []struct {
		name, source string
	}{
		{name: "chart", source: `
        chart:
          spec:
            chart: podinfo
            sourceRef:
              kind: HelmRepository
              name: podinfo`},
		{name: "chartRef", source: `
        chartRef:
          kind: OCIRepository
          name: podinfo`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appYAML := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: web
      type: helmrelease
      properties:` + tc.source + "\n"
			transformer := newBuiltinTransformer()
			app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			app.Spec.Components[0].Properties["values"] = map[string]any{"n": json.Number("1e400")}
			if err := transformer.ValidateAuthoredProperties(app); err != nil {
				t.Fatalf("validating: %v", err)
			}
			cluster, err := transformer.Transform(app, oam.TransformContext{Domain: kurelDomain})
			if err != nil {
				t.Fatalf("transforming: %v", err)
			}
			apps, err := oam.GenerateApplications(cluster)
			if err != nil {
				t.Fatalf("values {n: 1e400} refused: %v", err)
			}
			hr := componentRelease(t, apps, "web")
			if hr.Spec.Values == nil {
				t.Fatal("the HelmRelease has no values")
			}
			if got := string(hr.Spec.Values.Raw); got != `{"n":1e400}` {
				t.Errorf("values = %s, want {\"n\":1e400}", got)
			}
		})
	}
}
