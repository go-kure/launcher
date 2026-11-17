package kurel

import (
	"maps"
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// builtinsByPosition is every built-in handler and lowering rule kurel
// registers, by the position it is registered at and the type it claims.
func builtinsByPosition() map[string]map[string]any {
	out := map[string]map[string]any{"component": {}, "trait": {}, "policy": {}}
	for name, h := range builtinComponentHandlers() {
		out["component"][name] = h
	}
	for name, r := range builtinComponentLoweringRules() {
		out["component"][name] = r
	}
	for name, h := range builtinTraitHandlers() {
		out["trait"][name] = h
	}
	for name, r := range builtinTraitLoweringRules() {
		out["trait"][name] = r
	}
	for name, h := range builtinPolicyHandlers() {
		out["policy"][name] = h
	}
	return out
}

// builtinsDeclaringReadiness lists the built-ins allowed to set
// ContractMetadata.Readiness, as "<position>/<type>". Only postgresql declares
// it (go-kure/launcher#1144), and TestBuiltins_EveryOneDescribesItsContract
// requires that it does.
var builtinsDeclaringReadiness = map[string]bool{"component/postgresql": true}

// platformNeedsOfKind is the API group each CloudNativePG family kind needs the
// platform to serve (go-kure/launcher#1611): ten kinds project postgresql.cnpg.io
// objects and the object store projects a barmancloud.cnpg.io one.
var platformNeedsOfKind = map[string]string{
	"cnpg-backup":              "postgresql.cnpg.io",
	"cnpg-cluster":             "postgresql.cnpg.io",
	"cnpg-clusterimagecatalog": "postgresql.cnpg.io",
	"cnpg-database":            "postgresql.cnpg.io",
	"cnpg-databaserole":        "postgresql.cnpg.io",
	"cnpg-imagecatalog":        "postgresql.cnpg.io",
	"cnpg-objectstore":         "barmancloud.cnpg.io",
	"cnpg-pooler":              "postgresql.cnpg.io",
	"cnpg-publication":         "postgresql.cnpg.io",
	"cnpg-scheduledbackup":     "postgresql.cnpg.io",
	"cnpg-subscription":        "postgresql.cnpg.io",
}

// builtinsDeclaringPlatformNeeds lists the built-ins allowed to set
// ContractMetadata.PlatformNeeds, as "<position>/<type>": the CloudNativePG family
// kinds and the postgresql rule that lowers into them. A built-in that starts or
// stops declaring a need is a deliberate edit of this list.
func builtinsDeclaringPlatformNeeds() map[string]bool {
	allowed := map[string]bool{"component/postgresql": true}
	for kind := range platformNeedsOfKind {
		allowed["component/"+kind] = true
	}
	return allowed
}

// TestBuiltins_EveryOneDescribesItsContract: every built-in implements
// oam.ContractDescriber under the one scheme: the family is the type it is
// registered under, the version is builtin.ContractVersion, and the required
// capability keys are the type's own key exactly when its CapabilityRequired is
// true. A built-in added without metadata fails here.
func TestBuiltins_EveryOneDescribesItsContract(t *testing.T) {
	for position, byType := range builtinsByPosition() {
		for name, b := range byType {
			d, ok := b.(oam.ContractDescriber)
			if !ok {
				t.Errorf("%s %q (%T) does not implement oam.ContractDescriber", position, name, b)
				continue
			}
			meta := d.ContractMetadata()
			if meta.Family != name || meta.Version != builtin.ContractVersion {
				t.Errorf("%s %q: contract %s@%s, want %s@%s", position, name, meta.Family, meta.Version, name, builtin.ContractVersion)
			}
			if meta.Deprecated || meta.DeprecationMessage != "" {
				t.Errorf("%s %q is declared deprecated: %+v", position, name, meta)
			}
			var wantKeys []string
			if aware, ok := b.(oam.CapabilityAware); ok && aware.CapabilityRequired() {
				wantKeys = []string{name}
			}
			if !slices.Equal(meta.RequiredCapabilityKeys, wantKeys) {
				t.Errorf("%s %q: RequiredCapabilityKeys = %v, want %v", position, name, meta.RequiredCapabilityKeys, wantKeys)
			}
			// Checked before the postgresql skip below, so postgresql is not
			// exempt. A built-in that starts declaring readiness is added to
			// builtinsDeclaringReadiness (go-kure/launcher#1134) on purpose, and
			// one on the list must declare it.
			declares := len(meta.Readiness) > 0
			if allowed := builtinsDeclaringReadiness[position+"/"+name]; declares != allowed {
				t.Errorf("%s %q: declares readiness = %t (%+v), want %t (builtinsDeclaringReadiness)", position, name, declares, meta.Readiness, allowed)
			}
			// Likewise, in the same position: the need lists are exact in the tests
			// below, and a built-in outside the list declares none.
			declaresNeeds := len(meta.PlatformNeeds) > 0
			if allowed := builtinsDeclaringPlatformNeeds()[position+"/"+name]; declaresNeeds != allowed {
				t.Errorf("%s %q: declares platform needs = %t (%+v), want %t (builtinsDeclaringPlatformNeeds)", position, name, declaresNeeds, meta.PlatformNeeds, allowed)
			}
			if position == "component" && name == "postgresql" {
				continue // TestNewBuiltinTransformer_PublishesPostgresqlPrerequisites
			}
			if meta.Prerequisites != nil {
				t.Errorf("%s %q declares prerequisites %+v; only postgresql does", position, name, meta.Prerequisites)
			}
		}
	}
}

// TestNewBuiltinTransformer_PublishesPostgresqlPrerequisites: HandlerContracts
// publishes postgresql's backup and recovery prerequisites
// (go-kure/launcher#1027), exactly these, in this order: each names what it
// is, when it applies, and the operator and plugin carry the versions
// docs/postgresql-backup-recovery.md gives them.
func TestNewBuiltinTransformer_PublishesPostgresqlPrerequisites(t *testing.T) {
	got := newBuiltinTransformer().HandlerContracts().Components["postgresql"].Prerequisites
	const backup = "backup or recovery set"
	want := []oam.ContractPrerequisite{
		{Name: "CloudNativePG operator", Version: "1.26 or later; the objects follow the 1.30.1 API", When: backup},
		{Name: "Barman Cloud plugin barman-cloud.cloudnative-pg.io", Version: "v0.15.x", When: backup},
		{Name: "CustomResourceDefinition objectstores.barmancloud.cnpg.io", Version: "v1", When: backup},
		{Name: "the bucket, and the Secrets s3Credentials and endpointCA name", When: backup},
		{Name: "the ObjectStore recovery.source.objectStoreName names", When: "recovery.source.objectStoreName set"},
		{Name: "the Secret recovery.secret.name names, with the owner's credentials", When: "recovery.secret.name set"},
	}
	if !slices.Equal(got, want) {
		t.Errorf(`HandlerContracts().Components["postgresql"].Prerequisites =
%+v
want
%+v`, got, want)
	}
}

// TestNewBuiltinTransformer_PublishesPostgresqlPlatformNeeds: HandlerContracts
// publishes the two capabilities postgresql's output can need (go-kure/launcher#1611),
// exactly these, in this order: the CloudNativePG API always, and the Barman
// Cloud API for backup and recovery, whose When says so.
func TestNewBuiltinTransformer_PublishesPostgresqlPlatformNeeds(t *testing.T) {
	got := newBuiltinTransformer().HandlerContracts().Components["postgresql"].PlatformNeeds
	want := []oam.PlatformNeed{
		{Capability: "postgresql.cnpg.io"},
		{Capability: "barmancloud.cnpg.io", When: "backup or recovery set"},
	}
	if !slices.Equal(got, want) {
		t.Errorf(`HandlerContracts().Components["postgresql"].PlatformNeeds =
%+v
want
%+v`, got, want)
	}
}

// clusterComponent is an authored cnpg-cluster component "pg" with extra
// properties appended (indented by eight spaces).
func clusterComponent(extra string) string {
	return "    - name: pg\n      type: cnpg-cluster\n      properties:\n        instances: 1\n        storage:\n          size: 1Gi\n" + extra
}

// TestBuiltinTransform_PostgresqlPlatformNeeds: a real build reports what the
// lowered document emits (go-kure/launcher#1611). A postgresql with neither backup
// nor recovery has a Cluster that names no plugin, so the Barman Cloud API is not
// among its needs; with backup, with an inline recovery store, or with a recovery
// from an existing store by name it is, though the last emits no ObjectStore; an
// authored Cluster reports it exactly when it names the plugin, enabled; an
// application with no CloudNativePG kind reports none.
func TestBuiltinTransform_PostgresqlPlatformNeeds(t *testing.T) {
	const header = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
`
	tests := []struct {
		name string
		app  string
		want []string
	}{
		{
			name: "without backup or recovery",
			app:  header + "    - name: db\n      type: postgresql\n      properties:\n        replicas: 1\n",
			want: []string{"postgresql.cnpg.io"},
		},
		{
			name: "recovery from an inline store, no backup",
			app:  postgresqlRecoveryApp("", "", ""),
			want: []string{"barmancloud.cnpg.io", "postgresql.cnpg.io"},
		},
		{
			// The Cluster names the plugin and an ObjectStore that already exists:
			// no ObjectStore is emitted, and the Barman Cloud API is still needed.
			name: "recovery from an existing store by name, no backup",
			app:  header + "    - name: db\n      type: postgresql\n      properties:\n        recovery:\n          source:\n            serverName: origin\n            objectStoreName: origin-store\n",
			want: []string{"barmancloud.cnpg.io", "postgresql.cnpg.io"},
		},
		{
			name: "with backup",
			app:  postgresqlMembersApp("", ""),
			want: []string{"barmancloud.cnpg.io", "postgresql.cnpg.io"},
		},
		{
			name: "an authored Cluster without the plugin",
			app:  header + clusterComponent(""),
			want: []string{"postgresql.cnpg.io"},
		},
		{
			name: "an authored Cluster that names the plugin as its WAL archiver",
			app:  header + clusterComponent("        plugins:\n          - name: barman-cloud.cloudnative-pg.io\n            isWALArchiver: true\n            parameters:\n              barmanObjectName: existing\n"),
			want: []string{"barmancloud.cnpg.io", "postgresql.cnpg.io"},
		},
		{
			name: "an authored Cluster whose plugin entry is disabled",
			app:  header + clusterComponent("        plugins:\n          - name: barman-cloud.cloudnative-pg.io\n            enabled: false\n"),
			want: []string{"postgresql.cnpg.io"},
		},
		{
			// The build drops a null key before it decodes, so `plugin: null` beside
			// `Plugin` leaves the plugin in the emitted recovery source.
			name: "an authored Cluster whose recovery plugin is spelled Plugin beside a null plugin",
			app:  header + clusterComponent("        bootstrap:\n          recovery:\n            source: origin\n        externalClusters:\n          - name: origin\n            Plugin:\n              name: barman-cloud.cloudnative-pg.io\n              parameters:\n                barmanObjectName: existing\n                serverName: origin\n            plugin: null\n"),
			want: []string{"barmancloud.cnpg.io", "postgresql.cnpg.io"},
		},
		{
			name: "an authored Cluster that names another plugin",
			app:  header + clusterComponent("        plugins:\n          - name: other.example.io\n"),
			want: []string{"postgresql.cnpg.io"},
		},
		{
			name: "no CloudNativePG kind",
			app:  header + "    - name: api\n      type: webservice\n      properties:\n        image: ghcr.io/example/api:v1.0.0\n        port: 9090\n",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, result, err := transformWithBuiltins(t, tc.app)
			if err != nil {
				t.Fatalf("transforming: %v", err)
			}
			if !slices.Equal(result.PlatformNeeds, tc.want) {
				t.Errorf("PlatformNeeds = %#v, want %#v", result.PlatformNeeds, tc.want)
			}
		})
	}
}

// TestBuiltins_PlatformNeedsOfTheCloudNativePGKinds: each of the eleven kinds
// publishes exactly one need, the API group its output belongs to, with no When.
func TestBuiltins_PlatformNeedsOfTheCloudNativePGKinds(t *testing.T) {
	contracts := newBuiltinTransformer().HandlerContracts().Components
	for kind, group := range platformNeedsOfKind {
		want := []oam.PlatformNeed{{Capability: group}}
		if got := contracts[kind].PlatformNeeds; !slices.Equal(got, want) {
			t.Errorf("component %q: PlatformNeeds = %+v, want %+v", kind, got, want)
		}
	}
}

// TestNewBuiltinTransformer_PublishesPostgresqlReadiness: HandlerContracts
// publishes one readiness entry for each kind postgresql lowers into
// (go-kure/launcher#1144), exactly these, in the order the rule declares its
// targets. Each signal is read from the status type of the CloudNativePG and
// Barman Cloud versions go.mod pins.
func TestNewBuiltinTransformer_PublishesPostgresqlReadiness(t *testing.T) {
	got := newBuiltinTransformer().HandlerContracts().Components["postgresql"].Readiness
	const cnpg, barman = "postgresql.cnpg.io", "barmancloud.cnpg.io"
	want := []oam.ContractReadiness{
		{Group: cnpg, Kind: "Cluster", NamePattern: "the component name, or clusterObjectName", Condition: "Ready", Generic: oam.GenericJudges},
		{Group: barman, Kind: "ObjectStore", NamePattern: "the component name, or backup.objectStoreObjectName; <component>-recovery-source for an inline recovery.source.objectStore", Generic: oam.GenericCannotJudge},
		{Group: cnpg, Kind: "Pooler", NamePattern: "<component>-pooler, or poolerName", Field: "status.phase", Value: "active", Generic: oam.GenericCannotJudge},
		{Group: cnpg, Kind: "Database", NamePattern: "<component>-<database>, or databases[].objectName", Field: "status.applied", Value: "true", Generic: oam.GenericCannotJudge},
		{Group: cnpg, Kind: "ScheduledBackup", NamePattern: "<component>-<schedule>", Generic: oam.GenericCannotJudge},
	}
	if !slices.Equal(got, want) {
		t.Errorf(`HandlerContracts().Components["postgresql"].Readiness =
%+v
want
%+v`, got, want)
	}
}

// TestBuiltins_ReadinessFollowsTheDeclarationRules: every readiness entry a
// built-in declares obeys the rules oam.ContractReadiness states: a Kind, one of
// Condition or Field with Value only beside Field, GenericJudges only on a
// "Ready" condition, and an entry with neither signal says GenericCannotJudge.
func TestBuiltins_ReadinessFollowsTheDeclarationRules(t *testing.T) {
	for position, byType := range builtinsByPosition() {
		for name, b := range byType {
			d, ok := b.(oam.ContractDescriber)
			if !ok {
				continue
			}
			for _, r := range d.ContractMetadata().Readiness {
				switch {
				case r.Kind == "":
					t.Errorf("%s %q: a readiness entry has no Kind: %+v", position, name, r)
				case r.Condition != "" && r.Field != "":
					t.Errorf("%s %q: %s declares both Condition and Field: %+v", position, name, r.Kind, r)
				case r.Value != "" && r.Field == "":
					t.Errorf("%s %q: %s has a Value without a Field: %+v", position, name, r.Kind, r)
				case r.Generic == oam.GenericJudges && r.Condition != "Ready":
					t.Errorf("%s %q: %s says a generic check judges it but its signal is not the Ready condition: %+v", position, name, r.Kind, r)
				case r.Condition == "" && r.Field == "" && r.Generic != oam.GenericCannotJudge:
					t.Errorf("%s %q: %s gives no signal and does not say cannot-judge: %+v", position, name, r.Kind, r)
				}
			}
		}
	}
}

// TestNewBuiltinTransformer_HandlerContractsListsEveryBuiltin: what
// HandlerContracts publishes for kurel's transformer is exactly the registered
// built-ins, each position in its own map.
func TestNewBuiltinTransformer_HandlerContractsListsEveryBuiltin(t *testing.T) {
	set := newBuiltinTransformer().HandlerContracts()
	want := builtinsByPosition()
	for position, got := range map[string]map[string]oam.ContractMetadata{
		"component": set.Components, "trait": set.Traits, "policy": set.Policies,
	} {
		gotNames := slices.Sorted(maps.Keys(got))
		wantNames := slices.Sorted(maps.Keys(want[position]))
		if !slices.Equal(gotNames, wantNames) {
			t.Errorf("HandlerContracts() %s types = %v\nwant %v", position, gotNames, wantNames)
		}
	}
}

// TestNewBuiltinTransformer_IsComplete: every built-in lowering rule declares
// the types it lowers into, and kurel registers all of them.
func TestNewBuiltinTransformer_IsComplete(t *testing.T) {
	for name, r := range builtinComponentLoweringRules() {
		if _, ok := r.(oam.LoweringTargetDeclarer); !ok {
			t.Errorf("component rule %q (%T) does not implement oam.LoweringTargetDeclarer", name, r)
		}
	}
	for name, r := range builtinTraitLoweringRules() {
		if _, ok := r.(oam.LoweringTargetDeclarer); !ok {
			t.Errorf("trait rule %q (%T) does not implement oam.LoweringTargetDeclarer", name, r)
		}
	}
	if err := newBuiltinTransformer().Seal(); err != nil {
		t.Errorf("Seal() on kurel's transformer: %v", err)
	}
}

// TestTransform_WebserviceWithoutServiceIsRefused is the acceptance case: with
// "webservice" registered and "service" not, the transform fails with an error
// naming both, before it looks at the document.
func TestTransform_WebserviceWithoutServiceIsRefused(t *testing.T) {
	handlers := builtinComponentHandlers()
	delete(handlers, "service")
	tr := oam.NewTransformer(handlers, nil)
	tr.RegisterBuiltinComponentLowering(builtinComponentLoweringRules()["webservice"])
	for name, h := range builtinTraitHandlers() {
		tr.RegisterBuiltinTrait(name, h)
	}

	const want = `registry incomplete: lowering rule component/webservice@v1alpha1 lowers into component type "service", which is not registered`
	if err := tr.Seal(); err == nil || err.Error() != want {
		t.Errorf("Seal() = %v\nwant     %s", err, want)
	}
	// A worker: the document does not use the rule that is refused.
	_, err := tr.Transform(workerApp(map[string]any{"image": "nginx:1"}), oam.TransformContext{Namespace: "ns", Domain: kurelDomain})
	if err == nil || err.Error() != want {
		t.Errorf("Transform err = %v\nwant            %s", err, want)
	}
}

// TestTransform_BuiltinsRegisterInAnyOrder: the built-ins registered in the
// reverse of kurel's order (rules last in kurel's terms, first here; every
// component handler after the rule that lowers into it) still seal and
// transform.
func TestTransform_BuiltinsRegisterInAnyOrder(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	for _, r := range builtinTraitLoweringRules() {
		tr.RegisterBuiltinTraitLowering(r)
	}
	for _, r := range builtinComponentLoweringRules() {
		tr.RegisterBuiltinComponentLowering(r)
	}
	for name, h := range builtinPolicyHandlers() {
		tr.RegisterPolicy(name, h)
	}
	for name, h := range builtinTraitHandlers() {
		tr.RegisterBuiltinTrait(name, h)
	}
	for name, h := range builtinComponentHandlers() {
		tr.RegisterComponent(name, h)
	}
	if err := tr.Seal(); err != nil {
		t.Fatalf("Seal(): %v", err)
	}
	if _, err := tr.Transform(webserviceApp(map[string]any{"image": "nginx:1"}), oam.TransformContext{Namespace: "ns", Domain: kurelDomain}); err != nil {
		t.Errorf("Transform: %v", err)
	}
}

// onlyDeclaredTargets is a transformer holding the named built-in component
// rules and, of the built-in handlers, only the types those rules declare they
// lower into.
func onlyDeclaredTargets(t *testing.T, rules ...string) *oam.Transformer {
	t.Helper()
	tr := oam.NewTransformer(nil, nil)
	var targets oam.LoweringTargets
	for _, name := range rules {
		r := builtinComponentLoweringRules()[name]
		tr.RegisterBuiltinComponentLowering(r)
		declared := r.(oam.LoweringTargetDeclarer).LoweringTargets()
		targets.ComponentTypes = append(targets.ComponentTypes, declared.ComponentTypes...)
		targets.TraitTypes = append(targets.TraitTypes, declared.TraitTypes...)
		targets.PolicyTypes = append(targets.PolicyTypes, declared.PolicyTypes...)
	}
	for _, typ := range slices.Compact(slices.Sorted(slices.Values(targets.ComponentTypes))) {
		tr.RegisterComponent(typ, builtinComponentHandlers()[typ])
	}
	for _, typ := range slices.Compact(slices.Sorted(slices.Values(targets.TraitTypes))) {
		tr.RegisterBuiltinTrait(typ, builtinTraitHandlers()[typ])
	}
	for _, typ := range slices.Compact(slices.Sorted(slices.Values(targets.PolicyTypes))) {
		tr.RegisterPolicy(typ, builtinPolicyHandlers()[typ])
	}
	return tr
}

// TestBuiltinRules_DeclaredTargetsAreEnoughToTransform ties each declaration to
// what the rule emits: with nothing registered but a rule and the types it
// declares, a document that uses the rule transforms. A type the rule emitted
// and did not declare would have no handler. It covers the documents below, not
// every input of a rule.
func TestBuiltinRules_DeclaredTargetsAreEnoughToTransform(t *testing.T) {
	const header = "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Application\nmetadata:\n  name: shop\n  namespace: shop\nspec:\n  components:\n"
	for name, tc := range map[string]struct {
		rules []string
		doc   string
	}{
		"webservice": {[]string{"webservice"}, header + `    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 9090
        volumes:
          - name: cache
            type: pvc
            mountPath: /cache
            size: 1Gi
`},
		"worker": {[]string{"worker"}, header + `    - name: queue
      type: worker
      properties:
        image: ghcr.io/example/queue:v1.0.0
        volumes:
          - name: cache
            type: pvc
            mountPath: /cache
            size: 1Gi
`},
		"helm, generated HelmRepository": {[]string{"helm"}, helmAppHeader},
		"helm, generated OCIRepository": {[]string{"helm"}, header + `    - name: chart
      type: helm
      properties:
        version: 1.2.3
        source:
          url: oci://registry.example.com/charts/shop
`},
		"helm, values in a ConfigMap": {[]string{"helm"}, header + `    - name: podinfo
      type: helm
      properties:
        chart: podinfo
        source:
          kind: HelmRepository
          name: podinfo
        valuesMode: configMap
        values:
          replicaCount: 2
`},
		"postgresql, split and ordered": {[]string{"postgresql", "webservice"}, postgresqlMembersApp("", postgresqlOrderPolicy)},
	} {
		t.Run(name, func(t *testing.T) {
			tr := onlyDeclaredTargets(t, tc.rules...)
			if err := tr.Seal(); err != nil {
				t.Fatalf("Seal(): %v", err)
			}
			app, err := oam.ParseWithExtraTypes([]byte(tc.doc), nil, tr.LowerableTypes())
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if _, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Domain: kurelDomain}); err != nil {
				t.Errorf("transform with only the declared targets registered: %v", err)
			}
		})
	}
}
