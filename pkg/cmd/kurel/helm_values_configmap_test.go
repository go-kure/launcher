package kurel

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// helm valuesMode: configMap emits its values ConfigMap through a configmap
// trait on the helmrelease it lowers to (go-kure/launcher#702). These tests
// drive the whole built-in pipeline: the trait's own validation runs on the
// lowered component, its object is placed and labelled like any configmap
// trait's, and it follows the HelmRelease into the Flux namespace.

// helmValuesComponent is a helm component referencing an existing
// HelmRepository under valuesMode: configMap, with traits attached.
func helmValuesComponent(values map[string]any, traits ...oam.Trait) oam.Component {
	return oam.Component{Name: "podinfo", Type: "helm", Traits: traits, Properties: map[string]any{
		"chart":      "podinfo",
		"source":     map[string]any{"kind": "HelmRepository", "name": "podinfo"},
		"valuesMode": "configMap",
		"values":     values,
		"valuesFrom": []any{map[string]any{"kind": "Secret", "name": "creds"}},
	}}
}

// helmValuesTransform builds comp under fluxNS ("" for none) and returns every
// generated object in emission order, or the first Transform/Generate error.
func helmValuesTransform(t *testing.T, fluxNS string, comp oam.Component) ([]client.Object, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	cluster, err := newBuiltinTransformer().Transform(app, oam.TransformContext{FluxNamespace: fluxNS, Domain: kurelDomain})
	if err != nil {
		return nil, err
	}
	var objs []client.Object
	var walk func(n *stack.Node) error
	walk = func(n *stack.Node) error {
		if n == nil {
			return nil
		}
		if n.Bundle != nil {
			for _, a := range n.Bundle.Applications {
				generated, err := a.Config.Generate(a)
				if err != nil {
					return err
				}
				for _, o := range generated {
					objs = append(objs, *o)
				}
			}
		}
		for _, c := range n.Children {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	return objs, walk(cluster.Node)
}

// TestHelmValuesConfigMap_Emitted: the ConfigMap comes after the HelmRelease,
// carries the component's app label, is named for the digest of the bytes it
// stores, and is the first valuesFrom entry. Under a Flux namespace both move
// there and the release still targets the application namespace.
func TestHelmValuesConfigMap_Emitted(t *testing.T) {
	for _, fluxNS := range []string{"", fluxNSTarget} {
		objs, err := helmValuesTransform(t, fluxNS, helmValuesComponent(map[string]any{"replicaCount": 2}))
		if err != nil {
			t.Fatalf("fluxNS %q: %v", fluxNS, err)
		}
		if len(objs) != 2 {
			t.Fatalf("fluxNS %q: %d objects, want HelmRelease then ConfigMap", fluxNS, len(objs))
		}
		hr, ok := objs[0].(*helmv2.HelmRelease)
		if !ok {
			t.Fatalf("fluxNS %q: first object is %T, want the HelmRelease", fluxNS, objs[0])
		}
		cm, ok := objs[1].(*corev1.ConfigMap)
		if !ok {
			t.Fatalf("fluxNS %q: second object is %T, want the values ConfigMap", fluxNS, objs[1])
		}

		if want := map[string]string{"app": oam.ComponentLabelValue("podinfo")}; !maps.Equal(cm.Labels, want) {
			t.Errorf("fluxNS %q: ConfigMap labels %v, want %v", fluxNS, cm.Labels, want)
		}
		data := cm.Data["values.json"]
		sum := sha256.Sum256([]byte(data))
		if len(cm.Data) != 1 || cm.Name != "podinfo-values-"+hex.EncodeToString(sum[:])[:10] {
			t.Errorf("fluxNS %q: ConfigMap %s data %v, want one values.json named for its digest", fluxNS, cm.Name, cm.Data)
		}
		if hr.Spec.Values != nil {
			t.Errorf("fluxNS %q: HelmRelease still carries inline values %s", fluxNS, hr.Spec.Values.Raw)
		}
		if vf := hr.Spec.ValuesFrom; len(vf) != 2 || vf[0].Kind != "ConfigMap" || vf[0].Name != cm.Name ||
			vf[0].ValuesKey != "values.json" || vf[1].Name != "creds" {
			t.Errorf("fluxNS %q: valuesFrom %+v, want the ConfigMap then creds", fluxNS, vf)
		}

		wantNS := "default"
		if fluxNS != "" {
			wantNS = fluxNS
		}
		if hr.Namespace != wantNS || cm.Namespace != wantNS {
			t.Errorf("fluxNS %q: HelmRelease in %q, ConfigMap in %q, want both in %q", fluxNS, hr.Namespace, cm.Namespace, wantNS)
		}
		if fluxNS != "" && hr.Spec.TargetNamespace != "default" {
			t.Errorf("fluxNS %q: targetNamespace %q, want default", fluxNS, hr.Spec.TargetNamespace)
		}
	}
}

// TestHelmValuesConfigMap_TraitValidationRuns: the synthesized trait goes
// through the configmap trait's own validation, so values over the ConfigMap
// size limit are refused there rather than emitted.
func TestHelmValuesConfigMap_TraitValidationRuns(t *testing.T) {
	big := map[string]any{"blob": strings.Repeat("x", 1<<20)}
	_, err := helmValuesTransform(t, "", helmValuesComponent(big))
	if err == nil || !strings.Contains(err.Error(), "configmap trait") || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized values: err = %v, want the configmap trait's size refusal", err)
	}
}
