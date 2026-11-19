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

// The statefulset kind takes the upstream appsv1.StatefulSetSpec, as deployment,
// daemonset, job, cronjob and replicaset take theirs (go-kure/launcher#1629). The
// reshaped form belongs to webservice, worker and their member type
// role-deployment only.

func TestStatefulsetHandler_CanHandle(t *testing.T) {
	h := &components.StatefulsetHandler{}
	if !h.CanHandle("statefulset") {
		t.Error("CanHandle(statefulset) = false")
	}
	for _, other := range []string{"deployment", "daemonset", "role-deployment", "webservice", "worker", ""} {
		if h.CanHandle(other) {
			t.Errorf("CanHandle(%q) = true", other)
		}
	}
}

// stsRich is a StatefulSet spec that sets every top-level field, two claim
// templates and a pod spec with an init container, two containers and a probe.
// Its replicas and claims stay under the limits of ptStrictPolicy.
const stsRich = `replicas: 2
serviceName: db-headless
podManagementPolicy: Parallel
minReadySeconds: 5
revisionHistoryLimit: 4
updateStrategy:
  type: RollingUpdate
  rollingUpdate:
    partition: 1
    maxUnavailable: 1
persistentVolumeClaimRetentionPolicy:
  whenDeleted: Delete
  whenScaled: Retain
ordinals:
  start: 3
selector:
  matchLabels:
    tier: db
template:
  metadata:
    labels:
      tier: db
    annotations:
      example.com/scrape: "true"
  spec:
    serviceAccountName: db
    initContainers:
      - name: init
        image: registry.example/team/init:1.0.0
    containers:
      - name: app
        image: registry.example/team/db:1.2.3
        workingDir: /var/lib/db
        envFrom:
          - secretRef:
              name: db-secret
        securityContext:
          runAsUser: 999
        ports:
          - name: tcp
            containerPort: 5432
        readinessProbe:
          tcpSocket:
            port: tcp
          periodSeconds: 5
          timeoutSeconds: 2
          successThreshold: 1
          failureThreshold: 4
        volumeMounts:
          - name: data
            mountPath: /var/lib/db
      - name: proxy
        image: registry.example/team/proxy:2.0.0
        ports:
          - name: metrics
            containerPort: 9090
volumeClaimTemplates:
  - metadata:
      name: data
      labels:
        tier: db
    spec:
      accessModes:
        - ReadWriteOncePod
      storageClassName: fast
      volumeMode: Filesystem
      resources:
        requests:
          storage: 5Gi
        limits:
          storage: 8Gi
      selector:
        matchLabels:
          tier: fast
      dataSourceRef:
        apiGroup: snapshot.storage.k8s.io
        kind: VolumeSnapshot
        name: seed
      volumeAttributesClassName: gold
  - metadata:
      name: wal
    spec:
      resources:
        requests:
          storage: 1Gi
`

// stsPlain is the smallest statefulset component: a selector, and a template whose
// labels it matches, running the given pod spec.
func stsPlain(podSpec string) string {
	return "selector:\n  matchLabels:\n    tier: db\ntemplate:\n  metadata:\n    labels:\n      tier: db\n  spec:\n" + htIndent(podSpec, "    ")
}

// stsGenerate generates a statefulset component under the given policies and
// returns the StatefulSet.
func stsGenerate(t *testing.T, name string, props map[string]any, policies ...oam.Policy) *appsv1.StatefulSet {
	t.Helper()
	return generateCoreKindUnder(t, &components.StatefulsetHandler{}, "statefulset", name, props, policies...).(*appsv1.StatefulSet)
}

// stsClaim is one raw claim template named name that asks for storage.
func stsClaim(name, storage string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": name},
		"spec":     map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": storage}}},
	}
}

// stsWithClaims is the plain statefulset properties with the given claim templates.
func stsWithClaims(t *testing.T, claims ...any) map[string]any {
	t.Helper()
	props := ptObject(t, stsPlain(htPlainPod))
	props["volumeClaimTemplates"] = claims
	return props
}

// TestStatefulsetHandler_EmitsAuthoredSpec: the StatefulSet is named after the
// component in the build namespace, unlabelled, and its spec is the authored one,
// field for field, with one thing added: the `app` label on the pod template,
// beside the authored labels. Exactly one object comes out: no Service, no
// ServiceAccount and no claim beside it (go-kure/launcher#690, go-kure/launcher#702).
func TestStatefulsetHandler_EmitsAuthoredSpec(t *testing.T) {
	var want appsv1.StatefulSetSpec
	if err := yaml.UnmarshalStrict([]byte(stsRich), &want); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	want.Template.Labels["app"] = "db"
	props := ptObject(t, stsRich)
	sts := stsGenerate(t, "db", props, ptStrictPolicy(), nil)
	if sts.APIVersion != "apps/v1" || sts.Kind != "StatefulSet" {
		t.Errorf("GVK = %s %s, want apps/v1 StatefulSet", sts.APIVersion, sts.Kind)
	}
	if sts.Name != "db" || sts.Namespace != coreKindNamespace {
		t.Errorf("name/namespace = %q/%q, want db/%q", sts.Name, sts.Namespace, coreKindNamespace)
	}
	if !reflect.DeepEqual(sts.Spec, want) {
		t.Errorf("spec differs from the authored one plus the app label:\n got %+v\nwant %+v", sts.Spec, want)
	}
	// The generator never writes the authored properties: the map it was given
	// still equals a fresh decoding of the document, and a second build from
	// that same map gives the same spec.
	if !reflect.DeepEqual(props, ptObject(t, stsRich)) {
		t.Error("the build changed the authored properties")
	}
	if again := stsGenerate(t, "db", props); !reflect.DeepEqual(again.Spec, sts.Spec) {
		t.Error("two builds from one properties map differ")
	}
}

// TestStatefulsetHandler_GenerateGolden compares the emitted object with its
// whole YAML: every field the kind writes and nothing else (no resources, no
// replicas, no access mode, no port protocol, no token posture).
func TestStatefulsetHandler_GenerateGolden(t *testing.T) {
	const doc = `serviceName: db-headless
selector:
  matchLabels:
    tier: db
template:
  metadata:
    labels:
      tier: db
  spec:
    containers:
      - name: app
        image: registry.example/team/db:1.2.3
        ports:
          - containerPort: 5432
volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      storageClassName: ""
      resources:
        requests:
          storage: 5Gi
`
	const want = `apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: db
  namespace: apps
spec:
  selector:
    matchLabels:
      tier: db
  serviceName: db-headless
  template:
    metadata:
      labels:
        app: db
        tier: db
    spec:
      containers:
      - image: registry.example/team/db:1.2.3
        name: app
        ports:
        - containerPort: 5432
        resources: {}
  updateStrategy: {}
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      resources:
        requests:
          storage: 5Gi
      storageClassName: ""
    status: {}
status:
  availableReplicas: 0
  replicas: 0
`
	got, err := yaml.Marshal(stsGenerate(t, "db", ptObject(t, doc)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("emitted object:\n%s\nwant:\n%s", got, want)
	}
}

// TestStatefulsetHandler_FillsNothing: the kind adds no default of launcher's: a
// policy that carries resource, replica and storage defaults fills none of them,
// and the pod keeps the API's own posture on the service account token and on the
// protocol of a port. Nothing is added to a claim template either.
func TestStatefulsetHandler_FillsNothing(t *testing.T) {
	defaults := &stubPolicy{
		defaultCPURequest: "100m", defaultMemoryRequest: "128Mi",
		defaultCPULimit: "500m", defaultMemoryLimit: "256Mi",
		defaultReplicas: int32ptr(2), defaultStorageSize: "1Gi",
	}
	props := ptObject(t, stsPlain(htPlainPod+"    ports:\n      - containerPort: 5432\n"))
	props["volumeClaimTemplates"] = []any{map[string]any{"metadata": map[string]any{"name": "data"}}}
	sts := stsGenerate(t, "db", props, defaults)
	ps := sts.Spec.Template.Spec
	if res := ps.Containers[0].Resources; len(res.Requests) != 0 || len(res.Limits) != 0 {
		t.Errorf("resources = %+v, want none: the statefulset kind fills no policy default", res)
	}
	if ps.AutomountServiceAccountToken != nil {
		t.Errorf("automountServiceAccountToken = %v, want it unset: the kind fills no value", *ps.AutomountServiceAccountToken)
	}
	if got := ps.Containers[0].Ports[0].Protocol; got != "" {
		t.Errorf("port protocol = %q, want it unset as authored", got)
	}
	if sts.Spec.Replicas != nil {
		t.Errorf("replicas = %d, want it unset: the policy default is not applied", *sts.Spec.Replicas)
	}
	if len(sts.Labels) != 0 || len(sts.Annotations) != 0 {
		t.Errorf("the StatefulSet carries labels %v and annotations %v, want none", sts.Labels, sts.Annotations)
	}
	if sts.Spec.PodManagementPolicy != "" || sts.Spec.UpdateStrategy.Type != "" || sts.Spec.MinReadySeconds != 0 || sts.Spec.RevisionHistoryLimit != nil {
		t.Errorf("spec fields were filled: %+v", sts.Spec)
	}
	claim := sts.Spec.VolumeClaimTemplates[0]
	if len(claim.Spec.AccessModes) != 0 || len(claim.Spec.Resources.Requests) != 0 || claim.Spec.StorageClassName != nil {
		t.Errorf("the claim template was filled: %+v", claim.Spec)
	}
}

// TestStatefulsetHandler_ReportsNoBuiltinDefault: no config of the kind reports a
// fill or a policy default, since there is none to report.
func TestStatefulsetHandler_ReportsNoBuiltinDefault(t *testing.T) {
	cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db", Type: "statefulset", Properties: ptObject(t, stsPlain(htPlainPod))}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if _, ok := cfg.(oam.BuiltinDefaultReporter); ok {
		t.Error("the statefulset config reports builtin defaults, though the kind fills none")
	}
	if _, ok := cfg.(oam.PolicyDefaultReporter); ok {
		t.Error("the statefulset config reports policy defaults, though the kind applies none")
	}
}

// TestStatefulsetHandler_PropertySchema: selector and template are the required
// properties, as the API requires them, and every top-level StatefulSetSpec field
// is declared.
func TestStatefulsetHandler_PropertySchema(t *testing.T) {
	schema := (&components.StatefulsetHandler{}).PropertySchema()
	for _, name := range []string{"replicas", "selector", "template", "serviceName", "volumeClaimTemplates", "podManagementPolicy",
		"updateStrategy", "minReadySeconds", "revisionHistoryLimit", "persistentVolumeClaimRetentionPolicy", "ordinals"} {
		s, ok := schema[name]
		if !ok {
			t.Errorf("property %q is not declared", name)
			continue
		}
		if want := name == "selector" || name == "template"; s.Required != want {
			t.Errorf("property %q: Required = %v, want %v", name, s.Required, want)
		}
	}
	// The reshaped form is not declared.
	for _, name := range []string{"image", "ports", "env", "resources", "probes", "sidecars", "initContainers", "podMetadata", "volumes"} {
		if _, ok := schema[name]; ok {
			t.Errorf("the reshaped property %q is declared", name)
		}
	}
	if got := len(schema); got != 11 {
		t.Errorf("%d properties declared, want the 11 of StatefulSetSpec", got)
	}
}

// TestStatefulsetHandler_Refusals: what the component refuses when it is read,
// with no policy involved: the reshaped form (the main container's fields at the
// top level, `sidecars`, `podMetadata`, a claim template of the old shape), a key
// that is no StatefulSetSpec field at any depth, an unauthored selector or
// template, a serviceName that is no Service name, a claim template name used
// twice or by a pod volume, and what the pod kind refuses of a pod spec, by its
// path under the template.
func TestStatefulsetHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a apps/v1 StatefulSetSpec"
	app := map[string]any{"name": "app", "image": "registry.example/team/db:1.2.3"}
	selector := map[string]any{"matchLabels": map[string]any{"tier": "db"}}
	with := func(extra map[string]any) map[string]any {
		spec := map[string]any{"containers": []any{app}}
		maps.Copy(spec, extra)
		return map[string]any{
			"selector": selector,
			"template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"tier": "db"}}, "spec": spec},
		}
	}
	top := func(extra map[string]any) map[string]any {
		props := with(nil)
		maps.Copy(props, extra)
		return props
	}
	claim := func(name string) any { return stsClaim(name, "1Gi") }
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"no properties":                  {nil, "selector: required"},
		"no selector":                    {map[string]any{"template": with(nil)["template"]}, "selector: required"},
		"no template":                    {map[string]any{"selector": selector}, "template.spec.containers: required"},
		"the reshaped image":             {top(map[string]any{"image": "registry.example/team/db:1.2.3"}), notASpec},
		"the reshaped sidecars":          {top(map[string]any{"sidecars": []any{}}), notASpec},
		"the reshaped podMetadata":       {top(map[string]any{"podMetadata": map[string]any{}}), notASpec},
		"the reshaped volumes":           {top(map[string]any{"volumes": []any{}}), notASpec},
		"a deployment's field":           {top(map[string]any{"strategy": map[string]any{}}), notASpec},
		"a daemonset's field":            {top(map[string]any{"updateStrategy": map[string]any{"maxSurge": 1, "bogus": 1}}), notASpec},
		"replicas a string":              {top(map[string]any{"replicas": "two"}), notASpec},
		"unknown key in a container":     {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/db:1.2.3", "bogus": 1}}}), notASpec},
		"the reshaped volume mountPath":  {with(map[string]any{"volumes": []any{map[string]any{"name": "d", "emptyDir": map[string]any{}, "mountPath": "/d"}}}), notASpec},
		"the reshaped container probes":  {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/db:1.2.3", "probes": map[string]any{}}}}), notASpec},
		"the reshaped claim template":    {top(map[string]any{"volumeClaimTemplates": []any{map[string]any{"name": "data", "size": "1Gi", "mountPath": "/data"}}}), notASpec},
		"a claim size that is no number": {top(map[string]any{"volumeClaimTemplates": []any{stsClaim("data", "not-a-quantity")}}), notASpec},
		"an unknown claim spec key":      {top(map[string]any{"volumeClaimTemplates": []any{map[string]any{"metadata": map[string]any{"name": "data"}, "spec": map[string]any{"bogus": 1}}}}), notASpec},
		"two spellings":                  {top(map[string]any{"minReadySeconds": 1, "MinReadySeconds": 2}), "sets the same field as"},
		"active deadline":                {with(map[string]any{"activeDeadlineSeconds": 600}), "template.spec.activeDeadlineSeconds: not supported"},
		"ephemeral containers":           {with(map[string]any{"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "registry.example/debug:1"}}}), "template.spec.ephemeralContainers: not supported"},
		"priority":                       {with(map[string]any{"priority": 1000}), "template.spec.priority: not authorable"},
		"overhead":                       {with(map[string]any{"overhead": map[string]any{"cpu": "100m"}}), "template.spec.overhead: not authorable"},
		"untagged image":                 {with(map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/db"}}}), `template.spec.containers[0] "app": image "registry.example/team/db" rejected: no tag or digest specified`},
		"latest init image":              {with(map[string]any{"initContainers": []any{map[string]any{"name": "init", "image": "busybox:latest"}}}), `template.spec.initContainers[0] "init": image "busybox:latest" rejected: :latest tag not allowed`},
		"duplicate container names":      {with(map[string]any{"containers": []any{app, app}}), `container name "app" is also used by template.spec.containers[0]`},
		"a selector with a bad operator": {top(map[string]any{"selector": map[string]any{"matchExpressions": []any{map[string]any{"key": "tier", "operator": "Bogus"}}}}), `selector: "Bogus" is not a valid label selector operator`},
		"serviceName with capitals":      {top(map[string]any{"serviceName": "DB"}), "serviceName:"},
		"serviceName with a dot":         {top(map[string]any{"serviceName": "db.headless"}), "serviceName:"},
		"serviceName too long":           {top(map[string]any{"serviceName": strings.Repeat("a", 64)}), "serviceName:"},
		"a claim template used twice":    {top(map[string]any{"volumeClaimTemplates": []any{claim("data"), claim("data")}}), `volumeClaimTemplates: duplicate name "data"`},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.StatefulsetHandler{}, "statefulset", "db", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}

	t.Run("a claim named like a volume", func(t *testing.T) {
		props := with(map[string]any{"volumes": []any{map[string]any{"name": "data", "emptyDir": map[string]any{}}}})
		props["volumeClaimTemplates"] = []any{claim("data")}
		err := coreKindErr(&components.StatefulsetHandler{}, "statefulset", "db", props)
		if err == nil || !strings.Contains(err.Error(), `template.spec.volumes: volume "data" has the same name as a claim template`) {
			t.Fatalf("err = %v, want the volume and claim template collision", err)
		}
	})
	t.Run("a claim and a volume with other names", func(t *testing.T) {
		props := with(map[string]any{"volumes": []any{map[string]any{"name": "scratch", "emptyDir": map[string]any{}}}})
		props["volumeClaimTemplates"] = []any{claim("data"), claim("wal")}
		if err := coreKindErr(&components.StatefulsetHandler{}, "statefulset", "db", props); err != nil {
			t.Fatalf("refused: %v", err)
		}
	})
	t.Run("a serviceName that is a valid DNS label", func(t *testing.T) {
		for _, name := range []string{"db", "db-headless", "9db", strings.Repeat("a", 63)} {
			if err := coreKindErr(&components.StatefulsetHandler{}, "statefulset", "db", top(map[string]any{"serviceName": name})); err != nil {
				t.Errorf("serviceName %q refused: %v", name, err)
			}
		}
	})
}

// TestStatefulsetHandler_ActiveDeadlineReasonNamesTheKinds: the API server forbids
// template.spec.activeDeadlineSeconds on a StatefulSet, and the refusal says so,
// with the five kinds that forbid it.
func TestStatefulsetHandler_ActiveDeadlineReasonNamesTheKinds(t *testing.T) {
	props := ptObject(t, stsPlain(htPlainPod+"activeDeadlineSeconds: 600\n"))
	err := coreKindErr(&components.StatefulsetHandler{}, "statefulset", "db", props)
	if err == nil {
		t.Fatal("activeDeadlineSeconds on the pod template was accepted")
	}
	// The generic decode refusal names StatefulSetSpec too, so pin the refusal
	// itself: its path and its reason, which lists StatefulSet among the kinds.
	for _, want := range []string{
		"template.spec.activeDeadlineSeconds: not supported",
		"Deployment, ReplicaSet, ReplicationController, DaemonSet or StatefulSet",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want one containing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "do not decode") {
		t.Errorf("err = %v, the generic decode refusal fired instead", err)
	}
}

// TestStatefulsetHandler_SelectorAndTheAppLabel: a selector that matches the
// authored template labels and stops matching once the `app` label is on them is
// refused, and an authored `app` label with another value is refused too.
func TestStatefulsetHandler_SelectorAndTheAppLabel(t *testing.T) {
	rules := func(op string) map[string]any {
		return map[string]any{
			"selector": map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": op}}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"tier": "db"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/db:1.2.3"}}},
			},
		}
	}
	err := coreKindErr(&components.StatefulsetHandler{}, "statefulset", "db", rules("DoesNotExist"))
	if err == nil || !strings.Contains(err.Error(), "selector: rules out the label `app: db`") {
		t.Errorf("DoesNotExist: err = %v, want the app-label refusal", err)
	}
	if err := coreKindErr(&components.StatefulsetHandler{}, "statefulset", "db", rules("Exists")); err != nil {
		t.Errorf("Exists: refused: %v", err)
	}
	other := ptObject(t, stsPlain(htPlainPod))
	other["template"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)["app"] = "frontend"
	err = coreKindErr(&components.StatefulsetHandler{}, "statefulset", "db", other)
	if err == nil || !strings.Contains(err.Error(), "template.metadata.labels.app: \"frontend\" is not the `app` label of component \"db\"") {
		t.Errorf("another app label: err = %v, want the refusal", err)
	}
	if sts := stsGenerate(t, "db", ptObject(t, stsPlain(htPlainPod))); sts.Spec.Template.Labels["app"] != "db" {
		t.Errorf("template labels = %v, want the app label", sts.Spec.Template.Labels)
	}
}

// TestStatefulsetHandler_ScalerOwnsTheCount: a component with a scaler trait is
// built without spec.replicas, whichever count is authored, and the policy's
// replica maximum is not applied to it (the scaler holds its own maxReplicas to
// the same maximum). Without a scaler the count is kept and held to the maximum
// (go-kure/launcher#1179, go-kure/launcher#1268). The trait order does not
// matter, and a direct handler call reaches the kind with no validation step in
// front of it.
func TestStatefulsetHandler_ScalerOwnsTheCount(t *testing.T) {
	scaler := oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 1, "maxReplicas": 2}}
	other := oam.Trait{Type: "ingress", Properties: map[string]any{}}
	build := func(t *testing.T, replicas any, policy oam.Policy, traits ...oam.Trait) (*appsv1.StatefulSet, error) {
		t.Helper()
		props := ptObject(t, stsPlain(htPlainPod))
		if replicas != nil {
			props["replicas"] = replicas
		}
		cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
			&oam.Component{Name: "db", Type: "statefulset", Properties: props, Traits: traits}, coreKindNamespace)
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
			return nil, err
		}
		objs, err := cfg.Generate(stack.NewApplication("db", coreKindNamespace, cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return (*objs[0]).(*appsv1.StatefulSet), nil
	}
	strict := ptStrictPolicy() // replica maximum 3
	withDefault := &stubPolicy{defaultReplicas: int32ptr(2), maxReplicas: int32ptr(3)}

	for name, tc := range map[string]struct {
		replicas any
		traits   []oam.Trait
	}{
		"a count over the maximum":    {10, []oam.Trait{scaler}},
		"a count within the maximum":  {2, []oam.Trait{scaler}},
		"no count":                    {nil, []oam.Trait{scaler}},
		"the scaler declared last":    {10, []oam.Trait{other, scaler}},
		"the scaler declared first":   {10, []oam.Trait{scaler, other}},
		"a scaler under a default":    {nil, []oam.Trait{scaler}},
		"a scaler, the count is zero": {0, []oam.Trait{scaler}},
	} {
		t.Run("scaled, "+name, func(t *testing.T) {
			policy := oam.Policy(strict)
			if name == "a scaler under a default" {
				policy = withDefault
			}
			sts, err := build(t, tc.replicas, policy, tc.traits...)
			if err != nil {
				t.Fatalf("with a scaler: %v", err)
			}
			if sts.Spec.Replicas != nil {
				t.Errorf("with a scaler: replicas = %d, want it unset", *sts.Spec.Replicas)
			}
		})
	}

	t.Run("not scaled, over the maximum", func(t *testing.T) {
		if _, err := build(t, 10, strict, other); err == nil || !strings.Contains(err.Error(), "replicas 10 exceeds enforced maximum 3") {
			t.Errorf("err = %v, want the replica maximum refusal", err)
		}
	})
	t.Run("not scaled, at the maximum", func(t *testing.T) {
		sts, err := build(t, 3, strict)
		if err != nil {
			t.Fatalf("replicas 3 under a maximum of 3: %v", err)
		}
		if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 3 {
			t.Errorf("replicas = %v, want the authored 3", sts.Spec.Replicas)
		}
	})
	t.Run("not scaled, no count: the policy default is not filled", func(t *testing.T) {
		sts, err := build(t, nil, withDefault)
		if err != nil {
			t.Fatalf("no replicas: %v", err)
		}
		if sts.Spec.Replicas != nil {
			t.Errorf("replicas = %d, want it unset: the kind fills no default", *sts.Spec.Replicas)
		}
	})
	t.Run("not scaled, no count, a maximum of one", func(t *testing.T) {
		// An unset count is the API server's default of one, which the maximum holds.
		if _, err := build(t, nil, &stubPolicy{maxReplicas: int32ptr(1)}); err != nil {
			t.Errorf("an unset count under a maximum of one: %v", err)
		}
	})
	t.Run("not scaled, a count of zero is not refused", func(t *testing.T) {
		sts, err := build(t, 0, strict)
		if err != nil {
			t.Fatalf("replicas 0: %v", err)
		}
		if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 0 {
			t.Errorf("replicas = %v, want the authored 0", sts.Spec.Replicas)
		}
	})
}

// TestStatefulsetPolicy_PodTemplate: the pod template is held to the environment
// policy for every container, the init containers included, and a violation names
// its path under the template. The cases of the former reshaped parser (the
// extra containers were `sidecars`) are cases of a second entry of `containers`
// here.
func TestStatefulsetPolicy_PodTemplate(t *testing.T) {
	const image = "image: registry.example/team/db:1.2.3\n"
	const side = "  - name: side\n    image: registry.example/team/side:1.0.0\n"
	pods := map[string]struct {
		pod  string
		want string
	}{
		"a privileged container": {
			"containers:\n  - name: app\n    " + image + "    securityContext:\n      privileged: true\n",
			"template.spec.containers[0]"},
		"a privileged second container": {
			"containers:\n  - name: app\n    " + image + "  - name: side\n    " + image + "    securityContext:\n      privileged: true\n",
			"template.spec.containers[1]"},
		"a privileged init container": {
			"initContainers:\n  - name: setup\n    " + image + "    securityContext:\n      privileged: true\ncontainers:\n  - name: app\n    " + image,
			"template.spec.initContainers[0]"},
		"a forbidden capability added": {
			"containers:\n  - name: app\n    " + image + "    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\n",
			"template.spec.containers[0]"},
		"a forbidden capability added in a second container": {
			"containers:\n  - name: app\n    " + image + side + "    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\n",
			"template.spec.containers[1]"},
		"a forbidden capability added in an init container": {
			"initContainers:\n  - name: setup\n    " + image + "    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\ncontainers:\n  - name: app\n    " + image,
			"template.spec.initContainers[0]"},
		"a cpu limit over the maximum": {
			"containers:\n  - name: app\n    " + image + "    resources:\n      limits:\n        cpu: \"8\"\n",
			"template.spec.containers[0]"},
		"a cpu limit over the maximum in an init container": {
			"initContainers:\n  - name: setup\n    " + image + "    resources:\n      limits:\n        cpu: \"8\"\ncontainers:\n  - name: app\n    " + image,
			"template.spec.initContainers[0]"},
		"a memory limit over the maximum in a second container": {
			"containers:\n  - name: app\n    " + image + side + "    resources:\n      limits:\n        memory: 4Gi\n",
			"template.spec.containers[1]"},
		"an image outside the allowlist": {
			"containers:\n  - name: app\n    " + image + "  - name: side\n    image: other.example/team/side:1.0.0\n",
			"template.spec.containers[1]"},
		"an init image outside the allowlist": {
			"initContainers:\n  - name: setup\n    image: other.example/team/init:1.0.0\ncontainers:\n  - name: app\n    " + image,
			"template.spec.initContainers[0]"},
		"a hostPath volume": {
			htPlainPod + "volumes:\n  - name: logs\n    hostPath:\n      path: /var/log/app\n",
			"template.spec"},
		"host networking": {
			htPlainPod + "hostNetwork: true\n",
			"template.spec"},
	}
	for name, tc := range pods {
		t.Run(name, func(t *testing.T) {
			_, err := pvTransform("statefulset", &components.StatefulsetHandler{}, ptObject(t, stsPlain(tc.pod)), pvPolicy(ptStrictPolicy()))
			htWantViolation(t, err, tc.want)
			// The same document builds under a policy that allows all of it:
			// the refusal is the policy's, not the parse's.
			if name != "a hostPath volume" && name != "host networking" && !strings.Contains(name, "image") {
				permissive := &stubPolicy{allowPrivileged: true}
				if _, err := pvTransform("statefulset", &components.StatefulsetHandler{}, ptObject(t, stsPlain(tc.pod)), pvPolicy(permissive)); err != nil {
					t.Errorf("under a permissive policy: %v", err)
				}
			}
		})
	}
	// The same pod spec with nothing the policy forbids builds.
	if _, err := pvTransform("statefulset", &components.StatefulsetHandler{}, ptObject(t, stsPlain(htPlainPod)), pvPolicy(ptStrictPolicy())); err != nil {
		t.Errorf("a plain pod: %v", err)
	}
}

// TestStatefulsetPolicy_NoIntrinsicDefaultIsBounded: the kind fills no resource
// default, so a container that authors no resources has no value for the maximum
// to hold (the former reshaped parser held its generated default of 100m cpu and
// 128Mi memory to the maximum, and said "generated default"). An authored value
// is held, as the case above shows.
func TestStatefulsetPolicy_NoIntrinsicDefaultIsBounded(t *testing.T) {
	tiny := &stubPolicy{maxCPU: "50m", maxMemory: "64Mi"}
	cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db", Type: "statefulset", Properties: ptObject(t, stsPlain(htPlainPod))}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if err := cfg.(oam.Enforceable).ApplyPolicy(tiny); err != nil {
		t.Errorf("a container with no resources under a 50m / 64Mi maximum: %v", err)
	}
}

// TestStatefulsetConfig_ApplyPolicy_MaxReplicas: an authored count over the
// maximum is refused, naming both numbers (the former reshaped parser's case).
func TestStatefulsetConfig_ApplyPolicy_MaxReplicas(t *testing.T) {
	props := ptObject(t, stsPlain(htPlainPod))
	props["replicas"] = 5
	cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db", Type: "statefulset", Properties: props}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	err = cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{maxReplicas: int32ptr(3)})
	if err == nil || !strings.Contains(err.Error(), "replicas 5 exceeds enforced maximum 3") {
		t.Errorf("err = %v, want the replica maximum refusal", err)
	}
}

// TestStatefulsetConfig_ApplyPolicy_NilIsANoOp: a nil policy checks nothing, as on
// every other config, whatever the config holds.
func TestStatefulsetConfig_ApplyPolicy_NilIsANoOp(t *testing.T) {
	if err := (&components.StatefulsetConfig{Name: "db", Namespace: coreKindNamespace}).ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) on a zero config = %v, want nil", err)
	}
	cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db", Type: "statefulset", Properties: ptObject(t, stsRich)}, coreKindNamespace)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if err := cfg.(oam.Enforceable).ApplyPolicy(nil); err != nil {
		t.Errorf("ApplyPolicy(nil) = %v, want nil", err)
	}
}

// TestStatefulsetConfig_ApplyPolicy_MaxStorageSize: each claim template's
// requested storage is held to the storage maximum, and a refusal names the claim
// by its index. A claim that requests no storage, or requests it under the
// maximum, is not refused (the former `size` and `resources.requests.storage`
// spellings are one spelling now).
func TestStatefulsetConfig_ApplyPolicy_MaxStorageSize(t *testing.T) {
	noRequest := map[string]any{"metadata": map[string]any{"name": "scratch"}}
	cases := []struct {
		name   string
		claims []any
		want   string // empty: the policy admits it
	}{
		{"over the limit", []any{stsClaim("data", "20Gi")}, "volumeClaimTemplates[0].spec.resources.requests.storage"},
		{"under the limit", []any{stsClaim("data", "1Gi")}, ""},
		{"at the limit", []any{stsClaim("data", "5Gi")}, ""},
		{"the second claim over the limit", []any{stsClaim("data", "1Gi"), stsClaim("wal", "20Gi")}, "volumeClaimTemplates[1].spec.resources.requests.storage"},
		{"a claim with no request", []any{noRequest}, ""},
		{"a claim with no request beside one over the limit", []any{noRequest, stsClaim("wal", "20Gi")}, "volumeClaimTemplates[1].spec.resources.requests.storage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
				&oam.Component{Name: "db", Type: "statefulset", Properties: stsWithClaims(t, tc.claims...)}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			err = cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{maxStorageSize: "5Gi"})
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("ApplyPolicy: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), "exceeds enforced maximum") || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want the storage maximum refusal naming %q", err, tc.want)
			}
		})
	}
	t.Run("no maximum", func(t *testing.T) {
		cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
			&oam.Component{Name: "db", Type: "statefulset", Properties: stsWithClaims(t, stsClaim("data", "20Gi"))}, coreKindNamespace)
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if err := cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{}); err != nil {
			t.Errorf("no storage maximum: %v", err)
		}
	})
}

// TestStatefulsetHandler_ClaimTemplatesReachTheSpec: every PersistentVolumeClaim
// field an author sets lands on the generated claim template, and none is added:
// no access mode default, no storage class, no size. launcher never mirrors
// dataSourceRef into the legacy dataSource field: the apiserver does that itself.
func TestStatefulsetHandler_ClaimTemplatesReachTheSpec(t *testing.T) {
	sts := stsGenerate(t, "db", ptObject(t, stsRich))
	if len(sts.Spec.VolumeClaimTemplates) != 2 {
		t.Fatalf("got %d claim templates, want 2", len(sts.Spec.VolumeClaimTemplates))
	}
	data := sts.Spec.VolumeClaimTemplates[0]
	if data.Name != "data" || data.Labels["tier"] != "db" {
		t.Errorf("data metadata = %q %v, want data with the tier label", data.Name, data.Labels)
	}
	if got := data.Spec.Resources.Requests["storage"]; got.String() != "5Gi" {
		t.Errorf("data requests.storage = %v, want 5Gi", &got)
	}
	if got := data.Spec.Resources.Limits["storage"]; got.String() != "8Gi" {
		t.Errorf("data limits.storage = %v, want 8Gi", &got)
	}
	if data.Spec.StorageClassName == nil || *data.Spec.StorageClassName != "fast" {
		t.Errorf("data storageClassName = %v, want fast", ptrString(data.Spec.StorageClassName))
	}
	if len(data.Spec.AccessModes) != 1 || data.Spec.AccessModes[0] != "ReadWriteOncePod" {
		t.Errorf("data accessModes = %v, want [ReadWriteOncePod]", data.Spec.AccessModes)
	}
	if data.Spec.Selector == nil || data.Spec.Selector.MatchLabels["tier"] != "fast" {
		t.Errorf("data selector = %v, want tier=fast", data.Spec.Selector)
	}
	if data.Spec.VolumeMode == nil || *data.Spec.VolumeMode != "Filesystem" {
		t.Errorf("data volumeMode = %v, want Filesystem", data.Spec.VolumeMode)
	}
	ref := data.Spec.DataSourceRef
	if ref == nil || ref.Kind != "VolumeSnapshot" || ref.Name != "seed" || ref.APIGroup == nil || *ref.APIGroup != "snapshot.storage.k8s.io" {
		t.Errorf("data dataSourceRef = %+v, want VolumeSnapshot/seed in snapshot.storage.k8s.io", ref)
	}
	if data.Spec.DataSource != nil {
		t.Errorf("data dataSource = %+v, want nil: mirroring is the apiserver's job", data.Spec.DataSource)
	}
	if data.Spec.VolumeAttributesClassName == nil || *data.Spec.VolumeAttributesClassName != "gold" {
		t.Errorf("data volumeAttributesClassName = %v, want gold", data.Spec.VolumeAttributesClassName)
	}

	wal := sts.Spec.VolumeClaimTemplates[1]
	if wal.Name != "wal" {
		t.Errorf("templates[1].Name = %q, want wal", wal.Name)
	}
	if got := wal.Spec.Resources.Requests["storage"]; got.String() != "1Gi" {
		t.Errorf("wal requests.storage = %v, want 1Gi", &got)
	}
	if len(wal.Spec.AccessModes) != 0 || wal.Spec.Selector != nil || wal.Spec.VolumeMode != nil || wal.Spec.StorageClassName != nil {
		t.Errorf("wal spec = %+v, want only the authored request", wal.Spec)
	}
}

// TestStatefulsetHandler_EndpointsComeFromTheFirstContainer: the endpoints are the
// ports of the first container of the template, in the authored order; the ports
// of a later container are not the component's service surface.
func TestStatefulsetHandler_EndpointsComeFromTheFirstContainer(t *testing.T) {
	h := &components.StatefulsetHandler{}
	eps, err := h.Endpoints(&oam.Component{Name: "db", Type: "statefulset", Properties: ptObject(t, stsRich)})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 1 || eps[0].Name != "tcp" || eps[0].Ports[0].IntValue() != 5432 {
		t.Errorf("endpoints = %+v, want the one tcp:5432 of the first container", eps)
	}
	eps, err = h.Endpoints(&oam.Component{Name: "db", Type: "statefulset", Properties: ptObject(t, stsPlain(htPlainPod))})
	if err != nil || len(eps) != 0 {
		t.Errorf("a component with no ports: endpoints = %+v, err = %v; want none", eps, err)
	}
	if _, err := h.Endpoints(&oam.Component{Name: "db", Type: "statefulset", Properties: map[string]any{"image": "x"}}); err == nil {
		t.Error("Endpoints of a refused component returned no error")
	}
}

// TestStatefulsetConfig_ServiceAccountName: the StatefulSet reports the account its
// pods run as to the traits that bind identity to it: the template's
// serviceAccountName, the deprecated serviceAccount where that one is unset, and
// "" for the namespace's default account. It always runs pods.
func TestStatefulsetConfig_ServiceAccountName(t *testing.T) {
	for name, tc := range map[string]struct {
		podSpec string
		want    string
	}{
		"none authored":      {htPlainPod, ""},
		"serviceAccountName": {htPlainPod + "serviceAccountName: db\n", "db"},
		"deprecated alias":   {htPlainPod + "serviceAccount: legacy\n", "legacy"},
		"both":               {htPlainPod + "serviceAccountName: db\nserviceAccount: legacy\n", "db"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(
				&oam.Component{Name: "db", Type: "statefulset", Properties: ptObject(t, stsPlain(tc.podSpec))}, coreKindNamespace)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			namer, ok := cfg.(oam.ServiceAccountNamer)
			if !ok {
				t.Fatal("the statefulset config does not implement oam.ServiceAccountNamer")
			}
			if got, runsPods := namer.ServiceAccountName(); got != tc.want || !runsPods {
				t.Errorf("ServiceAccountName() = %q, %v; want %q, true", got, runsPods, tc.want)
			}
		})
	}
}

// TestStatefulsetConfig_GenerateRepeatsTheParseRefusals: the config is exported, so
// a caller can build one without a parse. Generate then refuses what the parse
// refuses (a duplicate container name, a missing selector, a duplicate claim
// template name and an invalid serviceName here) instead of emitting a
// StatefulSet the API server would reject, and it does not write the `app` label
// into the config's own template labels.
func TestStatefulsetConfig_GenerateRepeatsTheParseRefusals(t *testing.T) {
	var spec appsv1.StatefulSetSpec
	if err := yaml.UnmarshalStrict([]byte(stsPlain(htPlainPod)), &spec); err != nil {
		t.Fatalf("decoding the test spec: %v", err)
	}
	cfg := &components.StatefulsetConfig{Name: "db", Namespace: coreKindNamespace, Spec: spec}
	if _, err := cfg.Generate(stack.NewApplication("db", coreKindNamespace, cfg)); err != nil {
		t.Fatalf("Generate of a valid config: %v", err)
	}
	if _, ok := cfg.Spec.Template.Labels["app"]; ok {
		t.Errorf("Generate wrote the app label into the config's template labels: %v", cfg.Spec.Template.Labels)
	}

	mutate := func(f func(*appsv1.StatefulSetSpec)) *components.StatefulsetConfig {
		s := cfg.Spec.DeepCopy()
		f(s)
		return &components.StatefulsetConfig{Name: "db", Namespace: coreKindNamespace, Spec: *s}
	}
	for name, tc := range map[string]struct {
		cfg  *components.StatefulsetConfig
		want string
	}{
		"duplicate container name": {mutate(func(s *appsv1.StatefulSetSpec) {
			s.Template.Spec.Containers = append(s.Template.Spec.Containers, s.Template.Spec.Containers[0])
		}), "is also used by"},
		"no selector": {mutate(func(s *appsv1.StatefulSetSpec) { s.Selector = nil }), "selector: required"},
		"duplicate claim template": {mutate(func(s *appsv1.StatefulSetSpec) {
			var vct appsv1.StatefulSetSpec
			_ = yaml.UnmarshalStrict([]byte("volumeClaimTemplates:\n  - metadata:\n      name: data\n"), &vct)
			s.VolumeClaimTemplates = append(vct.VolumeClaimTemplates, vct.VolumeClaimTemplates...)
		}), `volumeClaimTemplates: duplicate name "data"`},
		"invalid serviceName": {mutate(func(s *appsv1.StatefulSetSpec) { s.ServiceName = "Not_A_Name" }), "serviceName:"},
		"another app label":   {mutate(func(s *appsv1.StatefulSetSpec) { s.Template.Labels["app"] = "frontend" }), "template.metadata.labels.app"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.cfg.Generate(stack.NewApplication("db", coreKindNamespace, tc.cfg)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
