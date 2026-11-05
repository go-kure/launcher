package registry_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/launcher/pkg/oam"
)

// These tests hold the provenance report to the policy defaults the built-in
// handlers apply (go-kure/launcher#1234): a value the author left out and the
// environment policy filled is reported as SourcePolicy at the path the author
// would have written. They run the registry's whole pipeline, so the owner a
// handler's default is recorded under is the one a consumer reads.

// defaultsPolicy is a Policy with the Default* values a test sets and nothing
// else; every other accessor is NoopPolicy's.
type defaultsPolicy struct {
	oam.NoopPolicy
	replicas, scalerMin, scalerMax                          *int32
	cpuRequest, memoryRequest, cpuLimit, memoryLimit, store string
	maxReplicas                                             *int32
}

func (p *defaultsPolicy) DefaultReplicas() *int32          { return p.replicas }
func (p *defaultsPolicy) DefaultScalerMinReplicas() *int32 { return p.scalerMin }
func (p *defaultsPolicy) DefaultScalerMaxReplicas() *int32 { return p.scalerMax }
func (p *defaultsPolicy) DefaultCPURequest() string        { return p.cpuRequest }
func (p *defaultsPolicy) DefaultMemoryRequest() string     { return p.memoryRequest }
func (p *defaultsPolicy) DefaultCPULimit() string          { return p.cpuLimit }
func (p *defaultsPolicy) DefaultMemoryLimit() string       { return p.memoryLimit }
func (p *defaultsPolicy) DefaultStorageSize() string       { return p.store }
func (p *defaultsPolicy) MaxReplicas() *int32              { return p.maxReplicas }

func i32(v int32) *int32 { return &v }

// owner names the property map an entry belongs to.
type owner struct{ component, member, trait string }

func policyProvenanceFor(t *testing.T, p oam.Policy, comps ...oam.Component) (*oam.Provenance, []byte) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	tr := newTransformer()
	if err := tr.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("ValidateAuthoredProperties: %v", err)
	}
	cluster, _, prov, err := tr.TransformWithProvenance(app, oam.TransformContext{Namespace: "default", Policy: p})
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	return prov, manifestsOf(t, cluster)
}

// manifestsOf generates every application of cluster and marshals the objects.
// The cluster itself is not compared: its application configs are wrapped by
// the engine in a type whose fields are unexported, so marshalling the cluster
// would hide the values a policy default fills.
func manifestsOf(t *testing.T, cluster *stack.Cluster) []byte {
	t.Helper()
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	out, err := yaml.Marshal(apps)
	if err != nil {
		t.Fatalf("marshal the generated objects: %v", err)
	}
	return out
}

// entriesOf returns the entries of one owner by path.
func entriesOf(p *oam.Provenance, o owner) map[string]oam.Source {
	out := map[string]oam.Source{}
	for _, e := range p.Entries {
		if e.Component == o.component && e.Member == o.member && e.Trait == o.trait {
			out[e.Path] = e.Source
		}
	}
	return out
}

// policyEntries returns the entries of any owner whose kind is policy, keyed
// "component/member/trait:path", so a test can say there is none anywhere.
func policyEntries(p *oam.Provenance) map[string]oam.Source {
	out := map[string]oam.Source{}
	for _, e := range p.Entries {
		if e.Source.Kind == oam.SourcePolicy {
			out[e.Component+"/"+e.Member+"/"+e.Trait+":"+e.Path] = e.Source
		}
	}
	return out
}

func workload(name, typ string, extra map[string]any) oam.Component {
	props := map[string]any{"image": "ghcr.io/org/app:v1"}
	for k, v := range extra {
		props[k] = v
	}
	return oam.Component{Name: name, Type: typ, Properties: props}
}

// policyDefaultCase is one default site: the accessor that supplies the value,
// a component that leaves the value out, the owner and the path it is reported
// under.
type policyDefaultCase struct {
	name     string
	accessor string
	comp     oam.Component
	policy   *defaultsPolicy
	owner    owner
	path     string
}

// policyDefaultCases lists a row for every Default* accessor of oam.Policy,
// and for every handler that applies one. TestPolicyDefaults_EveryAccessorHasARow
// fails for an accessor with none.
func policyDefaultCases() []policyDefaultCase {
	var cases []policyDefaultCase
	// Resource defaults on every kind that has them. cnpg-cluster is reached
	// through a postgresql component.
	kinds := []struct {
		typ   string
		extra map[string]any
	}{
		{"deployment", nil},
		{"statefulset", nil},
		{"daemonset", nil},
		{"cronjob", map[string]any{"schedule": "0 2 * * *"}},
		{"job", nil},
	}
	for _, k := range kinds {
		comp := workload("app", k.typ, k.extra)
		for _, r := range []struct {
			accessor string
			path     string
			policy   *defaultsPolicy
		}{
			{"DefaultCPURequest", "resources.requests.cpu", &defaultsPolicy{cpuRequest: "250m"}},
			{"DefaultMemoryRequest", "resources.requests.memory", &defaultsPolicy{memoryRequest: "256Mi"}},
			{"DefaultCPULimit", "resources.limits.cpu", &defaultsPolicy{cpuLimit: "500m"}},
			{"DefaultMemoryLimit", "resources.limits.memory", &defaultsPolicy{memoryLimit: "512Mi"}},
		} {
			cases = append(cases, policyDefaultCase{
				name: k.typ + "/" + r.accessor, accessor: r.accessor, comp: comp,
				policy: r.policy, owner: owner{component: "app"}, path: r.path,
			})
		}
	}
	for _, k := range []string{"deployment", "statefulset"} {
		cases = append(cases, policyDefaultCase{
			name: k + "/DefaultReplicas", accessor: "DefaultReplicas", comp: workload("app", k, nil),
			policy: &defaultsPolicy{replicas: i32(3)}, owner: owner{component: "app"}, path: "replicas",
		})
	}
	// A postgresql component lowers to a cnpg Cluster; the policy runs on the
	// Cluster, so the entries are on its owner and its property spellings.
	db := oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{}}
	for _, r := range []struct {
		accessor string
		path     string
		policy   *defaultsPolicy
	}{
		{"DefaultReplicas", "instances", &defaultsPolicy{replicas: i32(3)}},
		{"DefaultCPURequest", "resources.requests.cpu", &defaultsPolicy{cpuRequest: "250m"}},
		{"DefaultMemoryRequest", "resources.requests.memory", &defaultsPolicy{memoryRequest: "256Mi"}},
		{"DefaultCPULimit", "resources.limits.cpu", &defaultsPolicy{cpuLimit: "500m"}},
		{"DefaultMemoryLimit", "resources.limits.memory", &defaultsPolicy{memoryLimit: "512Mi"}},
		{"DefaultStorageSize", "storage.size", &defaultsPolicy{store: "20Gi"}},
	} {
		cases = append(cases, policyDefaultCase{
			name: "postgresql/" + r.accessor, accessor: r.accessor, comp: db,
			policy: r.policy, owner: owner{component: "db"}, path: r.path,
		})
	}
	// Storage: the pvc kind and the pvc trait share the claim policy.
	cases = append(cases, policyDefaultCase{
		name: "persistentvolumeclaim/DefaultStorageSize", accessor: "DefaultStorageSize",
		comp:   oam.Component{Name: "data", Type: "persistentvolumeclaim", Properties: map[string]any{}},
		policy: &defaultsPolicy{store: "20Gi"}, owner: owner{component: "data"}, path: "size",
	})
	withTrait := func(typ string, props map[string]any) oam.Component {
		c := workload("app", "deployment", nil)
		c.Traits = []oam.Trait{{Type: typ, Properties: props}}
		return c
	}
	cases = append(cases,
		policyDefaultCase{
			name: "pvc trait/DefaultStorageSize", accessor: "DefaultStorageSize",
			comp:   withTrait("pvc", map[string]any{"name": "data"}),
			policy: &defaultsPolicy{store: "20Gi"}, owner: owner{component: "app", trait: "pvc"}, path: "size",
		},
		policyDefaultCase{
			name: "scaler trait/DefaultScalerMinReplicas", accessor: "DefaultScalerMinReplicas",
			comp:   withTrait("scaler", map[string]any{"maxReplicas": 5}),
			policy: &defaultsPolicy{scalerMin: i32(2)}, owner: owner{component: "app", trait: "scaler"}, path: "minReplicas",
		},
		policyDefaultCase{
			name: "scaler trait/DefaultScalerMaxReplicas", accessor: "DefaultScalerMaxReplicas",
			comp:   withTrait("scaler", map[string]any{"minReplicas": 1}),
			policy: &defaultsPolicy{scalerMax: i32(6)}, owner: owner{component: "app", trait: "scaler"}, path: "maxReplicas",
		},
	)
	return cases
}

// A default the author left out is reported once, as policy, under the accessor
// that supplied it; nothing else is reported as policy.
func TestPolicyDefaults_ReportedWhereApplied(t *testing.T) {
	for _, tc := range policyDefaultCases() {
		t.Run(tc.name, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, tc.policy, tc.comp)
			want := oam.Source{Kind: oam.SourcePolicy, Ref: tc.accessor}
			if got := entriesOf(prov, tc.owner)[tc.path]; got != want {
				t.Errorf("%s of %+v: got %+v, want %+v\npolicy entries: %v", tc.path, tc.owner, got, want, policyEntries(prov))
			}
			for key, src := range policyEntries(prov) {
				if src.Ref != tc.accessor {
					t.Errorf("entry %s names accessor %q; only %q is set", key, src.Ref, tc.accessor)
				}
			}
		})
	}
}

// An authored value is never reported as policy, and its source stays authored.
func TestPolicyDefaults_AuthoredValueIsNotReported(t *testing.T) {
	p := &defaultsPolicy{replicas: i32(3), cpuRequest: "250m", memoryRequest: "256Mi", cpuLimit: "500m", memoryLimit: "512Mi"}
	comp := workload("app", "deployment", map[string]any{
		"replicas":  2,
		"resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "64Mi"}, "limits": map[string]any{"cpu": "1", "memory": "1Gi"}},
	})
	prov, _ := policyProvenanceFor(t, p, comp)
	if got := policyEntries(prov); len(got) != 0 {
		t.Errorf("authored values reported as policy: %v", got)
	}
	e := entriesOf(prov, owner{component: "app"})
	for _, path := range []string{"replicas", "resources.requests.cpu", "resources.limits.memory"} {
		if e[path].Kind != oam.SourceAuthored {
			t.Errorf("%s = %+v, want authored", path, e[path])
		}
	}
}

// `unlimited` is the author's opt-out of a limit: the policy default is not
// applied, so there is nothing to report.
func TestPolicyDefaults_UnlimitedLimitIsNotReported(t *testing.T) {
	p := &defaultsPolicy{cpuLimit: "500m", memoryLimit: "512Mi"}
	comp := workload("app", "deployment", map[string]any{
		"resources": map[string]any{"limits": map[string]any{"cpu": "unlimited", "memory": "unlimited"}},
	})
	prov, _ := policyProvenanceFor(t, p, comp)
	if got := policyEntries(prov); len(got) != 0 {
		t.Errorf("an unlimited limit reported as policy: %v", got)
	}
}

// A request default above the author's limit is fixed at the limit instead: the
// value is not the policy's, but the policy default caused it.
func TestPolicyDefaults_RequestCappedToTheLimit(t *testing.T) {
	p := &defaultsPolicy{cpuRequest: "2"}
	comp := workload("app", "deployment", map[string]any{
		"resources": map[string]any{"limits": map[string]any{"cpu": "500m"}},
	})
	prov, _ := policyProvenanceFor(t, p, comp)
	want := oam.Source{Kind: oam.SourcePolicy, Ref: "DefaultCPURequest", Detail: "capped-to-limit"}
	if got := entriesOf(prov, owner{component: "app"})["resources.requests.cpu"]; got != want {
		t.Errorf("resources.requests.cpu = %+v, want %+v", got, want)
	}
}

// Under a scaler the HorizontalPodAutoscaler owns the count: the deployment
// applies no replica default (go-kure/launcher#1179), so the report has none;
// the same deployment without a scaler has one.
func TestPolicyDefaults_ScaledDeploymentHasNoReplicasEntry(t *testing.T) {
	p := &defaultsPolicy{replicas: i32(3)}
	scaled := workload("app", "deployment", nil)
	scaled.Traits = []oam.Trait{{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 5}}}
	prov, _ := policyProvenanceFor(t, p, scaled)
	if got := policyEntries(prov); len(got) != 0 {
		t.Errorf("a scaled deployment reports a policy default: %v", got)
	}
	prov, _ = policyProvenanceFor(t, p, workload("app", "deployment", nil))
	if got := entriesOf(prov, owner{component: "app"})["replicas"]; got.Kind != oam.SourcePolicy {
		t.Errorf("an unscaled deployment: replicas = %+v, want policy", got)
	}
}

// webservice emits a deployment and a same-name service, so its deployment is a
// member of a sibling group. worker lowers to a same-name deployment and
// serviceaccount unless serviceAccountName is authored (the components README's
// component table: "Lowered to a same-name `deployment` and (unless
// `serviceAccountName` is authored) `serviceaccount` group"), and a name a rule
// emits twice is one sibling group, whose members the report keys by type. So
// the worker's deployment owner carries member "deployment" too. An earlier
// version of this test expected an empty member for the worker, which was wrong.
// With serviceAccountName authored the worker generates no account, emits the
// deployment alone, and the owner's member is empty. The default is reported on
// that deployment's owner in each case.
func TestPolicyDefaults_WebserviceAndWorkerOwnerShapes(t *testing.T) {
	p := &defaultsPolicy{replicas: i32(3)}
	cases := []struct {
		name  string
		typ   string
		extra map[string]any
		owner owner
	}{
		{"webservice", "webservice", map[string]any{"port": 8080}, owner{component: "app", member: "deployment"}},
		{"worker", "worker", nil, owner{component: "app", member: "deployment"}},
		{"worker_with_authored_service_account", "worker", map[string]any{"serviceAccountName": "existing"}, owner{component: "app"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]any{}
			for k, v := range tc.extra {
				extra[k] = v
			}
			prov, _ := policyProvenanceFor(t, p, workload("app", tc.typ, extra))
			want := oam.Source{Kind: oam.SourcePolicy, Ref: "DefaultReplicas"}
			if got := entriesOf(prov, tc.owner)["replicas"]; got != want {
				t.Errorf("replicas of %+v = %+v, want %+v\npolicy entries: %v", tc.owner, got, want, policyEntries(prov))
			}
			if got := policyEntries(prov); len(got) != 1 {
				t.Errorf("want exactly one policy entry, got %v", got)
			}
		})
	}
}

// A postgresql component with a backup store is emitted as a sibling group, so
// the Cluster's owner carries its member; without one the member is empty. No
// entry is under the postgresql type in either case.
func TestPolicyDefaults_PostgresqlOwnerShapes(t *testing.T) {
	p := &defaultsPolicy{replicas: i32(3)}
	cases := []struct {
		name  string
		props map[string]any
		owner owner
	}{
		{"no backup store", map[string]any{}, owner{component: "db"}},
		{"with a backup store", map[string]any{"backup": map[string]any{"objectStore": map[string]any{
			"destinationPath": "s3://my-bucket/postgres/",
			"s3Credentials":   map[string]any{"inheritFromIAMRole": true},
		}}}, owner{component: "db", member: "cnpg-cluster"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, p, oam.Component{Name: "db", Type: "postgresql", Properties: tc.props})
			want := oam.Source{Kind: oam.SourcePolicy, Ref: "DefaultReplicas"}
			if got := entriesOf(prov, tc.owner)["instances"]; got != want {
				t.Errorf("instances of %+v = %+v, want %+v\npolicy entries: %v", tc.owner, got, want, policyEntries(prov))
			}
		})
	}
}

// The report changes nothing the transform produces.
func TestPolicyDefaults_ReportDoesNotChangeTheOutput(t *testing.T) {
	for _, tc := range policyDefaultCases() {
		t.Run(tc.name, func(t *testing.T) {
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
				Spec:       oam.ApplicationSpec{Components: []oam.Component{tc.comp}},
			}
			cluster, _, err := newTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default", Policy: tc.policy})
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			plain := manifestsOf(t, cluster)
			_, reported := policyProvenanceFor(t, tc.policy, tc.comp)
			if string(plain) != string(reported) {
				t.Errorf("the generated objects differ with the report on")
			}
			// Control: the compared bytes do hold the default. Without the policy
			// default the transform either refuses (a size or a scaler bound has no
			// other source) or generates different objects.
			bare, _, err := newTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default", Policy: &defaultsPolicy{}})
			if err == nil {
				if without := manifestsOf(t, bare); string(without) == string(plain) {
					t.Errorf("the generated objects do not change when the policy default is removed: the comparison cannot see %s", tc.accessor)
				}
			}
		})
	}
}

// Every Default* accessor of oam.Policy has a row in policyDefaultCases, and
// every row names an accessor that exists: a default added to the interface
// without a reporter fails here.
func TestPolicyDefaults_EveryAccessorHasARow(t *testing.T) {
	var accessors []string
	pt := reflect.TypeOf((*oam.Policy)(nil)).Elem()
	for i := 0; i < pt.NumMethod(); i++ {
		if name := pt.Method(i).Name; strings.HasPrefix(name, "Default") {
			accessors = append(accessors, name)
		}
	}
	rows := map[string]bool{}
	for _, tc := range policyDefaultCases() {
		rows[tc.accessor] = true
	}
	for _, a := range accessors {
		if !rows[a] {
			t.Errorf("Policy accessor %s has no reporter: add a row to policyDefaultCases (pkg/oam/builtin/registry/policy_default_provenance_test.go) and report the value from the site that applies it, via PolicyDefaultReporter (pkg/oam/policy.go)", a)
		}
	}
	for a := range rows {
		if !slices.Contains(accessors, a) {
			t.Errorf("policyDefaultCases names %s, which oam.Policy no longer has", a)
		}
	}
}
