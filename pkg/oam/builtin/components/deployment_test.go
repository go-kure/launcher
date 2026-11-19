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

// The deployment kind takes the upstream appsv1.DeploymentSpec, as replicaset
// takes its ReplicaSetSpec (go-kure/launcher#1629). The reshaped form belongs to
// webservice, worker and their member type role-deployment, and its tests are
// role_deployment_*_test.go.

func TestDeploymentHandler_CanHandle(t *testing.T) {
	h := &components.DeploymentHandler{}
	if !h.CanHandle("deployment") {
		t.Error("CanHandle(deployment) = false")
	}
	for _, other := range []string{"role-deployment", "webservice", "worker", "replicaset", "statefulset", ""} {
		if h.CanHandle(other) {
			t.Errorf("CanHandle(%q) = true", other)
		}
	}
}

// dpRich is a Deployment spec that sets every top-level field and a pod spec
// with an init container, two containers and a probe.
const dpRich = `replicas: 2
minReadySeconds: 5
revisionHistoryLimit: 4
paused: true
progressDeadlineSeconds: 120
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0
selector:
  matchLabels:
    tier: web
template:
  metadata:
    labels:
      tier: web
    annotations:
      example.com/scrape: "true"
  spec:
    serviceAccountName: web
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

// dpPlain is the smallest deployment component: a selector, and a template
// whose labels it matches, running the given pod spec.
func dpPlain(podSpec string) string {
	return "selector:\n  matchLabels:\n    tier: web\ntemplate:\n  metadata:\n    labels:\n      tier: web\n  spec:\n" + htIndent(podSpec, "    ")
}

// dpGenerate generates a deployment component under the given policies and
// returns the Deployment.
func dpGenerate(t *testing.T, name string, props map[string]any, policies ...oam.Policy) *appsv1.Deployment {
	t.Helper()
	return generateCoreKindUnder(t, &components.DeploymentHandler{}, "deployment", name, props, policies...).(*appsv1.Deployment)
}

// TestDeploymentHandler_EmitsAuthoredSpec: the Deployment is named after the
// component in the build namespace, unlabelled, and its spec is the authored
// one, field for field, with one thing added: the `app` label on the pod
// template, beside the authored labels.
func TestDeploymentHandler_EmitsAuthoredSpec(t *testing.T) {
	var want appsv1.DeploymentSpec
	if err := yaml.UnmarshalStrict([]byte(dpRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	want.Template.Labels["app"] = "web"
	dep := dpGenerate(t, "web", ptObject(t, dpRich), ptStrictPolicy(), nil)
	if dep.APIVersion != "apps/v1" || dep.Kind != "Deployment" {
		t.Errorf("GVK = %s %s, want apps/v1 Deployment", dep.APIVersion, dep.Kind)
	}
	if dep.Namespace != coreKindNamespace {
		t.Errorf("namespace = %q, want the build namespace %q", dep.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(dep.Spec, want) {
		t.Errorf("spec differs from the authored one plus the app label:\n got %+v\nwant %+v", dep.Spec, want)
	}
}

// TestDeploymentHandler_FillsNothing: the kind adds no default of launcher's: a
// policy that carries replica and resource defaults fills none of them, and the
// pod keeps the API's own posture on the service account token and on the
// protocol of a port.
func TestDeploymentHandler_FillsNothing(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultReplicas: int32ptr(2), defaultStorageSize: "1Gi",
	}
	pod := htPlainPod + "    ports:\n      - containerPort: 8080\n"
	dep := dpGenerate(t, "web", ptObject(t, dpPlain(pod)), defaults)
	if dep.Spec.Replicas != nil {
		t.Errorf("replicas = %d, want it unset as authored", *dep.Spec.Replicas)
	}
	ps := dep.Spec.Template.Spec
	if res := ps.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the deployment kind fills no policy default", res)
	}
	if ps.AutomountServiceAccountToken != nil {
		t.Errorf("automountServiceAccountToken = %v, want it unset: the kind fills no value", *ps.AutomountServiceAccountToken)
	}
	if got := ps.Containers[0].Ports[0].Protocol; got != "" {
		t.Errorf("port protocol = %q, want it unset as authored", got)
	}
	if len(dep.Labels) != 0 || len(dep.Annotations) != 0 {
		t.Errorf("the Deployment carries labels %v and annotations %v, want none", dep.Labels, dep.Annotations)
	}
}

// TestDeploymentHandler_ReportsNoBuiltinDefault: no config of the kind reports a
// fill, since there is none to report.
func TestDeploymentHandler_ReportsNoBuiltinDefault(t *testing.T) {
	cfg, err := (&components.DeploymentHandler{}).ToApplicationConfig(
		&oam.Component{Name: "web", Type: "deployment", Properties: ptObject(t, dpPlain(htPlainPod))}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if r, ok := cfg.(oam.BuiltinDefaultReporter); ok {
		if got := r.BuiltinDefaults(); len(got) != 0 {
			t.Errorf("BuiltinDefaults() = %+v, want none", got)
		}
	}
}

// TestDeploymentHandler_Refusals: what the component refuses when it is read,
// with no policy involved: the reshaped form (the main container's fields at the
// top level, `sidecars`, `podMetadata`), a key that is no DeploymentSpec field
// at any depth, an unauthored selector or template, and what the pod kind
// refuses of a pod spec, by its path under the template.
func TestDeploymentHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a apps/v1 DeploymentSpec"
	app := map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}
	selector := map[string]any{"matchLabels": map[string]any{"tier": "web"}}
	with := func(extra map[string]any) map[string]any {
		spec := map[string]any{"containers": []any{app}}
		maps.Copy(spec, extra)
		return map[string]any{
			"selector": selector,
			"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"tier": "web"}}, "spec": spec},
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
		"the reshaped ports":             {top(map[string]any{"ports": []any{}}), notASpec},
		"a statefulset's field":          {top(map[string]any{"serviceName": "web"}), notASpec},
		"replicas a string":              {top(map[string]any{"replicas": "two"}), notASpec},
		"unknown key in a container":     {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3", "bogus": 1}}}), notASpec},
		"the reshaped volume mountPath":  {with(map[string]any{"volumes": []any{map[string]any{"name": "d", "emptyDir": map[string]any{}, "mountPath": "/d"}}}), notASpec},
		"the reshaped container probes":  {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3", "probes": map[string]any{}}}}), notASpec},
		"two spellings":                  {top(map[string]any{"replicas": 1, "Replicas": 2}), "sets the same field as"},
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
			err := coreKindErr(&components.DeploymentHandler{}, "deployment", "web", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestDeploymentHandler_SelectorAndTheAppLabel: a selector that matches the
// authored template labels and stops matching once the `app` label is on them is
// refused, and an authored `app` label with another value is refused too.
func TestDeploymentHandler_SelectorAndTheAppLabel(t *testing.T) {
	rules := func(op string) map[string]any {
		return map[string]any{
			"selector": map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": op}}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}}},
			},
		}
	}
	err := coreKindErr(&components.DeploymentHandler{}, "deployment", "web", rules("DoesNotExist"))
	if err == nil || !strings.Contains(err.Error(), "selector: rules out the label `app: web`") {
		t.Errorf("DoesNotExist: err = %v, want the app-label refusal", err)
	}
	if err := coreKindErr(&components.DeploymentHandler{}, "deployment", "web", rules("Exists")); err != nil {
		t.Errorf("Exists: refused: %v", err)
	}
	other := ptObject(t, dpPlain(htPlainPod))
	other["template"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)["app"] = "frontend"
	err = coreKindErr(&components.DeploymentHandler{}, "deployment", "web", other)
	if err == nil || !strings.Contains(err.Error(), "template.metadata.labels.app: \"frontend\" is not the `app` label of component \"web\"") {
		t.Errorf("another app label: err = %v, want the refusal", err)
	}
	if dep := dpGenerate(t, "web", ptObject(t, dpPlain(htPlainPod))); dep.Spec.Template.Labels["app"] != "web" {
		t.Errorf("template labels = %v, want the app label", dep.Spec.Template.Labels)
	}
}

// TestDeploymentHandler_ScalerOwnsTheCount: a component with a scaler trait is
// built without spec.replicas, whichever count is authored, and the policy's
// replica maximum is not applied to it (the scaler holds its own maxReplicas to
// the same maximum). Without a scaler the count is kept and held to the maximum
// (go-kure/launcher#1179).
func TestDeploymentHandler_ScalerOwnsTheCount(t *testing.T) {
	scaler := oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 1, "maxReplicas": 2}}
	build := func(t *testing.T, traits ...oam.Trait) (*appsv1.Deployment, error) {
		t.Helper()
		props := ptObject(t, dpPlain(htPlainPod))
		props["replicas"] = 10
		cfg, err := (&components.DeploymentHandler{}).ToApplicationConfig(
			&oam.Component{Name: "web", Type: "deployment", Properties: props, Traits: traits}, coreKindNamespace)
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if err := cfg.(oam.Enforceable).ApplyPolicy(ptStrictPolicy()); err != nil {
			return nil, err
		}
		objs, err := cfg.Generate(stack.NewApplication("web", coreKindNamespace, cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return (*objs[0]).(*appsv1.Deployment), nil
	}

	dep, err := build(t, scaler)
	if err != nil {
		t.Fatalf("with a scaler: %v", err)
	}
	if dep.Spec.Replicas != nil {
		t.Errorf("with a scaler: replicas = %d, want it unset", *dep.Spec.Replicas)
	}
	if _, err := build(t); err == nil || !strings.Contains(err.Error(), "replicas 10 exceeds enforced maximum 3") {
		t.Errorf("without a scaler: err = %v, want the replica maximum refusal", err)
	}
}

// TestDeploymentPolicy_PodTemplate: the pod template is held to the environment
// policy for every container, the init containers included, and a violation
// names its path under the template.
func TestDeploymentPolicy_PodTemplate(t *testing.T) {
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
			_, err := pvTransform("deployment", &components.DeploymentHandler{}, ptObject(t, dpPlain(tc.pod)), pvPolicy(ptStrictPolicy()))
			htWantViolation(t, err, tc.want)
		})
	}
	// The same pod spec with nothing the policy forbids builds.
	if _, err := pvTransform("deployment", &components.DeploymentHandler{}, ptObject(t, dpPlain(htPlainPod)), pvPolicy(ptStrictPolicy())); err != nil {
		t.Errorf("a plain pod: %v", err)
	}
}

// TestDeploymentHandler_EndpointsComeFromTheFirstContainer: the endpoints are the
// ports of the first container of the template, in the authored order; the ports
// of a later container are not the component's service surface.
func TestDeploymentHandler_EndpointsComeFromTheFirstContainer(t *testing.T) {
	h := &components.DeploymentHandler{}
	eps, err := h.Endpoints(&oam.Component{Name: "web", Type: "deployment", Properties: ptObject(t, dpRich)})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 1 || eps[0].Name != "http" || eps[0].Ports[0].IntValue() != 8080 {
		t.Errorf("endpoints = %+v, want the one http:8080 of the first container", eps)
	}
	eps, err = h.Endpoints(&oam.Component{Name: "web", Type: "deployment", Properties: ptObject(t, dpPlain(htPlainPod))})
	if err != nil || len(eps) != 0 {
		t.Errorf("a component with no ports: endpoints = %+v, err = %v; want none", eps, err)
	}
	if _, err := h.Endpoints(&oam.Component{Name: "web", Type: "deployment", Properties: map[string]any{"image": "x"}}); err == nil {
		t.Error("Endpoints of a refused component returned no error")
	}
}

// TestDeploymentConfig_ServiceAccountName: the Deployment reports the account
// its pods run as to the traits that bind identity to it: the template's
// serviceAccountName, the deprecated serviceAccount where that one is unset, and
// "" for the namespace's default account. It always runs pods.
func TestDeploymentConfig_ServiceAccountName(t *testing.T) {
	for name, tc := range map[string]struct {
		podSpec string
		want    string
	}{
		"none authored":      {htPlainPod, ""},
		"serviceAccountName": {htPlainPod + "serviceAccountName: web\n", "web"},
		"deprecated alias":   {htPlainPod + "serviceAccount: legacy\n", "legacy"},
		"both":               {htPlainPod + "serviceAccountName: web\nserviceAccount: legacy\n", "web"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := (&components.DeploymentHandler{}).ToApplicationConfig(
				&oam.Component{Name: "web", Type: "deployment", Properties: ptObject(t, dpPlain(tc.podSpec))}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			namer, ok := cfg.(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("the deployment config does not implement oam.ServiceAccountNamer")
			}
			if got, runsPods := namer.ServiceAccountName(); got != tc.want || !runsPods {
				t.Errorf("ServiceAccountName() = %q, %v; want %q, true", got, runsPods, tc.want)
			}
		})
	}
}

// TestDeploymentConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing, as
// on every other config.
func TestDeploymentConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	cfg := &components.DeploymentConfig{Name: "web", Namespace: coreKindNamespace}
	if err := cfg.ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
}
