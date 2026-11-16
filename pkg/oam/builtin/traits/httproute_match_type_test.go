package traits

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// A match type is one of the Gateway API words or is left out. A word the
// handler does not know, the empty string included, is refused with the path
// and the accepted words: before go-kure/launcher#1592 it was accepted and the
// HTTPRoute got the default type.
//
// The default is reported as a built-in fill exactly where it stands: where the
// type is left out. A known word is the author's and is not reported. A value
// that is not a string is refused as an unknown word is
// (TestHTTPRouteHandler_MatchTypeNotAString).
func TestHTTPRouteHandler_MatchType(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", &mockServicePortConfig{port: 80})
	route := func(match map[string]any) map[string]any {
		return map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules":      []any{map[string]any{"matches": []any{match}}},
		}
	}
	path := func(typ any) map[string]any {
		p := map[string]any{"value": "/api"}
		if typ != nil {
			p["type"] = typ
		}
		return map[string]any{"path": p}
	}
	header := func(typ any) map[string]any {
		hm := map[string]any{"name": "x-tenant", "value": "a"}
		if typ != nil {
			hm["type"] = typ
		}
		return map[string]any{"headers": []any{hm}}
	}
	const (
		pathWords   = `rules[0].matches[0].path: type must be 'PathPrefix', 'Exact', or 'RegularExpression', got `
		headerWords = `rules[0].matches[0].headers[0]: type must be 'Exact' or 'RegularExpression', got `
	)

	for _, tc := range []struct {
		name       string
		match      map[string]any
		wantPath   string
		wantHeader string
		wantFill   bool
		wantErr    string
	}{
		{name: "path/left out", match: path(nil), wantPath: "PathPrefix", wantFill: true},
		{name: "path/not a string", match: path(5), wantErr: pathWords + `a value that is not a string (int)`},
		{name: "path/PathPrefix", match: path("PathPrefix"), wantPath: "PathPrefix"},
		{name: "path/Exact", match: path("Exact"), wantPath: "Exact"},
		{name: "path/RegularExpression", match: path("RegularExpression"), wantPath: "RegularExpression"},
		{name: "path/the ingress word", match: path("Prefix"), wantErr: pathWords + `"Prefix"`},
		{name: "path/wrong case", match: path("exact"), wantErr: pathWords + `"exact"`},
		{name: "path/empty", match: path(""), wantErr: pathWords + `""`},
		{name: "header/left out", match: header(nil), wantHeader: "Exact", wantFill: true},
		{name: "header/not a string", match: header(5), wantErr: headerWords + `a value that is not a string (int)`},
		{name: "header/Exact", match: header("Exact"), wantHeader: "Exact"},
		{name: "header/RegularExpression", match: header("RegularExpression"), wantHeader: "RegularExpression"},
		{name: "header/short word", match: header("Regex"), wantErr: headerWords + `"Regex"`},
		{name: "header/a path word", match: header("PathPrefix"), wantErr: headerWords + `"PathPrefix"`},
		{name: "header/empty", match: header(""), wantErr: headerWords + `""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := h.parseProperties(route(tc.match), app)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProperties: %v", err)
			}
			match := cfg.Rules[0].Matches[0]
			if tc.wantPath != "" && (match.Path == nil || match.Path.Type != tc.wantPath) {
				t.Errorf("path = %+v, want type %q", match.Path, tc.wantPath)
			}
			if tc.wantHeader != "" && (len(match.Headers) != 1 || match.Headers[0].Type != tc.wantHeader) {
				t.Errorf("headers = %+v, want one of type %q", match.Headers, tc.wantHeader)
			}
			typePath := "rules[0].matches[0].path.type"
			if tc.wantHeader != "" {
				typePath = "rules[0].matches[0].headers[0].type"
			}
			filled := false
			for _, d := range cfg.BuiltinDefaults() {
				if d.Path == typePath {
					filled = true
				}
			}
			if filled != tc.wantFill {
				t.Errorf("fill reported at %s = %v, want %v (report: %+v)", typePath, filled, tc.wantFill, cfg.BuiltinDefaults())
			}
		})
	}
}
