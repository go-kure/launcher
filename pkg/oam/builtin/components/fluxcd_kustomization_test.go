package components_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// kzSourceRef is the smallest valid sourceRef: an existing OCIRepository.
func kzSourceRef() map[string]any {
	return map[string]any{"kind": "OCIRepository", "name": "manifests"}
}

func kzConfig(t *testing.T, name string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := (&components.FluxcdKustomizationHandler{}).ToApplicationConfig(
		&oam.Component{Name: name, Type: "fluxcd-kustomization", Properties: props}, "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

// kzGenerate renders cfg, under fluxNS when non-empty, and returns its one
// object, the Kustomization.
func kzGenerate(t *testing.T, cfg stack.ApplicationConfig, fluxNS string) *kustv1.Kustomization {
	t.Helper()
	if fluxNS != "" {
		cfg.(interface{ SetFluxNamespace(string) }).SetFluxNamespace(fluxNS)
	}
	objs, err := cfg.Generate(stack.NewApplication("x", "demo", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate emitted %d objects, want exactly the Kustomization", len(objs))
	}
	kz, ok := (*objs[0]).(*kustv1.Kustomization)
	if !ok {
		t.Fatalf("Generate emitted %T, want a Kustomization", *objs[0])
	}
	return kz
}

func TestFluxcdKustomizationHandler_CanHandle(t *testing.T) {
	h := &components.FluxcdKustomizationHandler{}
	if !h.CanHandle("fluxcd-kustomization") || h.CanHandle("kustomization") || h.CanHandle("oci") {
		t.Error("CanHandle must accept fluxcd-kustomization only")
	}
}

// TestFluxcdKustomizationHandler_SchemaMatchesSpec ties the published schema to
// the struct: exactly KustomizationSpec's top-level JSON keys, each with the
// property type its Go field encodes as. An upstream field added or removed on
// a kustomize-controller bump turns this red.
func TestFluxcdKustomizationHandler_SchemaMatchesSpec(t *testing.T) {
	schema := (&components.FluxcdKustomizationHandler{}).PropertySchema()
	want := map[string]oam.PropertyType{}
	durationType := reflect.TypeFor[metav1.Duration]()
	for f := range reflect.TypeFor[kustv1.KustomizationSpec]().Fields() {
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		var pt oam.PropertyType
		switch {
		case ft == durationType, ft.Kind() == reflect.String:
			pt = oam.PropertyTypeString
		case ft.Kind() == reflect.Bool:
			pt = oam.PropertyTypeBoolean
		case ft.Kind() == reflect.Slice:
			pt = oam.PropertyTypeArray
		case ft.Kind() == reflect.Struct:
			pt = oam.PropertyTypeObject
		default:
			t.Fatalf("field %s: unmapped kind %s", f.Name, ft.Kind())
		}
		want[key] = pt
	}
	got := map[string]oam.PropertyType{}
	for k, s := range schema {
		got[k] = s.Type
		if s.Description == "" {
			t.Errorf("property %q has no description", k)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("schema keys/types = %v\nwant %v", got, want)
	}
}

// TestFluxcdKustomizationHandler_EveryFieldReachable: no KustomizationSpec field
// is unreachable through the strict decode. The exclusion list is explicit and
// must stay empty.
func TestFluxcdKustomizationHandler_EveryFieldReachable(t *testing.T) {
	excluded := []string{}
	got := builtin.UnreachableJSONFields(reflect.TypeFor[kustv1.KustomizationSpec]())
	if !slices.Equal(got, excluded) {
		t.Errorf("unreachable KustomizationSpec fields: %v, want %v", got, excluded)
	}
}

// fullKustomizationProps sets every top-level KustomizationSpec key, with
// durations in the canonical form metav1.Duration re-encodes to, so the emitted
// spec can be compared with the authored map key for key.
const fullKustomizationProps = `
commonMetadata:
  labels: {team: web}
  annotations: {owner: platform}
dependsOn:
  - {name: db, namespace: data}
  - {name: cache, readyExpr: "dep.status.ready == true"}
decryption:
  provider: sops
  serviceAccountName: kms
  secretRef: {name: sops-keys}
interval: 10m0s
retryInterval: 2m0s
kubeConfig:
  secretRef: {name: remote, key: value}
path: ./deploy/production
postBuild:
  substituteStrategy: Always
  substitute: {cluster_name: prod, region: eu}
  substituteFrom:
    - {kind: ConfigMap, name: cluster-vars}
    - {kind: Secret, name: cluster-secrets, optional: true}
prune: true
deletionPolicy: Orphan
healthChecks:
  - {apiVersion: apps/v1, kind: Deployment, name: web, namespace: web}
namePrefix: prod-
nameSuffix: -eu
patches:
  - patch: '[{"op":"add","path":"/metadata/labels/x","value":"y"}]'
    target: {kind: Deployment, name: web}
  - patch: |
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: web
      spec:
        replicas: 3
images:
  - {name: ghcr.io/org/web, newName: registry.example.com/web, newTag: "1.2"}
serviceAccountName: deployer
sourceRef: {apiVersion: source.toolkit.fluxcd.io/v1, kind: OCIRepository, name: manifests, namespace: sources}
suspend: true
targetNamespace: web
timeout: 5m0s
force: true
wait: true
buildMetadata: [originAnnotations]
components: [../components/monitoring]
ignoreMissingComponents: true
healthCheckExprs:
  - {apiVersion: example.com/v1, kind: Thing, current: status.ready == true}
ignore:
  - paths: [/spec/replicas]
    target: {kind: Deployment}
`

// TestFluxcdKustomizationHandler_ProjectsEveryField: an authored map carrying
// every top-level key comes out as spec unchanged, key for key.
func TestFluxcdKustomizationHandler_ProjectsEveryField(t *testing.T) {
	props := yamlProps(t, fullKustomizationProps)
	kz := kzGenerate(t, kzConfig(t, "web", props), "")
	if got, want := jsonShape(t, kz.Spec), jsonShape(t, props); !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		wj, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("emitted spec differs from authored properties\ngot:  %s\nwant: %s", gj, wj)
	}
	if got, want := len(props), len((&components.FluxcdKustomizationHandler{}).PropertySchema()); got != want {
		t.Errorf("fixture sets %d keys; it must set every one of the %d spec keys", got, want)
	}
}

// TestFluxcdKustomizationHandler_PatchesPostBuildDependsOn is the acceptance
// line of go-kure/launcher#784: a fluxcd-kustomization with patches, postBuild
// and dependsOn renders them verbatim.
func TestFluxcdKustomizationHandler_PatchesPostBuildDependsOn(t *testing.T) {
	const authored = `
sourceRef: {kind: GitRepository, name: fleet}
path: ./apps/web
prune: true
patches:
  - patch: |
      - op: replace
        path: /spec/replicas
        value: 2
    target: {group: apps, version: v1, kind: Deployment, name: web, labelSelector: "tier=front"}
postBuild:
  substitute: {domain: example.com}
  substituteFrom:
    - {kind: Secret, name: vars, optional: true}
dependsOn:
  - {name: infra}
  - {name: db, namespace: data}
`
	props := yamlProps(t, authored)
	kz := kzGenerate(t, kzConfig(t, "web", props), "")
	for _, key := range []string{"patches", "postBuild", "dependsOn"} {
		got := jsonShape(t, kz.Spec).(map[string]any)[key]
		if want := jsonShape(t, props[key]); !reflect.DeepEqual(got, want) {
			t.Errorf("spec.%s = %v\nwant the authored %v", key, got, want)
		}
	}
	if len(kz.Spec.Patches) != 1 || kz.Spec.Patches[0].Target == nil || kz.Spec.Patches[0].Target.LabelSelector != "tier=front" {
		t.Errorf("patch target not projected: %+v", kz.Spec.Patches)
	}
	if kz.Spec.PostBuild == nil || kz.Spec.PostBuild.Substitute["domain"] != "example.com" {
		t.Errorf("postBuild not projected: %+v", kz.Spec.PostBuild)
	}
	if len(kz.Spec.DependsOn) != 2 || kz.Spec.DependsOn[1].Namespace != "data" {
		t.Errorf("dependsOn not projected: %+v", kz.Spec.DependsOn)
	}
}

func TestFluxcdKustomizationHandler_Identity(t *testing.T) {
	kz := kzGenerate(t, kzConfig(t, "web", map[string]any{"sourceRef": kzSourceRef()}), "")
	if kz.Name != "web" || kz.Namespace != "demo" {
		t.Errorf("Kustomization %s/%s, want demo/web", kz.Namespace, kz.Name)
	}
	if kz.APIVersion != "kustomize.toolkit.fluxcd.io/v1" || kz.Kind != "Kustomization" {
		t.Errorf("TypeMeta %s %s", kz.APIVersion, kz.Kind)
	}

	kz = kzGenerate(t, kzConfig(t, "web", map[string]any{"sourceRef": kzSourceRef()}), "flux-system")
	if kz.Namespace != "flux-system" {
		t.Errorf("namespace %q under a Flux namespace, want flux-system", kz.Namespace)
	}
}

// TestFluxcdKustomizationHandler_TargetNamespaceNeverDefaulted: unlike a
// HelmRelease, a Kustomization's targetNamespace overrides the namespace of
// every object it applies, so it is never defaulted, with or without a Flux
// namespace; an authored value is kept.
func TestFluxcdKustomizationHandler_TargetNamespaceNeverDefaulted(t *testing.T) {
	for _, fluxNS := range []string{"", "flux-system"} {
		kz := kzGenerate(t, kzConfig(t, "web", map[string]any{"sourceRef": kzSourceRef()}), fluxNS)
		if kz.Spec.TargetNamespace != "" {
			t.Errorf("Flux namespace %q: targetNamespace = %q, want none", fluxNS, kz.Spec.TargetNamespace)
		}
		kz = kzGenerate(t, kzConfig(t, "web", map[string]any{"sourceRef": kzSourceRef(), "targetNamespace": "elsewhere"}), fluxNS)
		if kz.Spec.TargetNamespace != "elsewhere" {
			t.Errorf("Flux namespace %q: authored targetNamespace lost: %q", fluxNS, kz.Spec.TargetNamespace)
		}
	}
}

func TestFluxcdKustomizationHandler_IntervalDefault(t *testing.T) {
	kz := kzGenerate(t, kzConfig(t, "web", map[string]any{"sourceRef": kzSourceRef()}), "")
	if kz.Spec.Interval.Duration != 60*time.Minute {
		t.Errorf("default interval = %v, want 60m", kz.Spec.Interval.Duration)
	}
	kz = kzGenerate(t, kzConfig(t, "web", map[string]any{"sourceRef": kzSourceRef(), "interval": "10m"}), "")
	if kz.Spec.Interval.Duration != 10*time.Minute {
		t.Errorf("authored interval = %v, want 10m", kz.Spec.Interval.Duration)
	}
}

// TestFluxcdKustomizationHandler_Refuses covers the acceptance line "unknown
// fields are refused with the upstream field path" and the checks the kind makes
// itself.
func TestFluxcdKustomizationHandler_Refuses(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"unknown top-level key", map[string]any{"sourceRef": kzSourceRef(), "patchs": []any{}}, `unknown field "patchs"`},
		{"unknown key in sourceRef", map[string]any{"sourceRef": map[string]any{"kind": "OCIRepository", "name": "m", "tag": "v1"}}, `unknown field "sourceRef.tag"`},
		{"unknown key in postBuild", map[string]any{"sourceRef": kzSourceRef(), "postBuild": map[string]any{"substitutes": map[string]any{}}}, `unknown field "postBuild.substitutes"`},
		{"unknown key in a patch target", map[string]any{"sourceRef": kzSourceRef(), "patches": []any{map[string]any{"patch": "x", "target": map[string]any{"kinds": "Deployment"}}}}, `unknown field "patches[0].target.kinds"`},
		{"unknown key in a later dependsOn item", map[string]any{"sourceRef": kzSourceRef(), "dependsOn": []any{map[string]any{"name": "infra"}, map[string]any{"name": "db", "kind": "Kustomization"}}}, `unknown field "dependsOn[1].kind"`},
		{"unknown key that contains a dot", map[string]any{"sourceRef": map[string]any{"kind": "OCIRepository", "name": "m", "metadata.name": "x"}}, `unknown field "sourceRef.metadata.name"`},
		{"unknown key spelled in another case", map[string]any{"SourceRef": map[string]any{"Kind": "OCIRepository", "name": "m", "Tag": "v1"}}, `unknown field "SourceRef.Tag"`},
		{"the decode names the spec type", map[string]any{"sourceRef": kzSourceRef(), "x": 1}, "fluxcd-kustomization: properties do not decode as a KustomizationSpec"},
		{"no sourceRef", map[string]any{}, "fluxcd-kustomization: sourceRef.kind is required: one of OCIRepository, GitRepository, Bucket, ExternalArtifact"},
		{"sourceRef kind not a Flux source", map[string]any{"sourceRef": map[string]any{"kind": "HelmRepository", "name": "m"}}, `fluxcd-kustomization: sourceRef.kind "HelmRepository" is not one of OCIRepository, GitRepository, Bucket, ExternalArtifact`},
		{"sourceRef kind lowercase", map[string]any{"sourceRef": map[string]any{"kind": "ocirepository", "name": "m"}}, `fluxcd-kustomization: sourceRef.kind "ocirepository" is not one of`},
		{"sourceRef name missing", map[string]any{"sourceRef": map[string]any{"kind": "OCIRepository"}}, "fluxcd-kustomization: sourceRef.name is required"},
		{"wrong type bool", map[string]any{"sourceRef": kzSourceRef(), "prune": "yes"}, "prune"},
		{"wrong type list", map[string]any{"sourceRef": kzSourceRef(), "patches": "x"}, "patches"},
		{"wrong type object", map[string]any{"sourceRef": "manifests"}, "sourceRef"},
		{"invalid interval", map[string]any{"sourceRef": kzSourceRef(), "interval": "5minutes"}, "5minutes"},
		{"signed interval", map[string]any{"sourceRef": kzSourceRef(), "interval": "-5m"}, `fluxcd-kustomization: interval "-5m" is invalid`},
		{"sub-millisecond retryInterval", map[string]any{"sourceRef": kzSourceRef(), "retryInterval": "1us"}, `fluxcd-kustomization: retryInterval "1us" is invalid`},
		{"signed timeout", map[string]any{"sourceRef": kzSourceRef(), "timeout": "-1s"}, `fluxcd-kustomization: timeout "-1s" is invalid`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&components.FluxcdKustomizationHandler{}).ToApplicationConfig(
				&oam.Component{Name: "web", Type: "fluxcd-kustomization", Properties: tc.props}, "demo")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestFluxcdKustomizationHandler_GenerateDoesNotAlias: rendering twice gives
// equal, independent output, and editing one render leaves the config untouched.
func TestFluxcdKustomizationHandler_GenerateDoesNotAlias(t *testing.T) {
	cfg := kzConfig(t, "web", map[string]any{
		"sourceRef": kzSourceRef(),
		"dependsOn": []any{map[string]any{"name": "db"}},
		"postBuild": map[string]any{"substitute": map[string]any{"a": "b"}},
	})
	kz1 := kzGenerate(t, cfg, "")
	kz1.Spec.DependsOn[0].Name = "mutated"
	kz1.Spec.PostBuild.Substitute["a"] = "mutated"
	kz2 := kzGenerate(t, cfg, "")
	if kz2.Spec.DependsOn[0].Name != "db" || kz2.Spec.PostBuild.Substitute["a"] != "b" {
		t.Errorf("second render saw the first render's edits: %+v", kz2.Spec)
	}
	spec := cfg.(*components.FluxcdKustomizationConfig).Spec
	if spec.Interval.Duration != 0 || spec.TargetNamespace != "" || spec.DependsOn[0].Name != "db" {
		t.Errorf("Generate mutated the config's spec: %+v", spec)
	}
}

// TestFluxcdKustomizationConfig_GenerateValidatesDirectConfig: a config built
// directly is checked at the emission boundary as the parse path checks it.
func TestFluxcdKustomizationConfig_GenerateValidatesDirectConfig(t *testing.T) {
	ref := kustv1.CrossNamespaceSourceReference{Kind: "OCIRepository", Name: "m"}
	cases := map[string]*components.FluxcdKustomizationConfig{
		"no sourceRef":       {Name: "web"},
		"sourceRef kind":     {Name: "web", Spec: kustv1.KustomizationSpec{SourceRef: kustv1.CrossNamespaceSourceReference{Kind: "HelmChart", Name: "m"}}},
		"sourceRef name":     {Name: "web", Spec: kustv1.KustomizationSpec{SourceRef: kustv1.CrossNamespaceSourceReference{Kind: "Bucket"}}},
		"negative interval":  {Name: "web", Spec: kustv1.KustomizationSpec{SourceRef: ref, Interval: metav1.Duration{Duration: -time.Minute}}},
		"sub-ms timeout":     {Name: "web", Spec: kustv1.KustomizationSpec{SourceRef: ref, Timeout: &metav1.Duration{Duration: time.Microsecond}}},
		"negative retryIntv": {Name: "web", Spec: kustv1.KustomizationSpec{SourceRef: ref, RetryInterval: &metav1.Duration{Duration: -time.Second}}},
	}
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := cases[n].Generate(nil); err == nil {
			t.Errorf("%s: Generate accepted an invalid config", n)
		}
	}
	valid := &components.FluxcdKustomizationConfig{Name: "web", Namespace: "demo", Spec: kustv1.KustomizationSpec{SourceRef: ref}}
	if _, err := valid.Generate(nil); err != nil {
		t.Errorf("Generate refused a valid direct config: %v", err)
	}
}

// TestFluxcdKustomizationConfig_ApplyPolicy: a Kustomization names a source by
// reference and pulls nothing itself, so no registry allowlist applies to it.
func TestFluxcdKustomizationConfig_ApplyPolicy(t *testing.T) {
	cfg := kzConfig(t, "web", map[string]any{"sourceRef": kzSourceRef()}).(*components.FluxcdKustomizationConfig)
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v", err)
	}
	if err := cfg.ApplyPolicy(fluxSrcPolicy{allowed: []string{"registry.example.com"}}); err != nil {
		t.Errorf("ApplyPolicy under an allowlist = %v, want nil: a sourceRef is a reference, not a registry", err)
	}
}

// TestFluxcdKustomizationConfig_FluxNamespaceReads: the ConfigMaps and Secrets
// the Kustomization reads by name from its own namespace.
func TestFluxcdKustomizationConfig_FluxNamespaceReads(t *testing.T) {
	cfg := &components.FluxcdKustomizationConfig{Name: "web", Namespace: "demo", Spec: kustv1.KustomizationSpec{
		SourceRef:  kustv1.CrossNamespaceSourceReference{Kind: "OCIRepository", Name: "m"},
		Decryption: &kustv1.Decryption{Provider: "sops", SecretRef: &meta.LocalObjectReference{Name: "sops-keys"}},
		KubeConfig: &meta.KubeConfigReference{
			ConfigMapRef: &meta.LocalObjectReference{Name: "remote-cm"},
			SecretRef:    &meta.SecretKeyReference{Name: "remote-secret"},
		},
		PostBuild: &kustv1.PostBuild{SubstituteFrom: []kustv1.SubstituteReference{
			{Kind: "ConfigMap", Name: "vars"},
			{Kind: "Secret", Name: "secret-vars"},
			{Kind: "Other", Name: "ignored"},
		}},
	}}
	configMaps, secrets := cfg.FluxNamespaceReads()
	if want := []string{"remote-cm", "vars"}; !slices.Equal(configMaps, want) {
		t.Errorf("ConfigMaps = %v, want %v", configMaps, want)
	}
	if want := []string{"sops-keys", "remote-secret", "secret-vars"}; !slices.Equal(secrets, want) {
		t.Errorf("Secrets = %v, want %v", secrets, want)
	}

	bare := &components.FluxcdKustomizationConfig{Name: "web", Spec: kustv1.KustomizationSpec{SourceRef: kustv1.CrossNamespaceSourceReference{Kind: "OCIRepository", Name: "m"}}}
	if cms, secs := bare.FluxNamespaceReads(); len(cms)+len(secs) != 0 {
		t.Errorf("a Kustomization naming none reads %v / %v", cms, secs)
	}
}

// TestTransform_FluxcdKustomization_FluxNamespace runs the transform pipeline
// with a Flux namespace: the Kustomization lands in the Flux namespace and no
// targetNamespace is added.
func TestTransform_FluxcdKustomization_FluxNamespace(t *testing.T) {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"fluxcd-kustomization": &components.FluxcdKustomizationHandler{}}, nil)
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "shop", Namespace: "shop"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web", Type: "fluxcd-kustomization",
			Properties: map[string]any{"sourceRef": kzSourceRef(), "path": "./deploy", "prune": true},
		}}},
	}
	cluster, err := tr.Transform(app, oam.TransformContext{FluxNamespace: "flux-system"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	var found bool
	var walk func(*stack.Node)
	walk = func(n *stack.Node) {
		if n == nil {
			return
		}
		if n.Bundle != nil {
			for _, a := range n.Bundle.Applications {
				if a.Name != "web" {
					continue
				}
				found = true
				kz := kzGenerate(t, a.Config, "")
				if kz.Namespace != "flux-system" || kz.Spec.TargetNamespace != "" || !kz.Spec.Prune || kz.Spec.Path != "./deploy" {
					t.Errorf("Kustomization in %s, spec %+v", kz.Namespace, kz.Spec)
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(cluster.Node)
	if !found {
		t.Fatal("fluxcd-kustomization application not found")
	}
}
