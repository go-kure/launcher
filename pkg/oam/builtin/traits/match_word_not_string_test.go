package traits

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// notStrings are the shapes of a value that is not a string, with the type the
// refusal names for each.
var notStrings = []struct {
	name  string
	value any
	typ   string
}{
	{"number", 5, "int"},
	{"float", 1.5, "float64"},
	{"list", []any{"Exact"}, "[]interface {}"},
	{"map", map[string]any{"type": "Exact"}, "map[string]interface {}"},
	{"bool", true, "bool"},
}

// nulls are the shapes of a null: the untyped nil a decoded `key:` gives, and
// the typed nils a Go caller or a lowering rule can pass. Each is the key left
// out, as it was before go-kure/launcher#1598.
var nulls = []struct {
	name  string
	value any
}{
	{"null", nil},
	{"nil string pointer", (*string)(nil)},
	{"nil map", map[string]any(nil)},
	{"nil list", []any(nil)},
}

// fillReported tells whether a built-in report names path.
func fillReported(report []oam.BuiltinDefault, path string) bool {
	for _, d := range report {
		if d.Path == path {
			return true
		}
	}
	return false
}

// A match type of httproute and a pathType of ingress that is present and is
// not a string is refused, with the path and the accepted words
// (go-kure/launcher#1598): before, the parser read it as left out, the object
// got the default, and the provenance report named the default as a built-in
// fill. A key that is left out or holds a null keeps the default and the fill.
func TestHTTPRouteHandler_MatchTypeNotAString(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", &mockServicePortConfig{port: 80})
	route := func(match map[string]any) map[string]any {
		return map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules":      []any{map[string]any{"matches": []any{match}}},
		}
	}
	path := func(typ any) map[string]any {
		return map[string]any{"path": map[string]any{"value": "/api", "type": typ}}
	}
	header := func(typ any) map[string]any {
		return map[string]any{"headers": []any{map[string]any{"name": "x-tenant", "value": "a", "type": typ}}}
	}
	const (
		pathWords   = `rules[0].matches[0].path: type must be 'PathPrefix', 'Exact', or 'RegularExpression', got a value that is not a string (`
		headerWords = `rules[0].matches[0].headers[0]: type must be 'Exact' or 'RegularExpression', got a value that is not a string (`
	)

	for _, ns := range notStrings {
		t.Run("path/"+ns.name, func(t *testing.T) {
			_, err := h.parseProperties(route(path(ns.value)), app)
			if want := pathWords + ns.typ + ")"; err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to contain %q", err, want)
			}
		})
		t.Run("header/"+ns.name, func(t *testing.T) {
			_, err := h.parseProperties(route(header(ns.value)), app)
			if want := headerWords + ns.typ + ")"; err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to contain %q", err, want)
			}
		})
	}

	// Controls: a null, typed or not, is the key left out.
	for _, null := range nulls {
		t.Run("path/"+null.name, func(t *testing.T) {
			cfg, err := h.parseProperties(route(path(null.value)), app)
			if err != nil {
				t.Fatalf("parseProperties: %v", err)
			}
			if got := cfg.Rules[0].Matches[0].Path.Type; got != "PathPrefix" {
				t.Errorf("path type = %q, want the default PathPrefix", got)
			}
			if !fillReported(cfg.BuiltinDefaults(), "rules[0].matches[0].path.type") {
				t.Errorf("no fill reported for the default: %+v", cfg.BuiltinDefaults())
			}
		})
		t.Run("header/"+null.name, func(t *testing.T) {
			cfg, err := h.parseProperties(route(header(null.value)), app)
			if err != nil {
				t.Fatalf("parseProperties: %v", err)
			}
			if got := cfg.Rules[0].Matches[0].Headers[0].Type; got != "Exact" {
				t.Errorf("header type = %q, want the default Exact", got)
			}
			if !fillReported(cfg.BuiltinDefaults(), "rules[0].matches[0].headers[0].type") {
				t.Errorf("no fill reported for the default: %+v", cfg.BuiltinDefaults())
			}
		})
	}
}

func TestIngressHandler_PathTypeNotAString(t *testing.T) {
	h := &IngressHandler{}
	app := stack.NewApplication("web", "default", &mockServicePortConfig{port: 80})
	ingress := func(path map[string]any) map[string]any {
		return map[string]any{
			"rules": []any{map[string]any{"host": "web.example.test", "paths": []any{path}}},
		}
	}
	const words = `rules[0].paths[0]: pathType must be 'Prefix', 'Exact', or 'ImplementationSpecific', got a value that is not a string (`

	for _, ns := range notStrings {
		t.Run(ns.name, func(t *testing.T) {
			_, err := h.parseProperties(ingress(map[string]any{"path": "/api", "pathType": ns.value}), app)
			if want := words + ns.typ + ")"; err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to contain %q", err, want)
			}
		})
	}

	// Controls: a null, typed or not, and a key left out keep the default and
	// its fill; a known word is the author's.
	type control struct {
		name     string
		path     map[string]any
		want     string
		wantFill bool
	}
	controls := []control{
		{"left out", map[string]any{"path": "/api"}, "Prefix", true},
		{"Exact", map[string]any{"path": "/api", "pathType": "Exact"}, "Exact", false},
	}
	for _, null := range nulls {
		controls = append(controls, control{null.name, map[string]any{"path": "/api", "pathType": null.value}, "Prefix", true})
	}
	for _, tc := range controls {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := h.parseProperties(ingress(tc.path), app)
			if err != nil {
				t.Fatalf("parseProperties: %v", err)
			}
			if got := cfg.Rules[0].Paths[0].PathType; got != tc.want {
				t.Errorf("pathType = %q, want %q", got, tc.want)
			}
			if got := fillReported(cfg.BuiltinDefaults(), "rules[0].paths[0].pathType"); got != tc.wantFill {
				t.Errorf("fill reported = %v, want %v (report: %+v)", got, tc.wantFill, cfg.BuiltinDefaults())
			}
		})
	}
}
