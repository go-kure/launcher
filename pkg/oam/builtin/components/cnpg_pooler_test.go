package components_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// minimalPooler is the smallest cnpg-pooler the Pooler CRD admits.
func minimalPooler() map[string]any {
	return map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}}
}

func cnpgPoolerErr(t *testing.T, props map[string]any) error {
	t.Helper()
	_, err := (&components.CnpgPoolerHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db-pooler", Type: "cnpg-pooler", Properties: props}, "data")
	return err
}

func newCnpgPooler(t *testing.T, props map[string]any) *components.CnpgPoolerConfig {
	t.Helper()
	cfg, err := (&components.CnpgPoolerHandler{}).ToApplicationConfig(
		&oam.Component{Name: "db-pooler", Type: "cnpg-pooler", Properties: props}, "data")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg.(*components.CnpgPoolerConfig)
}

func generateCnpgPooler(t *testing.T, c *components.CnpgPoolerConfig) *cnpgv1.Pooler {
	t.Helper()
	objs, err := c.Generate(stack.NewApplication("db-pooler", "data", c))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate: got %d objects, want 1", len(objs))
	}
	pooler, ok := (*objs[0]).(*cnpgv1.Pooler)
	if !ok {
		t.Fatalf("Generate: got %T, want *cnpgv1.Pooler", *objs[0])
	}
	return pooler
}

func TestCnpgPoolerHandler_CanHandle(t *testing.T) {
	h := &components.CnpgPoolerHandler{}
	if !h.CanHandle("cnpg-pooler") {
		t.Error("CanHandle(cnpg-pooler) = false")
	}
	if h.CanHandle("cnpg-cluster") {
		t.Error("CanHandle(cnpg-cluster) = true")
	}
}

// TestCnpgPoolerHandler_MinimalWritesNoOpinions: the smallest admitted
// component yields a Pooler carrying only identity, the cluster reference and
// an empty pgbouncer block — none of the type or instance count postgresql
// writes for its pooler.
func TestCnpgPoolerHandler_MinimalWritesNoOpinions(t *testing.T) {
	c := newCnpgPooler(t, minimalPooler())
	if err := c.ApplyPolicy(&stubPolicy{maxReplicas: int32ptr(0), defaultReplicas: int32ptr(3)}); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	pooler := generateCnpgPooler(t, c)
	if pooler.Name != "db-pooler" || pooler.Namespace != "data" {
		t.Errorf("identity = %s/%s, want data/db-pooler", pooler.Namespace, pooler.Name)
	}
	if pooler.APIVersion != "postgresql.cnpg.io/v1" || pooler.Kind != "Pooler" {
		t.Errorf("GVK = %s %s", pooler.APIVersion, pooler.Kind)
	}
	want := cnpgv1.PoolerSpec{Cluster: cnpgv1.LocalObjectReference{Name: "db"}, PgBouncer: &cnpgv1.PgBouncerSpec{}}
	if !reflect.DeepEqual(pooler.Spec, want) {
		t.Errorf("spec = %+v, want only cluster and an empty pgbouncer", pooler.Spec)
	}
}

func TestCnpgPoolerHandler_DecodesDeepBlocks(t *testing.T) {
	c := newCnpgPooler(t, map[string]any{
		"cluster":   map[string]any{"name": "db"},
		"type":      "ro",
		"instances": 0,
		"pgbouncer": map[string]any{
			"poolMode":   "transaction",
			"parameters": map[string]any{"max_client_conn": "1000"},
			"paused":     false,
		},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"team": "a"}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "pgbouncer", "image": "ghcr.io/cloudnative-pg/pgbouncer:1"}}},
		},
		"serviceAccountName": "pooler",
	})
	s := c.Spec
	if s.Type != cnpgv1.PoolerTypeRO || s.Instances == nil || *s.Instances != 0 || s.ServiceAccountName != "pooler" {
		t.Errorf("type/instances/serviceAccountName = %q/%v/%q", s.Type, s.Instances, s.ServiceAccountName)
	}
	if s.PgBouncer.PoolMode != cnpgv1.PgBouncerPoolModeTransaction || s.PgBouncer.Parameters["max_client_conn"] != "1000" ||
		s.PgBouncer.Paused == nil || *s.PgBouncer.Paused {
		t.Errorf("pgbouncer = %+v", s.PgBouncer)
	}
	if s.Template == nil || s.Template.ObjectMeta.Labels["team"] != "a" || s.Template.Spec.Containers[0].Name != "pgbouncer" {
		t.Errorf("template = %+v", s.Template)
	}
}

// TestCnpgPoolerHandler_Refusals: what the Pooler CRD or CloudNativePG's
// webhook would refuse, and what the strict decode refuses, is refused at
// parse time by path.
func TestCnpgPoolerHandler_Refusals(t *testing.T) {
	with := func(k string, v any) map[string]any {
		p := minimalPooler()
		p[k] = v
		return p
	}
	without := func(k string) map[string]any {
		p := minimalPooler()
		delete(p, k)
		return p
	}
	for _, tt := range []struct {
		name    string
		props   map[string]any
		wantSub string
	}{
		{"no cluster", without("cluster"), "cluster.name: required"},
		{"empty cluster name", with("cluster", map[string]any{"name": ""}), "cluster.name: required"},
		{"null cluster", with("cluster", nil), "cluster.name: required"},
		{"cluster name CloudNativePG refuses", with("cluster", map[string]any{"name": "db.main"}),
			`cluster.name "db.main": must be a DNS-1035 label of at most 50 characters`},
		{"cluster named like the pooler", with("cluster", map[string]any{"name": "db-pooler"}),
			`cluster.name "db-pooler": a pooler cannot have the same name as its cluster`},
		{"no pgbouncer", without("pgbouncer"), "pgbouncer: required"},
		{"null pgbouncer", with("pgbouncer", nil), "pgbouncer: required"},
		{"unknown top-level key", with("replicas", 2), `unknown field "replicas"`},
		{"null unknown key", with("replicas", nil), `unknown field "replicas"`},
		{"misspelt nested key", with("pgbouncer", map[string]any{"poolMod": "session"}), `unknown field "poolMod"`},
		{"wrong scalar type", with("instances", "2"), "cannot unmarshal string"},
		{"template ephemeral containers",
			with("template", map[string]any{"spec": map[string]any{"containers": []any{}, "ephemeralContainers": []any{map[string]any{"name": "debug"}}}}),
			"template.spec.ephemeralContainers: not supported"},
		{"template activeDeadlineSeconds",
			with("template", map[string]any{"spec": map[string]any{"containers": []any{}, "activeDeadlineSeconds": 60}}),
			"template.spec.activeDeadlineSeconds: only Job pods may set activeDeadlineSeconds"},
		{"template priority",
			with("template", map[string]any{"spec": map[string]any{"containers": []any{}, "priority": 1000}}),
			"template.spec.priority: not authorable"},
		{"template overhead",
			with("template", map[string]any{"spec": map[string]any{"containers": []any{}, "overhead": map[string]any{"cpu": "100m"}}}),
			"template.spec.overhead: not authorable"},
		// The CRD requires these and the pod spec's type leaves them out when
		// they are empty.
		{"restart rule without an action",
			with("template", poolerTemplate("containers", map[string]any{"name": "pgbouncer", "restartPolicyRules": []any{
				map[string]any{"action": "Restart"}, map[string]any{"exitCodes": map[string]any{"operator": "In", "values": []any{42}}},
			}})),
			"template.spec.containers[0].restartPolicyRules[1].action: required"},
		{"restart rule with an empty action",
			with("template", poolerTemplate("containers", map[string]any{"name": "pgbouncer", "restartPolicyRules": []any{map[string]any{"action": ""}}})),
			"template.spec.containers[0].restartPolicyRules[0].action: required"},
		{"exit codes without an operator",
			with("template", poolerTemplate("containers", map[string]any{"name": "pgbouncer", "restartPolicyRules": []any{
				map[string]any{"action": "Restart", "exitCodes": map[string]any{"values": []any{42}}},
			}})),
			"template.spec.containers[0].restartPolicyRules[0].exitCodes.operator: required"},
		{"init container's restart rule without an action",
			with("template", poolerTemplate("initContainers", map[string]any{"name": "wait"}, map[string]any{"name": "seed", "restartPolicyRules": []any{map[string]any{}}})),
			"template.spec.initContainers[1].restartPolicyRules[0].action: required"},
		{"init container's exit codes without an operator",
			with("template", poolerTemplate("initContainers", map[string]any{"name": "seed", "restartPolicyRules": []any{
				map[string]any{"action": "Restart", "exitCodes": map[string]any{"operator": nil}},
			}})),
			"template.spec.initContainers[0].restartPolicyRules[0].exitCodes.operator: required"},
		{"pod certificate without a signer",
			with("template", poolerTemplate("volumes", map[string]any{"name": "identity", "projected": map[string]any{"sources": []any{
				map[string]any{"podCertificate": map[string]any{"keyType": "ECDSAP384"}},
			}}})),
			"template.spec.volumes[0].projected.sources[0].podCertificate.signerName: required"},
		{"pod certificate without a key type",
			with("template", poolerTemplate("volumes", map[string]any{"name": "tmp", "emptyDir": map[string]any{}}, map[string]any{"name": "identity", "projected": map[string]any{"sources": []any{
				map[string]any{"serviceAccountToken": map[string]any{"path": "token"}},
				map[string]any{"podCertificate": map[string]any{"signerName": "example.com/workload"}},
			}}})),
			"template.spec.volumes[1].projected.sources[1].podCertificate.keyType: required"},
		{"two spellings of one field",
			map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}, "type": "rw", "Type": "ro"},
			"sets the same field as"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := cnpgPoolerErr(t, tt.props)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

// poolerTemplate is a pooler's `template` whose pod spec holds the list, and
// an empty container list where the list is another.
func poolerTemplate(list string, entries ...any) map[string]any {
	spec := map[string]any{"containers": []any{}}
	spec[list] = append([]any{}, entries...)
	return map[string]any{"spec": spec}
}

// TestCnpgPoolerHandler_NameBound pins the Pooler-name bound on both entry
// points: CloudNativePG names the pooler's Service after it, so it must be a
// DNS-1035 label, and the endpoint selector copies it into a label value.
func TestCnpgPoolerHandler_NameBound(t *testing.T) {
	h := &components.CnpgPoolerHandler{}
	for _, name := range []string{"1pool", "db.pooler", strings.Repeat("a", 64)} {
		comp := &oam.Component{Name: name, Type: "cnpg-pooler", Properties: minimalPooler()}
		want := fmt.Sprintf("cnpg-pooler name %q: must be a DNS-1035 label of at most 63 characters (CloudNativePG names the pooler's Service after it)", name)
		if _, err := h.ToApplicationConfig(comp, "data"); err == nil || err.Error() != want {
			t.Errorf("ToApplicationConfig(%q): err = %v, want %q", name, err, want)
		}
		if _, err := h.Endpoints(comp); err == nil || err.Error() != want {
			t.Errorf("Endpoints(%q): err = %v, want %q", name, err, want)
		}
	}
	longest := strings.Repeat("a", 63)
	comp := &oam.Component{Name: longest, Type: "cnpg-pooler", Properties: minimalPooler()}
	if _, err := h.ToApplicationConfig(comp, "data"); err != nil {
		t.Errorf("ToApplicationConfig(63 characters): %v", err)
	}
}

// TestCnpgPoolerHandler_EndpointMatchesPostgresqlPooler pins the byte identity
// of the pooler endpoint with postgresql's: a cnpg-pooler named
// <cluster>-pooler declares exactly the endpoint a postgresql component with
// an enabled pooler publishes for it, so a consumer's synthesized ingress
// allow does not change when the pooler moves from one to the other.
func TestCnpgPoolerHandler_EndpointMatchesPostgresqlPooler(t *testing.T) {
	pg, err := (postgresqlViaRule{}).Endpoints(&oam.Component{
		Name: "orders-db", Type: "postgresql",
		Properties: map[string]any{"pooler": map[string]any{"enabled": true}},
	})
	if err != nil {
		t.Fatalf("postgresql Endpoints: %v", err)
	}
	if len(pg) != 2 {
		t.Fatalf("postgresql endpoints = %+v, want the cluster and the pooler", pg)
	}
	got, err := (&components.CnpgPoolerHandler{}).Endpoints(&oam.Component{Name: "orders-db-pooler", Type: "cnpg-pooler"})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("endpoints = %+v, want one", got)
	}
	gotJSON, err := json.Marshal(got[0])
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(pg[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) || !reflect.DeepEqual(got[0], pg[1]) {
		t.Errorf("endpoint = %s, want postgresql's pooler endpoint %s", gotJSON, wantJSON)
	}
	if got[0].PodSelector.MatchLabels["cnpg.io/poolerName"] != "orders-db-pooler" || got[0].Ports[0].IntVal != 5432 {
		t.Errorf("endpoint = %s, want cnpg.io/poolerName=orders-db-pooler on 5432", gotJSON)
	}
}

// TestCnpgPoolerHandler_EndpointsRefusesAPoolerNamedLikeItsCluster: a pooler
// whose cluster reference is its own name is refused by the build, and its
// endpoint in the same words, so no selector is answered for a pooler that
// cannot be built. Only that relation is checked there: a cluster reference the
// decode refuses for another reason is still the decode's to refuse.
func TestCnpgPoolerHandler_EndpointsRefusesAPoolerNamedLikeItsCluster(t *testing.T) {
	h := &components.CnpgPoolerHandler{}
	pooler := func(cluster any) *oam.Component {
		props := minimalPooler()
		props["cluster"] = cluster
		return &oam.Component{Name: "main", Type: "cnpg-pooler", Properties: props}
	}

	const want = `cluster.name "main": a pooler cannot have the same name as its cluster`
	same := pooler(map[string]any{"name": "main"})
	// The decode matches field names in any case, so this spelling is the same
	// pooler to the build, and to the endpoint.
	otherCase := &oam.Component{Name: "main", Type: "cnpg-pooler", Properties: map[string]any{
		"Cluster": map[string]any{"Name": "main"}, "pgbouncer": map[string]any{},
	}}
	// A direct caller's typed values are read as their JSON serialization by
	// the build, and by the endpoint.
	for spelling, comp := range map[string]*oam.Component{
		"as the API spells it": same,
		"in another case":      otherCase,
		"a typed map":          pooler(map[string]string{"name": "main"}),
		"a struct":             pooler(cnpgv1.LocalObjectReference{Name: "main"}),
	} {
		_, buildErr := h.ToApplicationConfig(comp, "data")
		_, endpointsErr := h.Endpoints(comp)
		for reader, err := range map[string]error{"ToApplicationConfig": buildErr, "Endpoints": endpointsErr} {
			if err == nil || err.Error() != want {
				t.Errorf("%s, %s: err = %v\nwant %q", spelling, reader, err, want)
			}
		}
	}

	// Properties encoding/json cannot serialize are refused by the build's
	// first step, and by the endpoint with the same error.
	unserializable := pooler(make(chan int))
	_, buildErr := h.ToApplicationConfig(unserializable, "data")
	_, endpointsErr := h.Endpoints(unserializable)
	if buildErr == nil || endpointsErr == nil || buildErr.Error() != endpointsErr.Error() {
		t.Errorf("unserializable properties: ToApplicationConfig err = %v, Endpoints err = %v; want one refusal from both", buildErr, endpointsErr)
	}

	for name, cluster := range map[string]any{
		"another cluster": map[string]any{"name": "db"},
		"no name":         map[string]any{},
		"null":            nil,
		"no object":       "main",
	} {
		eps, err := h.Endpoints(pooler(cluster))
		if err != nil || len(eps) != 1 || eps[0].PodSelector.MatchLabels["cnpg.io/poolerName"] != "main" {
			t.Errorf("%s: Endpoints = %+v, %v; want the pooler's own endpoint", name, eps, err)
		}
	}
}

func TestCnpgPoolerConfig_ApplyPolicy(t *testing.T) {
	tmpl := func(spec map[string]any) map[string]any {
		p := minimalPooler()
		p["template"] = map[string]any{"spec": spec}
		return p
	}
	container := func(extra map[string]any) map[string]any {
		c := map[string]any{"name": "pgbouncer"}
		for k, v := range extra {
			c[k] = v
		}
		return map[string]any{"containers": []any{c}}
	}
	for _, tt := range []struct {
		name    string
		props   map[string]any
		policy  *stubPolicy
		wantErr string
	}{
		{"pgbouncer image from a disallowed registry",
			map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{"image": "docker.io/pgbouncer:1"}},
			&stubPolicy{allowedRegistries: []string{"ghcr.io"}},
			`pgbouncer.image: image "docker.io/pgbouncer:1" is not from an allowed registry [ghcr.io]`},
		{"template container image from a disallowed registry",
			tmpl(container(map[string]any{"image": "docker.io/pgbouncer:1"})),
			&stubPolicy{allowedRegistries: []string{"ghcr.io"}},
			`template.spec.containers[0] "pgbouncer": image "docker.io/pgbouncer:1" is not from an allowed registry [ghcr.io]`},
		{"template init container image",
			tmpl(map[string]any{"containers": []any{}, "initContainers": []any{map[string]any{"name": "init", "image": "docker.io/busybox"}}}),
			&stubPolicy{allowedRegistries: []string{"ghcr.io"}},
			`template.spec.initContainers[0] "init": image "docker.io/busybox" is not from an allowed registry [ghcr.io]`},
		{"privileged template container",
			tmpl(container(map[string]any{"securityContext": map[string]any{"privileged": true}})),
			&stubPolicy{},
			`template.spec.containers[0] "pgbouncer": securityContext.privileged is not allowed by environment policy`},
		{"forbidden capability",
			tmpl(container(map[string]any{"securityContext": map[string]any{"capabilities": map[string]any{"add": []any{"NET_ADMIN"}}}})),
			&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}},
			`template.spec.containers[0] "pgbouncer": securityContext.capabilities.add: "NET_ADMIN" is forbidden by environment policy`},
		{"cpu limit above the maximum",
			tmpl(container(map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "2"}}})),
			&stubPolicy{maxCPU: "1"},
			`template.spec.containers[0] "pgbouncer": cpu limit "2" exceeds enforced maximum "1"`},
		{"memory request above the maximum",
			tmpl(container(map[string]any{"resources": map[string]any{"requests": map[string]any{"memory": "2Gi"}}})),
			&stubPolicy{maxMemory: "1Gi"},
			`template.spec.containers[0] "pgbouncer": memory request "2Gi" exceeds enforced maximum "1Gi"`},
		{"host network", tmpl(map[string]any{"containers": []any{}, "hostNetwork": true}), &stubPolicy{}, "template.spec: hostNetwork is not allowed by environment policy"},
		{"hostPath volume",
			tmpl(map[string]any{"containers": []any{}, "volumes": []any{map[string]any{"name": "h", "hostPath": map[string]any{"path": "/"}}}}),
			&stubPolicy{}, `template.spec: volume "h": hostPath volumes are not allowed by environment policy`},
		{"image volume from a disallowed registry",
			tmpl(map[string]any{"containers": []any{}, "volumes": []any{map[string]any{"name": "ext", "image": map[string]any{"reference": "docker.io/library/ext:1"}}}}),
			&stubPolicy{allowedRegistries: []string{"ghcr.io"}},
			`template.spec: volume "ext" image.reference: image "docker.io/library/ext:1" is not from an allowed registry [ghcr.io]`},
		{"ephemeral volume above the storage maximum",
			tmpl(map[string]any{"containers": []any{}, "volumes": []any{map[string]any{"name": "scratch", "ephemeral": map[string]any{
				"volumeClaimTemplate": map[string]any{"spec": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "1Ti"}}}}}}}}),
			&stubPolicy{maxStorageSize: "10Gi"},
			`template.spec: volume "scratch" ephemeral.volumeClaimTemplate.spec.resources.requests.storage "1Ti" exceeds enforced maximum "10Gi"`},
		{"pod hostProcess",
			tmpl(map[string]any{"containers": []any{}, "securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}}),
			&stubPolicy{}, "template.spec.securityContext.windowsOptions.hostProcess is not allowed by environment policy"},
		{"pod cpu limit above the maximum",
			tmpl(map[string]any{"containers": []any{}, "resources": map[string]any{"limits": map[string]any{"cpu": "2"}}}),
			&stubPolicy{maxCPU: "1"}, `template.spec: resources cpu limit "2" exceeds enforced maximum "1"`},
		{"pod memory request above the maximum",
			tmpl(map[string]any{"containers": []any{}, "resources": map[string]any{"requests": map[string]any{"memory": "2Gi"}}}),
			&stubPolicy{maxMemory: "1Gi"}, `template.spec: resources memory request "2Gi" exceeds enforced maximum "1Gi"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := newCnpgPooler(t, tt.props).ApplyPolicy(tt.policy)
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
	t.Run("allowed by policy", func(t *testing.T) {
		c := newCnpgPooler(t, tmpl(container(map[string]any{
			"image":           "ghcr.io/cloudnative-pg/pgbouncer:1",
			"securityContext": map[string]any{"privileged": true},
			"resources":       map[string]any{"limits": map[string]any{"cpu": "1", "memory": "1Gi"}},
		})))
		if err := c.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}, allowPrivileged: true, maxCPU: "1", maxMemory: "1Gi"}); err != nil {
			t.Errorf("ApplyPolicy: %v", err)
		}
	})
	// The instance count is deliberately unpoliced: postgresql applies no
	// replica policy to its pooler, so neither does the kind it lowers onto.
	t.Run("instances are not policed", func(t *testing.T) {
		p := minimalPooler()
		p["instances"] = 5
		c := newCnpgPooler(t, p)
		if err := c.ApplyPolicy(&stubPolicy{maxReplicas: int32ptr(1), defaultReplicas: int32ptr(2)}); err != nil {
			t.Errorf("ApplyPolicy: %v", err)
		}
		if *c.Spec.Instances != 5 {
			t.Errorf("instances = %d, want the authored 5", *c.Spec.Instances)
		}
	})
}

// TestCnpgPoolerConfig_GenerateRevalidates pins the emission-boundary repeat
// of the parse-time refusals, for a config built directly in Go.
func TestCnpgPoolerConfig_GenerateRevalidates(t *testing.T) {
	ok := cnpgv1.PoolerSpec{Cluster: cnpgv1.LocalObjectReference{Name: "db"}, PgBouncer: &cnpgv1.PgBouncerSpec{}}
	ephemeral := *ok.DeepCopy()
	ephemeral.Template = &cnpgv1.PodTemplateSpec{Spec: corev1.PodSpec{
		EphemeralContainers: []corev1.EphemeralContainer{{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debug"}}},
	}}
	withTemplate := func(ps corev1.PodSpec) cnpgv1.PoolerSpec {
		s := *ok.DeepCopy()
		s.Template = &cnpgv1.PodTemplateSpec{Spec: ps}
		return s
	}
	deadline, priority := int64(60), int32(1000)
	for _, tt := range []struct {
		name, app string
		spec      cnpgv1.PoolerSpec
		wantSub   string
	}{
		{"name CloudNativePG refuses", "db.pooler", ok, `cnpg-pooler name "db.pooler"`},
		{"no cluster", "db-pooler", cnpgv1.PoolerSpec{PgBouncer: &cnpgv1.PgBouncerSpec{}}, "cluster.name: required"},
		{"cluster named like the pooler", "db", ok, "a pooler cannot have the same name as its cluster"},
		{"no pgbouncer", "db-pooler", cnpgv1.PoolerSpec{Cluster: cnpgv1.LocalObjectReference{Name: "db"}}, "pgbouncer: required"},
		{"template ephemeral containers", "db-pooler", ephemeral, "template.spec.ephemeralContainers: not supported"},
		{"template activeDeadlineSeconds", "db-pooler", withTemplate(corev1.PodSpec{ActiveDeadlineSeconds: &deadline}),
			"template.spec.activeDeadlineSeconds: only Job pods may set activeDeadlineSeconds"},
		{"template priority", "db-pooler", withTemplate(corev1.PodSpec{Priority: &priority}), "template.spec.priority: not authorable"},
		{"template overhead", "db-pooler",
			withTemplate(corev1.PodSpec{Overhead: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}}),
			"template.spec.overhead: not authorable"},
		{"restart rule without an action", "db-pooler",
			withTemplate(corev1.PodSpec{Containers: []corev1.Container{{Name: "pgbouncer", RestartPolicyRules: []corev1.ContainerRestartRule{{}}}}}),
			"template.spec.containers[0].restartPolicyRules[0].action: required"},
		{"init container's exit codes without an operator", "db-pooler",
			withTemplate(corev1.PodSpec{InitContainers: []corev1.Container{{Name: "seed", RestartPolicyRules: []corev1.ContainerRestartRule{
				{Action: corev1.ContainerRestartRuleActionRestart, ExitCodes: &corev1.ContainerRestartRuleOnExitCodes{Values: []int32{42}}},
			}}}}),
			"template.spec.initContainers[0].restartPolicyRules[0].exitCodes.operator: required"},
		{"pod certificate without a signer", "db-pooler",
			withTemplate(corev1.PodSpec{Volumes: []corev1.Volume{{Name: "identity", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{{PodCertificate: &corev1.PodCertificateProjection{KeyType: "ECDSAP384"}}},
			}}}}}),
			"template.spec.volumes[0].projected.sources[0].podCertificate.signerName: required"},
		{"pod certificate without a key type", "db-pooler",
			withTemplate(corev1.PodSpec{Volumes: []corev1.Volume{{Name: "identity", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
				Sources: []corev1.VolumeProjection{{PodCertificate: &corev1.PodCertificateProjection{SignerName: "example.com/workload"}}},
			}}}}}),
			"template.spec.volumes[0].projected.sources[0].podCertificate.keyType: required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &components.CnpgPoolerConfig{Name: tt.app, Namespace: "data", Spec: tt.spec}
			_, err := c.Generate(stack.NewApplication(tt.app, "data", c))
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

// TestCnpgPoolerConfig_Generate_TemplateResources: the template's pod and
// container resources get admission's request/limit and hugepages checks
// before emission.
func TestCnpgPoolerConfig_Generate_TemplateResources(t *testing.T) {
	over := map[string]any{"requests": map[string]any{"cpu": "2"}, "limits": map[string]any{"cpu": "1"}}
	for _, tt := range []struct {
		name    string
		spec    map[string]any
		wantErr string
	}{
		{"container request above limit",
			map[string]any{"containers": []any{map[string]any{"name": "pgbouncer", "resources": over}}},
			`template.spec.containers[0] "pgbouncer": resources: cpu: request 2 must not exceed limit 1`},
		{"init container request above limit",
			map[string]any{"containers": []any{}, "initContainers": []any{map[string]any{"name": "init", "resources": over}}},
			`template.spec.initContainers[0] "init": resources: cpu: request 2 must not exceed limit 1`},
		{"pod request above limit",
			map[string]any{"containers": []any{}, "resources": over},
			"template.spec: resources: cpu: request 2 must not exceed limit 1"},
		{"container hugepages without cpu or memory",
			map[string]any{"containers": []any{map[string]any{"name": "pgbouncer", "resources": map[string]any{
				"requests": map[string]any{"hugepages-2Mi": "2Mi"}, "limits": map[string]any{"hugepages-2Mi": "2Mi"}}}}},
			`template.spec.containers[0] "pgbouncer": resources: hugepages require cpu or memory in requests or limits`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := minimalPooler()
			p["template"] = map[string]any{"spec": tt.spec}
			c := newCnpgPooler(t, p)
			_, err := c.Generate(stack.NewApplication("db-pooler", "data", c))
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestCnpgPoolerConfig_Generate_TemplateWithoutContainers: a metadata-only
// template serializes with containers: [] rather than null, which the API
// server would prune before checking the CRD's required containers. An
// authored container list is kept as written.
func TestCnpgPoolerConfig_Generate_TemplateWithoutContainers(t *testing.T) {
	for _, tt := range []struct {
		name     string
		template map[string]any
		want     string
	}{
		{"metadata only", map[string]any{"metadata": map[string]any{"labels": map[string]any{"team": "orders"}}}, `"containers":[]`},
		{"empty spec", map[string]any{"spec": map[string]any{}}, `"containers":[]`},
		{"authored container", map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "pgbouncer"}}}}, `"containers":[{"name":"pgbouncer"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := minimalPooler()
			p["template"] = tt.template
			raw, err := json.Marshal(generateCnpgPooler(t, newCnpgPooler(t, p)).Spec.Template)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(raw), tt.want) {
				t.Errorf("template = %s, want it to contain %s", raw, tt.want)
			}
		})
	}
}

// TestCnpgPoolerConfig_Generate_DoesNotAlias: the generated Pooler shares no
// map or pointer with the config.
func TestCnpgPoolerConfig_Generate_DoesNotAlias(t *testing.T) {
	p := minimalPooler()
	p["pgbouncer"] = map[string]any{"parameters": map[string]any{"max_client_conn": "10"}}
	c := newCnpgPooler(t, p)
	first := generateCnpgPooler(t, c)
	first.Spec.PgBouncer.Parameters["max_client_conn"] = "edited"
	if second := generateCnpgPooler(t, c); second.Spec.PgBouncer.Parameters["max_client_conn"] != "10" {
		t.Errorf("second render changed after editing the first: %+v", second.Spec.PgBouncer)
	}
}
