package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The non-RWX guard on webservice, worker and deployment reads only the
// authored `replicas`. A `scaler` trait targets the same Deployment with an HPA, so the
// trait's effective maxReplicas is what actually bounds the pod count. These
// tests run the whole transformer, because the effective maxReplicas can come
// from a policy default and the component config can be wrapped by a
// decorating trait declared before the scaler: both are only visible there.

func nonRWXScalerTransformer() *oam.Transformer {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"webservice": &components.WebserviceHandler{},
		"worker":     &components.WorkerHandler{},
		"deployment": &components.DeploymentHandler{},
	}, nil)
	tr.RegisterBuiltinTrait("scaler", &traits.ScalerHandler{})
	tr.RegisterBuiltinTrait("configmap", &traits.ConfigMapHandler{})
	return tr
}

func claimProps(modes ...string) map[string]any {
	authored := make([]any, len(modes))
	for i, m := range modes {
		authored[i] = m
	}
	return map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"replicas": 1,
		"volumes": []any{
			map[string]any{
				"name":        "data",
				"type":        "pvc",
				"mountPath":   "/data",
				"size":        "1Gi",
				"accessModes": authored,
			},
		},
	}
}

func scalerTrait(props map[string]any) oam.Trait {
	return oam.Trait{Type: "scaler", Properties: props}
}

func transformOne(t *testing.T, kind string, props map[string]any, policy oam.Policy, trs ...oam.Trait) error {
	t.Helper()
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "app", Type: kind, Properties: props, Traits: trs,
		}}},
	}
	_, err := nonRWXScalerTransformer().Transform(app, oam.TransformContext{Policy: policy})
	return err
}

func TestScaler_NonRWXClaim_RejectsMaxReplicasAboveOne(t *testing.T) {
	configmapMount := oam.Trait{Type: "configmap", Properties: map[string]any{
		"name": "app-config", "mountPath": "/etc/config", "data": map[string]any{"k": "v"},
	}}
	cases := []struct {
		name    string
		kind    string
		modes   []string
		policy  oam.Policy
		traits  []oam.Trait
		wantMax string
	}{
		{"webservice/authored", "webservice", []string{"ReadWriteOnce"}, nil,
			[]oam.Trait{scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 5})}, "5"},
		{"worker/authored", "worker", []string{"ReadWriteOnce"}, nil,
			[]oam.Trait{scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 5})}, "5"},
		{"webservice/ReadWriteOncePod", "webservice", []string{"ReadWriteOncePod"}, nil,
			[]oam.Trait{scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 2})}, "2"},
		{"worker/ReadWriteOnce+ReadOnlyMany", "worker", []string{"ReadWriteOnce", "ReadOnlyMany"}, nil,
			[]oam.Trait{scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 3})}, "3"},
		{"webservice/policy-default-max", "webservice", []string{"ReadWriteOnce"},
			&stubScalerPolicy{defaultScalerMax: int32ptr32(4)},
			[]oam.Trait{scalerTrait(map[string]any{"minReplicas": 1})}, "4"},
		{"worker/behind-decorator", "worker", []string{"ReadWriteOnce"}, nil,
			[]oam.Trait{configmapMount, scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 5})}, "5"},
		{"deployment/authored", "deployment", []string{"ReadWriteOnce"}, nil,
			[]oam.Trait{scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 5})}, "5"},
		{"deployment/policy-default-max", "deployment", []string{"ReadWriteOnce"},
			&stubScalerPolicy{defaultScalerMax: int32ptr32(4)},
			[]oam.Trait{scalerTrait(map[string]any{"minReplicas": 1})}, "4"},
		{"deployment/behind-decorator", "deployment", []string{"ReadWriteOnce"}, nil,
			[]oam.Trait{configmapMount, scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 5})}, "5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := transformOne(t, tc.kind, claimProps(tc.modes...), tc.policy, tc.traits...)
			if err == nil {
				t.Fatal("expected the build to fail: the HPA can scale past the one replica a non-RWX claim allows")
			}
			for _, want := range []string{`"scaler"`, `"data"`, "maxReplicas " + tc.wantMax} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %s", err, want)
				}
			}
		})
	}
}

func TestScaler_NonRWXClaim_AcceptsSafeCombinations(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		props map[string]any
		max   int
	}{
		{"webservice/maxReplicas-1", "webservice", claimProps("ReadWriteOnce"), 1},
		{"worker/maxReplicas-1", "worker", claimProps("ReadWriteOnce"), 1},
		{"webservice/shareable-claim", "webservice", claimProps("ReadWriteOnce", "ReadWriteMany"), 5},
		{"worker/no-claim", "worker", map[string]any{"image": "ghcr.io/org/app:v1"}, 5},
		{"deployment/maxReplicas-1", "deployment", claimProps("ReadWriteOnce"), 1},
		{"deployment/shareable-claim", "deployment", claimProps("ReadWriteOnce", "ReadWriteMany"), 5},
		{"deployment/no-claim", "deployment", map[string]any{"image": "ghcr.io/org/app:v1"}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := transformOne(t, tc.kind, tc.props, nil,
				scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": tc.max}))
			if err != nil {
				t.Fatalf("expected the build to succeed, got: %v", err)
			}
		})
	}
}
