package traits

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// generatedByTrait applies a trait to component "web" and generates the one
// sub-application it adds.
func generatedByTrait(h oam.TraitHandler, props map[string]any) ([]*client.Object, error) {
	bundle := &stack.Bundle{}
	app := stack.NewApplication("web", "ns", &mockServicePortConfig{port: 80})
	if err := h.Apply(&oam.Trait{Properties: props}, app, bundle); err != nil {
		return nil, err
	}
	sub := bundle.Applications[len(bundle.Applications)-1]
	return sub.Config.Generate(sub)
}

// generatedNames is generatedByTrait reduced to each object's name by kind. The
// kind is read from the Go type: the objects carry it whether or not a builder
// filled in their TypeMeta.
func generatedNames(h oam.TraitHandler, props map[string]any) (map[string]string, error) {
	objs, err := generatedByTrait(h, props)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, p := range objs {
		names[reflect.TypeOf(*p).Elem().Name()] = (*p).GetName()
	}
	return names, nil
}

// nameOverrides are the trait properties that override a generated object's
// default name (go-kure/launcher#787): the kinds each one names, and the names
// every object of the trait has when no override is authored.
var nameOverrides = []struct {
	trait    string
	h        oam.TraitHandler
	props    map[string]any
	property string
	names    []string          // the kinds the property names
	defaults map[string]string // every generated kind's default name
}{
	{
		trait:    "scaler",
		h:        &ScalerHandler{},
		props:    map[string]any{"minReplicas": 2, "maxReplicas": 4, "enablePDB": true},
		property: "hpaName",
		names:    []string{"HorizontalPodAutoscaler"},
		defaults: map[string]string{"HorizontalPodAutoscaler": "web-hpa", "PodDisruptionBudget": "web-pdb"},
	},
	{
		trait:    "scaler",
		h:        &ScalerHandler{},
		props:    map[string]any{"minReplicas": 2, "maxReplicas": 4, "enablePDB": true},
		property: "pdbName",
		names:    []string{"PodDisruptionBudget"},
		defaults: map[string]string{"HorizontalPodAutoscaler": "web-hpa", "PodDisruptionBudget": "web-pdb"},
	},
	{
		trait: "rbac",
		h:     &RBACHandler{},
		props: map[string]any{
			"clusterWide": true,
			"rules": []any{map[string]any{
				"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"},
			}},
		},
		property: "name",
		names:    []string{"Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding"},
		defaults: map[string]string{"Role": "web", "RoleBinding": "web", "ClusterRole": "web", "ClusterRoleBinding": "web"},
	},
	{
		trait:    "networkpolicy",
		h:        &NetworkPolicyHandler{},
		props:    map[string]any{"ingress": []any{}},
		property: "name",
		names:    []string{"NetworkPolicy"},
		defaults: map[string]string{"NetworkPolicy": "web-allow"},
	},
}

// TestNameOverride_UsedAsWrittenOrRefused: an authored override names its
// objects as written, at the full 253 characters too, and leaves the trait's
// other objects on their defaults; a value no object name can be, the empty
// string included, is refused with the property in the error. Left out or null,
// every object keeps its default name.
func TestNameOverride_UsedAsWrittenOrRefused(t *testing.T) {
	fits := strings.Repeat("a", oam.ShortenLimitSubdomain)
	refused := map[string]string{
		"one character too long":    fits + "a",
		"an upper-case character":   "Web",
		"an underscore":             "web_scale",
		"a trailing hyphen":         "web-",
		"a slash":                   "team/web",
		"a colon":                   "web:reader",
		"an empty DNS label (a..b)": "a..b",
	}
	for _, o := range nameOverrides {
		t.Run(o.trait+" "+o.property, func(t *testing.T) {
			for _, name := range []string{"edge", "a.b", fits} {
				want := maps.Clone(o.defaults)
				for _, kind := range o.names {
					want[kind] = name
				}
				got, err := generatedNames(o.h, with(o.props, o.property, name))
				if err != nil {
					t.Errorf("%s = %q: %v, want it accepted", o.property, name, err)
				} else if !maps.Equal(got, want) {
					t.Errorf("%s = %q generated %v, want %v", o.property, name, got, want)
				}
			}
			for why, name := range refused {
				_, err := generatedNames(o.h, with(o.props, o.property, name))
				if err == nil {
					t.Errorf("%s with %s (%q) was accepted, want it refused", o.property, why, name)
					continue
				}
				if !strings.Contains(err.Error(), o.property+" ") || !strings.Contains(err.Error(), "DNS-1123 subdomain") {
					t.Errorf("%s with %s: error %q, want it to name the property and the rule", o.property, why, err)
				}
			}
			if got, err := generatedNames(o.h, with(o.props, o.property, "")); err == nil {
				t.Errorf("%s = \"\" was accepted and generated %v, want it refused", o.property, got)
			} else if !strings.Contains(err.Error(), o.property+" is empty") {
				t.Errorf("%s = \"\": error %q, want it to say the property is empty", o.property, err)
			}

			absent := map[string]map[string]any{"left out": o.props, "null": maps.Clone(o.props)}
			absent["null"][o.property] = nil
			for how, props := range absent {
				got, err := generatedNames(o.h, props)
				if err != nil {
					t.Errorf("%s %s: %v", o.property, how, err)
				} else if !maps.Equal(got, o.defaults) {
					t.Errorf("%s %s generated %v, want the defaults %v", o.property, how, got, o.defaults)
				}
			}
		})
	}
}

// A pdbName beside a scaler that generates no PodDisruptionBudget names nothing:
// it is refused, not dropped.
func TestScalerPDBName_NeedsEnablePDB(t *testing.T) {
	for how, props := range map[string]map[string]any{
		"enablePDB left out": {"minReplicas": 2, "maxReplicas": 4, "pdbName": "edge"},
		"enablePDB false":    {"minReplicas": 2, "maxReplicas": 4, "enablePDB": false, "pdbName": "edge"},
	} {
		got, err := generatedNames(&ScalerHandler{}, props)
		if err == nil {
			t.Errorf("%s: pdbName was accepted and generated %v, want it refused", how, got)
			continue
		}
		if !strings.Contains(err.Error(), `pdbName "edge" names no object`) || !strings.Contains(err.Error(), "enablePDB") {
			t.Errorf("%s: error %q, want it to name pdbName and enablePDB", how, err)
		}
	}
}

// The rbac trait's one name is also what both bindings refer to: a binding whose
// roleRef kept the default name would grant nothing. The subject and the
// component label stay the component's.
func TestRBACName_BindingsFollowTheRole(t *testing.T) {
	rbac := nameOverrides[2]
	objs, err := generatedByTrait(rbac.h, with(rbac.props, "name", "edge"))
	if err != nil {
		t.Fatal(err)
	}
	bindings := 0
	for _, p := range objs {
		if app := (*p).GetLabels()["app"]; app != "web" {
			t.Errorf("%T: app label %q, want the component's (web)", *p, app)
		}
		var ref rbacv1.RoleRef
		var subjects []rbacv1.Subject
		switch b := (*p).(type) {
		case *rbacv1.RoleBinding:
			ref, subjects = b.RoleRef, b.Subjects
		case *rbacv1.ClusterRoleBinding:
			ref, subjects = b.RoleRef, b.Subjects
		default:
			continue
		}
		bindings++
		if ref.Name != "edge" {
			t.Errorf("%T: roleRef.name %q, want edge", *p, ref.Name)
		}
		if len(subjects) != 1 || subjects[0].Name != "web" {
			t.Errorf("%T: subjects %v, want the one ServiceAccount web", *p, subjects)
		}
	}
	if bindings != 2 {
		t.Fatalf("found %d bindings, want the RoleBinding and the ClusterRoleBinding", bindings)
	}
}
