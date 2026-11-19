package components_test

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The daemonset kind takes the upstream appsv1.DaemonSetSpec, as deployment and
// replicaset take theirs (go-kure/launcher#1629). The reshaped form belongs to
// webservice, worker and their member type role-deployment, and to the kinds that
// have not moved yet.

func TestDaemonsetHandler_CanHandle(t *testing.T) {
	h := &components.DaemonsetHandler{}
	if !h.CanHandle("daemonset") {
		t.Error("CanHandle(daemonset) = false")
	}
	for _, other := range []string{"deployment", "role-deployment", "webservice", "statefulset", ""} {
		if h.CanHandle(other) {
			t.Errorf("CanHandle(%q) = true", other)
		}
	}
}

// dsRich is a DaemonSet spec that sets every top-level field and a pod spec with
// an init container, two containers and a probe.
const dsRich = `minReadySeconds: 5
revisionHistoryLimit: 4
updateStrategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0
selector:
  matchLabels:
    tier: agent
template:
  metadata:
    labels:
      tier: agent
    annotations:
      example.com/scrape: "true"
  spec:
    serviceAccountName: agent
    tolerations:
      - operator: Exists
    initContainers:
      - name: init
        image: registry.example/team/init:1.0.0
    containers:
      - name: app
        image: registry.example/team/app:1.2.3
        ports:
          - name: http
            containerPort: 8080
        readinessProbe:
          httpGet:
            path: /healthz
            port: http
          periodSeconds: 5
          timeoutSeconds: 2
          successThreshold: 1
          failureThreshold: 4
      - name: proxy
        image: registry.example/team/proxy:2.0.0
        ports:
          - name: metrics
            containerPort: 9090
`

// dsPlain is the smallest daemonset component: a selector, and a template whose
// labels it matches, running the given pod spec.
func dsPlain(podSpec string) string {
	return "selector:\n  matchLabels:\n    tier: agent\ntemplate:\n  metadata:\n    labels:\n      tier: agent\n  spec:\n" + htIndent(podSpec, "    ")
}

// dsGenerate generates a daemonset component under the given policies and
// returns the DaemonSet.
func dsGenerate(t *testing.T, name string, props map[string]any, policies ...oam.Policy) *appsv1.DaemonSet {
	t.Helper()
	return generateCoreKindUnder(t, &components.DaemonsetHandler{}, "daemonset", name, props, policies...).(*appsv1.DaemonSet)
}

// TestDaemonsetHandler_EmitsAuthoredSpec: the DaemonSet is named after the
// component in the build namespace, unlabelled, and its spec is the authored
// one, field for field, with one thing added: the `app` label on the pod
// template, beside the authored labels.
func TestDaemonsetHandler_EmitsAuthoredSpec(t *testing.T) {
	var want appsv1.DaemonSetSpec
	if err := yaml.UnmarshalStrict([]byte(dsRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	want.Template.Labels["app"] = "agent"
	ds := dsGenerate(t, "agent", ptObject(t, dsRich), ptStrictPolicy(), nil)
	if ds.APIVersion != "apps/v1" || ds.Kind != "DaemonSet" {
		t.Errorf("GVK = %s %s, want apps/v1 DaemonSet", ds.APIVersion, ds.Kind)
	}
	if ds.Name != "agent" || ds.Namespace != coreKindNamespace {
		t.Errorf("name/namespace = %q/%q, want agent/%q", ds.Name, ds.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(ds.Spec, want) {
		t.Errorf("spec differs from the authored one plus the app label:\n got %+v\nwant %+v", ds.Spec, want)
	}
}

// TestDaemonsetHandler_FillsNothing: the kind adds no default of launcher's: a
// policy that carries resource defaults fills none of them, and the pod keeps
// the API's own posture on the service account token and on the protocol of a
// port.
func TestDaemonsetHandler_FillsNothing(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultReplicas: int32ptr(2), defaultStorageSize: "1Gi",
	}
	pod := htPlainPod + "    ports:\n      - containerPort: 8080\n"
	ds := dsGenerate(t, "agent", ptObject(t, dsPlain(pod)), defaults)
	ps := ds.Spec.Template.Spec
	if res := ps.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the daemonset kind fills no policy default", res)
	}
	if ps.AutomountServiceAccountToken != nil {
		t.Errorf("automountServiceAccountToken = %v, want it unset: the kind fills no value", *ps.AutomountServiceAccountToken)
	}
	if got := ps.Containers[0].Ports[0].Protocol; got != "" {
		t.Errorf("port protocol = %q, want it unset as authored", got)
	}
	if len(ds.Labels) != 0 || len(ds.Annotations) != 0 {
		t.Errorf("the DaemonSet carries labels %v and annotations %v, want none", ds.Labels, ds.Annotations)
	}
	if ds.Spec.UpdateStrategy.Type != "" || ds.Spec.MinReadySeconds != 0 || ds.Spec.RevisionHistoryLimit != nil {
		t.Errorf("spec fields were filled: %+v", ds.Spec)
	}
}

// TestDaemonsetHandler_ReportsNoBuiltinDefault: no config of the kind reports a
// fill, since there is none to report.
func TestDaemonsetHandler_ReportsNoBuiltinDefault(t *testing.T) {
	cfg, err := (&components.DaemonsetHandler{}).ToApplicationConfig(
		&oam.Component{Name: "agent", Type: "daemonset", Properties: ptObject(t, dsPlain(htPlainPod))}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if _, ok := cfg.(oam.BuiltinDefaultReporter); ok {
		t.Error("the daemonset config reports builtin defaults, though the kind fills none")
	}
}

// TestDaemonsetHandler_Refusals: what the component refuses when it is read,
// with no policy involved: the reshaped form (the main container's fields at the
// top level, `sidecars`, `podMetadata`), a key that is no DaemonSetSpec field at
// any depth, an unauthored selector or template, and what the pod kind refuses
// of a pod spec, by its path under the template.
func TestDaemonsetHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a apps/v1 DaemonSetSpec"
	app := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	selector := map[string]any{"matchLabels": map[string]any{"tier": "agent"}}
	with := func(extra map[string]any) map[string]any {
		spec := map[string]any{"containers": []any{app}}
		maps.Copy(spec, extra)
		return map[string]any{
			"selector": selector,
			"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"tier": "agent"}}, "spec": spec},
		}
	}
	top := func(extra map[string]any) map[string]any {
		props := with(nil)
		maps.Copy(props, extra)
		return props
	}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":                  {nil, "selector: required"},
		"no selector":                    {map[string]any{"template": with(nil)["template"]}, "selector: required"},
		"no template":                    {map[string]any{"selector": selector}, "template.spec.containers: required"},
		"the reshaped image":             {top(map[string]any{"image": "registry.example/team/app:1.2.3"}), notASpec},
		"the reshaped sidecars":          {top(map[string]any{"sidecars": []any{}}), notASpec},
		"the reshaped podMetadata":       {top(map[string]any{"podMetadata": map[string]any{}}), notASpec},
		"the reshaped tolerations":       {top(map[string]any{"tolerations": []any{}}), notASpec},
		"a deployment's field":           {top(map[string]any{"replicas": 2}), notASpec},
		"a statefulset's field":          {top(map[string]any{"serviceName": "agent"}), notASpec},
		"minReadySeconds a string":       {top(map[string]any{"minReadySeconds": "five"}), notASpec},
		"unknown key in a container":     {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3", "bogus": 1}}}), notASpec},
		"the reshaped volume mountPath":  {with(map[string]any{"volumes": []any{map[string]any{"name": "d", "emptyDir": map[string]any{}, "mountPath": "/d"}}}), notASpec},
		"the reshaped container probes":  {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3", "probes": map[string]any{}}}}), notASpec},
		"two spellings":                  {top(map[string]any{"minReadySeconds": 1, "MinReadySeconds": 2}), "sets the same field as"},
		"active deadline":                {with(map[string]any{"activeDeadlineSeconds": 600}), "template.spec.activeDeadlineSeconds: not supported"},
		"ephemeral containers":           {with(map[string]any{"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "registry.example/debug:1"}}}), "template.spec.ephemeralContainers: not supported"},
		"priority":                       {with(map[string]any{"priority": 1000}), "template.spec.priority: not authorable"},
		"overhead":                       {with(map[string]any{"overhead": map[string]any{"cpu": "100m"}}), "template.spec.overhead: not authorable"},
		"untagged image":                 {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app"}}}), `template.spec.containers[0] "app": image "registry.example/team/app" rejected: no tag or digest specified`},
		"latest init image":              {with(map[string]any{"initContainers": []any{map[string]any{"name": "init", "image": "busybox:latest"}}}), `template.spec.initContainers[0] "init": image "busybox:latest" rejected: :latest tag not allowed`},
		"duplicate container names":      {with(map[string]any{"containers": []any{app, app}}), `container name "app" is also used by template.spec.containers[0]`},
		"a selector with a bad operator": {top(map[string]any{"selector": map[string]any{"matchExpressions": []any{map[string]any{"key": "tier", "operator": "Bogus"}}}}), `selector: "Bogus" is not a valid label selector operator`},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.DaemonsetHandler{}, "daemonset", "agent", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestDaemonsetHandler_ActiveDeadlineReasonNamesTheKind: the API server forbids
// template.spec.activeDeadlineSeconds on a DaemonSet, and the refusal says so.
func TestDaemonsetHandler_ActiveDeadlineReasonNamesTheKind(t *testing.T) {
	props := ptObject(t, dsPlain(htPlainPod+"activeDeadlineSeconds: 600\n"))
	err := coreKindErr(&components.DaemonsetHandler{}, "daemonset", "agent", props)
	if err == nil || !strings.Contains(err.Error(), "DaemonSet") {
		t.Fatalf("err = %v, want a refusal that names DaemonSet", err)
	}
}

// TestDaemonsetHandler_SelectorAndTheAppLabel: a selector that matches the
// authored template labels and stops matching once the `app` label is on them is
// refused, and an authored `app` label with another value is refused too.
func TestDaemonsetHandler_SelectorAndTheAppLabel(t *testing.T) {
	rules := func(op string) map[string]any {
		return map[string]any{
			"selector": map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": op}}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"tier": "agent"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}}},
			},
		}
	}
	err := coreKindErr(&components.DaemonsetHandler{}, "daemonset", "agent", rules("DoesNotExist"))
	if err == nil || !strings.Contains(err.Error(), "selector: rules out the label `app: agent`") {
		t.Errorf("DoesNotExist: err = %v, want the app-label refusal", err)
	}
	if err := coreKindErr(&components.DaemonsetHandler{}, "daemonset", "agent", rules("Exists")); err != nil {
		t.Errorf("Exists: refused: %v", err)
	}
	other := ptObject(t, dsPlain(htPlainPod))
	other["template"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)["app"] = "frontend"
	err = coreKindErr(&components.DaemonsetHandler{}, "daemonset", "agent", other)
	if err == nil || !strings.Contains(err.Error(), "template.metadata.labels.app: \"frontend\" is not the `app` label of component \"agent\"") {
		t.Errorf("another app label: err = %v, want the refusal", err)
	}
	if ds := dsGenerate(t, "agent", ptObject(t, dsPlain(htPlainPod))); ds.Spec.Template.Labels["app"] != "agent" {
		t.Errorf("template labels = %v, want the app label", ds.Spec.Template.Labels)
	}
}

// TestDaemonsetPolicy_PodTemplate: the pod template is held to the environment
// policy for every container, the init containers included, and a violation names
// its path under the template.
func TestDaemonsetPolicy_PodTemplate(t *testing.T) {
	const image = "image: registry.example/team/app:1.2.3\n"
	pods := map[string]struct {
		pod  string
		want string
	}{
		"a privileged second container": {
			"containers:\n  - name: app\n    " + image + "  - name: side\n    " + image + "    securityContext:\n      privileged: true\n",
			"template.spec.containers[1]"},
		"a privileged init container": {
			"initContainers:\n  - name: setup\n    " + image + "    securityContext:\n      privileged: true\ncontainers:\n  - name: app\n    " + image,
			"template.spec.initContainers[0]"},
		"a cpu limit over the maximum": {
			"containers:\n  - name: app\n    " + image + "    resources:\n      limits:\n        cpu: \"8\"\n",
			"template.spec.containers[0]"},
		"an image outside the allowlist": {
			"containers:\n  - name: app\n    " + image + "  - name: side\n    image: other.example/team/side:1.0.0\n",
			"template.spec.containers[1]"},
		"host networking": {
			htPlainPod + "hostNetwork: true\n",
			"template.spec"},
	}
	for name, tc := range pods {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("daemonset", &components.DaemonsetHandler{}, ptObject(t, dsPlain(tc.pod)), pvPolicy(ptStrictPolicy()))
			htWantViolation(t, err, tc.want)
		})
	}
	// The same pod spec with nothing the policy forbids builds.
	if _, err := pvTransform("daemonset", &components.DaemonsetHandler{}, ptObject(t, dsPlain(htPlainPod)), pvPolicy(ptStrictPolicy())); err != nil {
		t.Errorf("a plain pod: %v", err)
	}
}

// TestDaemonsetHandler_EndpointsComeFromTheFirstContainer: the endpoints are the
// ports of the first container of the template, in the authored order; the ports
// of a later container are not the component's service surface.
func TestDaemonsetHandler_EndpointsComeFromTheFirstContainer(t *testing.T) {
	h := &components.DaemonsetHandler{}
	eps, err := h.Endpoints(&oam.Component{Name: "agent", Type: "daemonset", Properties: ptObject(t, dsRich)})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 1 || eps[0].Name != "http" || eps[0].Ports[0].IntValue() != 8080 {
		t.Errorf("endpoints = %+v, want the one http:8080 of the first container", eps)
	}
	eps, err = h.Endpoints(&oam.Component{Name: "agent", Type: "daemonset", Properties: ptObject(t, dsPlain(htPlainPod))})
	if err != nil || len(eps) != 0 {
		t.Errorf("a component with no ports: endpoints = %+v, err = %v; want none", eps, err)
	}
	if _, err := h.Endpoints(&oam.Component{Name: "agent", Type: "daemonset", Properties: map[string]any{"image": "x"}}); err == nil {
		t.Error("Endpoints of a refused component returned no error")
	}
}

// TestDaemonsetConfig_ServiceAccountName: the DaemonSet reports the account its
// pods run as to the traits that bind identity to it: the template's
// serviceAccountName, the deprecated serviceAccount where that one is unset, and
// "" for the namespace's default account. It always runs pods.
func TestDaemonsetConfig_ServiceAccountName(t *testing.T) {
	for name, tc := range map[string]struct {
		podSpec string
		want    string
	}{
		"none authored":      {htPlainPod, ""},
		"serviceAccountName": {htPlainPod + "serviceAccountName: agent\n", "agent"},
		"deprecated alias":   {htPlainPod + "serviceAccount: legacy\n", "legacy"},
		"both":               {htPlainPod + "serviceAccountName: agent\nserviceAccount: legacy\n", "agent"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := (&components.DaemonsetHandler{}).ToApplicationConfig(
				&oam.Component{Name: "agent", Type: "daemonset", Properties: ptObject(t, dsPlain(tc.podSpec))}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			namer, ok := cfg.(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("the daemonset config does not implement oam.ServiceAccountNamer")
			}
			if got, runsPods := namer.ServiceAccountName(); got != tc.want || !runsPods {
				t.Errorf("ServiceAccountName() = %q, %v; want %q, true", got, runsPods, tc.want)
			}
		})
	}
}

// TestDaemonsetConfig_GenerateRepeatsTheParseRefusals: the config is exported, so
// a caller can build one without a parse. Generate then refuses what the parse
// refuses (a duplicate container name here, and a missing selector) instead of
// emitting a DaemonSet the API server would reject, and it does not write the
// `app` label into the config's own template labels.
func TestDaemonsetConfig_GenerateRepeatsTheParseRefusals(t *testing.T) {
	var spec appsv1.DaemonSetSpec
	if err := yaml.UnmarshalStrict([]byte(dsPlain(htPlainPod)), &spec); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	cfg := &components.DaemonsetConfig{Name: "agent", Namespace: coreKindNamespace, Spec: spec}
	if _, err := cfg.Generate(stack.NewApplication("agent", coreKindNamespace, cfg)); err != nil {
		t.Fatalf("Generate of a valid config: %v", err)
	}
	if _, ok := cfg.Spec.Template.Labels["app"]; ok {
		t.Errorf("Generate wrote the app label into the config's template labels: %v", cfg.Spec.Template.Labels)
	}

	dup := cfg.Spec.DeepCopy()
	dup.Template.Spec.Containers = append(dup.Template.Spec.Containers, dup.Template.Spec.Containers[0])
	bad := &components.DaemonsetConfig{Name: "agent", Namespace: coreKindNamespace, Spec: *dup}
	if _, err := bad.Generate(stack.NewApplication("agent", coreKindNamespace, bad)); err == nil || !strings.Contains(err.Error(), "is also used by") {
		t.Errorf("duplicate container name: err = %v, want the container-name refusal", err)
	}

	noSelector := &components.DaemonsetConfig{Name: "agent", Namespace: coreKindNamespace, Spec: *cfg.Spec.DeepCopy()}
	noSelector.Spec.Selector = nil
	if _, err := noSelector.Generate(stack.NewApplication("agent", coreKindNamespace, noSelector)); err == nil || !strings.Contains(err.Error(), "selector: required") {
		t.Errorf("no selector: err = %v, want the selector refusal", err)
	}
}

// TestDaemonsetConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing, as on
// every other config.
func TestDaemonsetConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg := &components.DaemonsetConfig{Name: "agent", Namespace: coreKindNamespace}
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
}
