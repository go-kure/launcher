package components

import (
	"go/ast"
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestPrometheusRelabelTargetLabels_MatchVendoredSource holds
// refusePromRelabelTargetLabels to the operator's source: the version from
// which ValidationSchemeForPrometheus (pkg/operator/validations.go) returns
// the UTF-8 scheme, and the legacy one below it; the validator judging a
// targetLabel by that scheme, reading an empty action as replace, for the
// actions of promTargetLabelActions ((*LabelConfigValidator), pkg/prometheus/
// validation/validator.go); and the lists of the Prometheus object it
// validates, which are promRelabelLists.
func TestPrometheusRelabelTargetLabels_MatchVendoredSource(t *testing.T) {
	src := loadVendoredPromOp(t)
	decl := func(rel, name string) *ast.FuncDecl {
		t.Helper()
		file := src.files[rel]
		if file == nil {
			t.Fatalf("the excerpt holds no %s", rel)
		}
		for _, d := range file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && funcName(fd) == name {
				return fd
			}
		}
		t.Fatalf("%s declares no %s", rel, name)
		return nil
	}
	stmts := func(fd *ast.FuncDecl) []string {
		var out []string
		for _, s := range fd.Body.List {
			out = append(out, src.text(s))
		}
		return out
	}

	const validations = "pkg/operator/validations.go"
	scheme := decl(validations, "ValidationSchemeForPrometheus")
	body := scheme.Body.List
	ifStmt, isIf := ast.Stmt(nil), false
	if len(body) == 2 {
		ifStmt = body[0]
		_, isIf = ifStmt.(*ast.IfStmt)
	}
	if !isIf || ifStmt.(*ast.IfStmt).Else != nil || len(ifStmt.(*ast.IfStmt).Body.List) != 1 {
		t.Fatalf("%s: ValidationSchemeForPrometheus is not one test of the version and a return: %q", validations, stmts(scheme))
	}
	is := ifStmt.(*ast.IfStmt)
	if got, want := src.text(is.Cond), `version.GTE(semver.MustParse("`+prometheusUTF8LabelNamesMinimum+`"))`; got != want {
		t.Errorf("%s: ValidationSchemeForPrometheus tests %s; prometheusUTF8LabelNamesMinimum reads %s", validations, got, want)
	}
	if got := src.text(is.Body.List[0]); got != "return model.UTF8Validation" {
		t.Errorf("%s: from its bound ValidationSchemeForPrometheus runs %s, not the UTF-8 scheme", validations, got)
	}
	if got := src.text(body[1]); got != "return model.LegacyValidation" {
		t.Errorf("%s: below its bound ValidationSchemeForPrometheus runs %s, not the legacy scheme", validations, got)
	}

	const val = "pkg/prometheus/validation/validator.go"
	if got, want := stmts(decl(val, "(*LabelConfigValidator).isValidLabelName")), []string{
		"validationScheme := operator.ValidationSchemeForPrometheus(lcv.v)",
		"return validationScheme.IsValidLabelName(labelName)",
	}; !slices.Equal(got, want) {
		t.Errorf("%s: isValidLabelName runs %q, not the scheme of the version (%q)", val, got, want)
	}
	validate := decl(val, "(*LabelConfigValidator).ValidateRelabelConfig")
	emptyIsReplace := false
	var actions []string
	ast.Inspect(validate.Body, func(n ast.Node) bool {
		s, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		cond := src.text(s.Cond)
		if cond == `rc.Action == ""` && len(s.Body.List) == 1 && src.text(s.Body.List[0]) == "rc.Action = string(relabel.Replace)" {
			emptyIsReplace = true
		}
		if !strings.Contains(cond, "rc.TargetLabel)") {
			return true
		}
		if !strings.Contains(cond, "lcv.isValidLabelName(rc.TargetLabel)") {
			t.Errorf("%s: ValidateRelabelConfig judges the targetLabel by another test than isValidLabelName: %s", val, cond)
			return true
		}
		ast.Inspect(s.Cond, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "relabel" {
					actions = append(actions, strings.ToLower(sel.Sel.Name))
				}
			}
			return true
		})
		return true
	})
	if !emptyIsReplace {
		t.Errorf("%s: ValidateRelabelConfig no longer reads an empty action as replace", val)
	}
	slices.Sort(actions)
	actions = slices.Compact(actions)
	if want := slices.Sorted(slices.Values(promTargetLabelActions)); !slices.Equal(actions, want) {
		t.Errorf("%s: ValidateRelabelConfig judges the targetLabel of the actions %q; promTargetLabelActions holds %q", val, actions, want)
	}

	// The lists the operator validates: those of the Prometheus object are
	// promRelabelLists; the selector's are a monitor's.
	lists := map[string]string{
		"am.RelabelConfigs":             "alerting.alertmanagers[].relabelings",
		"am.AlertRelabelConfigs":        "alerting.alertmanagers[].alertRelabelings",
		"scrapeClass.Relabelings":       "scrapeClasses[].relabelings",
		"scrapeClass.MetricRelabelings": "scrapeClasses[].metricRelabelings",
	}
	const sel = "pkg/prometheus/resource_selector.go"
	var validated []string
	for _, rel := range prometheusGateFiles(t, src) {
		ast.Inspect(src.files[rel], func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || src.text(call.Fun) != "lcv.Validate" || len(call.Args) != 1 {
				return true
			}
			arg := src.text(call.Args[0])
			switch path, known := lists[arg]; {
			case known:
				validated = append(validated, path)
			case rel == sel && arg == "rcs":
				// The relabelings of a ServiceMonitor, PodMonitor, Probe
				// or ScrapeConfig object, which this kind does not author.
			default:
				t.Errorf("%s: the operator validates the relabeling list %s, which no row names", rel, arg)
			}
			return true
		})
	}
	var want []string
	for _, l := range promRelabelLists {
		want = append(want, l.path)
	}
	slices.Sort(validated)
	slices.Sort(want)
	if !slices.Equal(validated, want) {
		t.Errorf("the operator validates the lists %v of the Prometheus object; promRelabelLists holds %v", validated, want)
	}
}

// TestPrometheusRelabelTargetLabels_Refused: a targetLabel that is not a
// legacy label name, for each action whose targetLabel the operator checks,
// in each list it validates, is refused below Prometheus 3.0.0, naming the
// rule; from 3.0.0 on, and at the operator's default where version is unset,
// it builds. A legacy name builds below 3.0.0, and so does such a name for an
// action whose targetLabel the operator does not check.
func TestPrometheusRelabelTargetLabels_Refused(t *testing.T) {
	type m = map[string]any
	endpoint := m{"namespace": "monitoring", "name": "alertmanager-operated", "port": "web"}
	with := func(base m, key string, rules []any) m {
		out := maps.Clone(base)
		out[key] = rules
		return out
	}
	// Each list's second owner holds the rules, its first none, so that the
	// refusal is seen to name the owner by its index.
	lists := map[string]func(rules []any) m{
		"alerting.alertmanagers[].relabelings": func(r []any) m {
			return m{"alerting": m{"alertmanagers": []any{endpoint, with(endpoint, "relabelings", r)}}}
		},
		"alerting.alertmanagers[].alertRelabelings": func(r []any) m {
			return m{"alerting": m{"alertmanagers": []any{endpoint, with(endpoint, "alertRelabelings", r)}}}
		},
		"scrapeClasses[].relabelings": func(r []any) m {
			return m{"scrapeClasses": []any{m{"name": "first"}, m{"name": "second", "relabelings": r}}}
		},
		"scrapeClasses[].metricRelabelings": func(r []any) m {
			return m{"scrapeClasses": []any{m{"name": "first"}, m{"name": "second", "metricRelabelings": r}}}
		},
	}
	for _, l := range promRelabelLists {
		if lists[l.path] == nil {
			t.Errorf("no fixture authors %s", l.path)
		}
	}
	rule := func(action, target string) m {
		r := m{"action": action, "sourceLabels": []any{"__name__"}, "targetLabel": target}
		if strings.EqualFold(action, "hashmod") {
			r["modulus"] = 4
		}
		return r
	}
	legacy := rule("replace", "service_name")
	for list, author := range lists {
		owner := strings.Replace(list, "[]", "[1]", 1)
		for _, action := range promTargetLabelActions {
			props := author([]any{legacy, rule(action, "service.name")})
			want := owner + `[1].targetLabel: "service.name" is not a legacy label name ([a-zA-Z_][a-zA-Z0-9_]*), the only names the Prometheus operator accepts for a ` +
				action + " action below Prometheus 3.0.0; at version %s it refuses the object at reconcile, and the Prometheus is not updated; name a legacy label, or set version to 3.0.0 or later"
			for _, version := range []string{"v2.55.0", "v3.0.0-rc.0"} {
				if err := promBuild(t, props, "version", version); err == nil || !strings.Contains(err.Error(), strings.Replace(want, "%s", version, 1)) {
					t.Errorf("%s, %s at %s: err = %v, want the targetLabel refused", list, action, version, err)
				}
			}
			for _, version := range []string{"v3.0.0", ""} {
				if err := promBuild(t, props, "version", version); err != nil {
					t.Errorf("%s, %s at %q: err = %v, want it built", list, action, version, err)
				}
			}
		}
		// The operator compares the action case-insensitively; the text
		// names it as it does.
		if err := promBuild(t, author([]any{rule("Replace", "service.name")}), "version", "v2.55.0"); err == nil || !strings.Contains(err.Error(), "for a replace action") {
			t.Errorf("%s, Replace: err = %v, want the targetLabel refused", list, err)
		}
		if err := promBuild(t, author([]any{legacy, rule("keep", "service.name")}), "version", "v2.55.0"); err != nil {
			t.Errorf("%s, keep: err = %v, want it built: the operator does not check the targetLabel of keep", list, err)
		}
		// The gate of an action under its version is refused first, as the
		// validator tests the action before the name.
		if err := promBuild(t, author([]any{rule("lowercase", "service.name")}), "version", "v2.35.0"); err == nil || strings.Contains(err.Error(), "targetLabel") {
			t.Errorf("%s, lowercase at v2.35.0: err = %v, want the action's gate", list, err)
		}
		// A version the operator cannot parse is left to it.
		if err := promBuild(t, author([]any{rule("replace", "service.name")}), "version", "main"); err != nil {
			t.Errorf("%s at an unparsable version: err = %v, want it left to the operator", list, err)
		}
	}
}
