package registry_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests hold the provenance report to the values launcher's own code
// fills after the policy decided (go-kure/launcher#1234): reported as
// SourceBuiltin at the path the author would have written, under the owner of
// the properties the component was built from. They share the helpers of
// policy_default_provenance_test.go.

// The only post-policy step today fills the postgresql Cluster. Its config must
// stay a reporter, or the fills it writes read as authored.
var _ oam.BuiltinDefaultReporter = (*components.CnpgClusterConfig)(nil)

func TestBuiltinDefaults_PostgresqlOwnerShapes(t *testing.T) {
	backup := map[string]any{"objectStore": map[string]any{
		"destinationPath": "s3://my-bucket/postgres/",
		"s3Credentials":   map[string]any{"inheritFromIAMRole": true},
	}}
	cases := []struct {
		name  string
		props map[string]any
		owner owner
	}{
		{"no backup store", map[string]any{}, owner{component: "db"}},
		{"with a backup store", map[string]any{"backup": backup}, owner{component: "db", member: "cnpg-cluster"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, &defaultsPolicy{replicas: i32(3)}, oam.Component{Name: "db", Type: "postgresql", Properties: tc.props})
			got := entriesOf(prov, tc.owner)
			for path, ref := range map[string]string{"enablePDB": "postgresql.enablePDB", "storage.size": "postgresql.storage.size"} {
				want := oam.Source{Kind: oam.SourceBuiltin, Ref: ref}
				if got[path] != want {
					t.Errorf("%s of %+v = %+v, want %+v\nentries: %v", path, tc.owner, got[path], want, got)
				}
			}
		})
	}
}

// A size the author wrote carries the lowering rule's source (the rule moves
// `storageSize` to `storage.size`), and the fallback is not applied. The policy
// default is a policy fill; the builtin fallback claims the path in no other case.
func TestBuiltinDefaults_StorageSizeHasOneSource(t *testing.T) {
	cases := []struct {
		name   string
		props  map[string]any
		policy *defaultsPolicy
		want   oam.Source
	}{
		{"authored", map[string]any{"storageSize": "50Gi"}, &defaultsPolicy{},
			oam.Source{Kind: oam.SourceRendered, Ref: "component/postgresql@v1alpha1"}},
		{"policy default", map[string]any{}, &defaultsPolicy{store: "20Gi"},
			oam.Source{Kind: oam.SourcePolicy, Ref: "DefaultStorageSize"}},
		{"fallback", map[string]any{}, &defaultsPolicy{},
			oam.Source{Kind: oam.SourceBuiltin, Ref: "postgresql.storage.size"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, _ := policyProvenanceFor(t, tc.policy, oam.Component{Name: "db", Type: "postgresql", Properties: tc.props})
			if got := entriesOf(prov, owner{component: "db"})["storage.size"]; got != tc.want {
				t.Errorf("storage.size = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// enablePDB is decided from the instance count whatever the count is, so it is
// reported for one instance too, where the value is false.
func TestBuiltinDefaults_EnablePDBIsReportedForOneInstance(t *testing.T) {
	prov, _ := policyProvenanceFor(t, &defaultsPolicy{}, oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{"replicas": 1}})
	want := oam.Source{Kind: oam.SourceBuiltin, Ref: "postgresql.enablePDB"}
	if got := entriesOf(prov, owner{component: "db"})["enablePDB"]; got != want {
		t.Errorf("enablePDB = %+v, want %+v", got, want)
	}
}

// The report changes nothing the transform produces.
func TestBuiltinDefaults_ReportDoesNotChangeTheOutput(t *testing.T) {
	comp := oam.Component{Name: "db", Type: "postgresql", Properties: map[string]any{}}
	p := &defaultsPolicy{replicas: i32(2)}
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{comp}},
	}
	cluster, _, err := newTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default", Policy: p})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	_, reported := policyProvenanceFor(t, p, comp)
	if string(manifestsOf(t, cluster)) != string(reported) {
		t.Errorf("the generated objects differ with the report on")
	}
}
