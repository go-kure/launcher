package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	autoscalingv2 "k8s.io/api/autoscaling/v2"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// The non-RWX guard on webservice, worker and deployment checks only the
// component's effective `replicas` (authored, or the policy default). A
// `scaler` trait targets the same Deployment with an HPA, so the trait's
// effective maxReplicas is what actually bounds the pod count. These
// tests run the whole transformer, because the effective maxReplicas can come
// from a policy default and the component config can be wrapped by a
// decorating trait declared before the scaler: both are only visible there.

func nonRWXScalerTransformer() *oam.Transformer {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{
		"deployment":     &components.DeploymentHandler{},
		"service":        &components.ServiceHandler{},
		"serviceaccount": &components.ServiceAccountHandler{},
	}, nil)
	tr.RegisterComponentLowering(components.WebserviceRule{})
	tr.RegisterComponentLowering(components.WorkerRule{})
	tr.RegisterBuiltinTrait("topology-spread", &traits.TopologySpreadHandler{})
	tr.RegisterBuiltinTrait("pvc", &traits.PVCHandler{})
	tr.RegisterBuiltinTrait("scaler", &traits.ScalerHandler{})
	tr.RegisterBuiltinTrait("configmap", &traits.ConfigMapHandler{})
	return tr
}

// claimProps mounts one claim with the given access modes. A role kind
// (webservice, worker) describes the claim it synthesizes, named
// "<component>-data"; a pod kind generates none, so it references an existing
// claim "data" and states that claim's modes (go-kure/launcher#702).
func claimProps(kind string, modes ...string) map[string]any {
	authored := make([]any, len(modes))
	for i, m := range modes {
		authored[i] = m
	}
	vol := map[string]any{
		"name":        "data",
		"type":        "pvc",
		"mountPath":   "/data",
		"accessModes": authored,
	}
	if kind == "deployment" {
		vol["claimName"] = "data"
	} else {
		vol["size"] = "1Gi"
	}
	return map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"replicas": 1,
		"volumes":  []any{vol},
	}
}

// nonRWXClaimName is the claim the scaler refusal names for claimProps(kind).
func nonRWXClaimName(kind string) string {
	if kind == "deployment" {
		return `"data"`
	}
	return `"app-data"`
}

func scalerTrait(props map[string]any) oam.Trait {
	return oam.Trait{Type: "scaler", Properties: props}
}

func transformOne(t *testing.T, kind string, props map[string]any, policy oam.Policy, trs ...oam.Trait) error {
	t.Helper()
	_, err := transformCluster(t, kind, props, policy, trs...)
	return err
}

func transformCluster(t *testing.T, kind string, props map[string]any, policy oam.Policy, trs ...oam.Trait) (*stack.Cluster, error) {
	t.Helper()
	// apiVersion and kind are set because worker is a lowering rule: once any
	// rule is registered, the engine validates the settled document.
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "app", Type: kind, Properties: props, Traits: trs,
		}}},
	}
	return nonRWXScalerTransformer().Transform(app, oam.TransformContext{Policy: policy})
}

// generatedHPAs runs Generate on every application in the cluster and returns
// the HorizontalPodAutoscalers it emits.
func generatedHPAs(t *testing.T, cluster *stack.Cluster) []*autoscalingv2.HorizontalPodAutoscaler {
	t.Helper()
	var hpas []*autoscalingv2.HorizontalPodAutoscaler
	var walk func(node *stack.Node)
	walk = func(node *stack.Node) {
		if node == nil {
			return
		}
		if node.Bundle != nil {
			for _, a := range node.Bundle.Applications {
				objs, err := a.Generate()
				if err != nil {
					t.Fatalf("Generate: %v", err)
				}
				for _, o := range objs {
					if hpa, ok := (*o).(*autoscalingv2.HorizontalPodAutoscaler); ok {
						hpas = append(hpas, hpa)
					}
				}
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cluster.Node)
	return hpas
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
			err := transformOne(t, tc.kind, claimProps(tc.kind, tc.modes...), tc.policy, tc.traits...)
			if err == nil {
				t.Fatal("expected the build to fail: the HPA can scale past the one replica a non-RWX claim allows")
			}
			for _, want := range []string{`"scaler"`, nonRWXClaimName(tc.kind), "maxReplicas " + tc.wantMax} {
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
		{"webservice/maxReplicas-1", "webservice", claimProps("webservice", "ReadWriteOnce"), 1},
		{"worker/maxReplicas-1", "worker", claimProps("worker", "ReadWriteOnce"), 1},
		{"webservice/shareable-claim", "webservice", claimProps("webservice", "ReadWriteOnce", "ReadWriteMany"), 5},
		{"worker/no-claim", "worker", map[string]any{"image": "ghcr.io/org/app:v1"}, 5},
		{"deployment/maxReplicas-1", "deployment", claimProps("deployment", "ReadWriteOnce"), 1},
		{"deployment/shareable-claim", "deployment", claimProps("deployment", "ReadWriteOnce", "ReadWriteMany"), 5},
		{"deployment/no-claim", "deployment", map[string]any{"image": "ghcr.io/org/app:v1"}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cluster, err := transformCluster(t, tc.kind, tc.props, nil,
				scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": tc.max}))
			if err != nil {
				t.Fatalf("expected the build to succeed, got: %v", err)
			}
			// Accepted is not enough: the scaler has to emit an HPA that targets
			// this component's Deployment with the authored bounds.
			hpas := generatedHPAs(t, cluster)
			if len(hpas) != 1 {
				t.Fatalf("generated %d HPAs, want 1", len(hpas))
			}
			spec := hpas[0].Spec
			ref := spec.ScaleTargetRef
			if ref.APIVersion != "apps/v1" || ref.Kind != "Deployment" || ref.Name != "app" {
				t.Errorf("scaleTargetRef = %s %s/%s, want apps/v1 Deployment/app", ref.APIVersion, ref.Kind, ref.Name)
			}
			if spec.MinReplicas == nil || *spec.MinReplicas != 1 {
				t.Errorf("minReplicas = %v, want 1", spec.MinReplicas)
			}
			if spec.MaxReplicas != int32(tc.max) {
				t.Errorf("maxReplicas = %d, want %d", spec.MaxReplicas, tc.max)
			}
		})
	}
}

// A deployment's non-RWX guard must look past a shareable first claim: the
// claim that limits the pod count here is the second one, and the refusal has
// to name it.
func TestScaler_NonRWXClaim_DeploymentNamesLaterClaim(t *testing.T) {
	props := map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"replicas": 1,
		"volumes": []any{
			map[string]any{
				"name": "shared", "type": "pvc", "mountPath": "/shared", "claimName": "shared",
				"accessModes": []any{"ReadWriteMany"},
			},
			map[string]any{
				"name": "scratch", "type": "pvc", "mountPath": "/scratch", "claimName": "scratch",
				"accessModes": []any{"ReadWriteOnce"},
			},
		},
	}
	err := transformOne(t, "deployment", props, nil,
		scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 5}))
	if err == nil {
		t.Fatal("expected the build to fail: the second claim is ReadWriteOnce and the HPA can scale to 5")
	}
	for _, want := range []string{`"scaler"`, `volume "scratch"`, "maxReplicas 5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), `"shared"`) {
		t.Errorf("error %q names the shareable claim %q", err, "shared")
	}
}
