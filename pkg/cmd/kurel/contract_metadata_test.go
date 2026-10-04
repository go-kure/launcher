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
	tr.RegisterComponentLowering(builtinComponentLoweringRules()["webservice"])
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
		tr.RegisterComponentLowering(r)
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
		tr.RegisterComponentLowering(r)
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
