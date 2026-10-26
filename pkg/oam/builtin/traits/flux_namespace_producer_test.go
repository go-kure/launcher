package traits_test

import (
	"go/types"
	"sort"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestFluxNamespaceInput_EveryImplementerSaysWhetherItProduces: every built-in
// type whose method set implements pkg/oam.fluxNamespaceInput, by a method of
// its own or a promoted one, also implements pkg/oam.fluxNamespaceProducer with
// its exact signature. The late-maker check (go-kure/launcher#1039) admits a
// trait object that does not say, so a new input without the method would be
// admitted silently, wherever its trait makes the object.
func TestFluxNamespaceInput_EveryImplementerSaysWhetherItProduces(t *testing.T) {
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedTypes}, "github.com/go-kure/launcher/pkg/oam/builtin/...")
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("packages.Load reported errors")
	}
	str := types.Typ[types.String]
	input := methodInterface("FluxNamespaceInput", nil, []*types.Var{
		types.NewParam(0, nil, "kind", str), types.NewParam(0, nil, "name", str),
	})
	producer := methodInterface("ProducesFluxNamespaceInput", nil, []*types.Var{
		types.NewParam(0, nil, "", types.Typ[types.Bool]),
	})
	var inputs, missing []string
	for _, p := range pkgs {
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || tn.IsAlias() {
				continue
			}
			if _, isIface := tn.Type().Underlying().(*types.Interface); isIface {
				continue
			}
			ptr := types.NewPointer(tn.Type())
			if !types.Implements(ptr, input) {
				continue
			}
			id := p.PkgPath + "." + name
			inputs = append(inputs, id)
			if !types.Implements(ptr, producer) {
				missing = append(missing, id)
			}
		}
	}
	sort.Strings(inputs)
	if len(inputs) < 4 {
		t.Fatalf("found the inputs %v, want at least the four built-in traits': the scan is not reading the sources", inputs)
	}
	if len(missing) > 0 {
		t.Errorf("types implementing FluxNamespaceInput without ProducesFluxNamespaceInput() bool: %v", missing)
	}
}

// methodInterface is the interface of the one method name(params) results.
func methodInterface(name string, params, results []*types.Var) *types.Interface {
	sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(results...), false)
	iface := types.NewInterfaceType([]*types.Func{types.NewFunc(0, nil, name, sig)}, nil)
	iface.Complete()
	return iface
}

// TestFluxNamespaceInput_Produces: the secret, certificate and configmap
// traits make the object they name; the external-secret trait makes its Secret
// only under creationPolicy Owner (the default) and Orphan, while under Merge
// the Secret exists apart from it and under None nothing writes it.
func TestFluxNamespaceInput_Produces(t *testing.T) {
	type producer interface{ ProducesFluxNamespaceInput() bool }
	for _, tc := range []struct {
		name string
		cfg  producer
		want bool
	}{
		{"secret", &traits.SecretConfig{}, true},
		{"certificate", &traits.CertificateConfig{}, true},
		{"configmap", &traits.ConfigMapConfig{}, true},
		{"external-secret default", &traits.ExternalSecretConfig{}, true},
		{"external-secret Owner", &traits.ExternalSecretConfig{CreationPolicy: "Owner"}, true},
		{"external-secret Orphan", &traits.ExternalSecretConfig{CreationPolicy: "Orphan"}, true},
		{"external-secret Merge", &traits.ExternalSecretConfig{CreationPolicy: "Merge"}, false},
		{"external-secret None", &traits.ExternalSecretConfig{CreationPolicy: "None"}, false},
	} {
		if got := tc.cfg.ProducesFluxNamespaceInput(); got != tc.want {
			t.Errorf("%s: ProducesFluxNamespaceInput = %v, want %v", tc.name, got, tc.want)
		}
	}
	// The secret trait's sub-application answers as its config does.
	app, err := applySecret(t, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if p, ok := app.Config.(producer); !ok || !p.ProducesFluxNamespaceInput() {
		t.Errorf("the secret trait's sub-application %T does not report that it makes its Secret", app.Config)
	}
}
