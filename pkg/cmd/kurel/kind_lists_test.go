package kurel

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// kindLists names the lists that hold one entry per component type. A change
// that adds a kind adds a line to each, and two such changes written side by
// side merge without a conflict only where their lines land between different
// neighbours. So every list here stands in the order of its component types,
// as sort.Strings gives it, and a new entry goes at its position instead of at
// the end: TestKindLists_InOrder holds that, and the same of the tables of
// kindTables.
//
// file is relative to this package. name is a package-level variable,
// "Func.variable" for a variable a function declares, or "Func.return" for the
// literal a function returns.
var kindLists = []struct {
	file, name string
}{
	{"build.go", "builtinComponentHandlers.return"},
	{"build_test.go", "TestBuiltinComponentHandlers_RegisteredTypes.wantHandlers"},
	{"component_label_invariant_test.go", "componentLabelFixtures"},
	{"../../oam/builtin/components/core_kinds_test.go", "coreKindSchemas"},
	{"../../oam/builtin/components/kind_api_sets_internal_test.go", "apiSetKinds"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "policyFreeKinds"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "TestPolicyFreeKinds_GenerateCopies.reaches"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "TestPolicyFreeKinds_Refusals.cases"},
	{"../../oam/validate.go", "validComponentTypes"},
}

// kindTables names the Markdown tables that hold one row per component type,
// the type in backquotes in the first cell. They stand in the same order, for
// the same reason. file is relative to this package, heading is the heading
// line the table stands under.
var kindTables = []struct {
	file, heading string
}{
	{"README.md", "## `kurel build`"},
	{"../../oam/builtin/components/README.md", "## Component types"},
	{"../../../docs/oam/design-kurel-package.md", "### 4.2 Supported component types (Phase 1)"},
}

// The reasons more than one component type gives for having no row.
const (
	ownProperties = "it authors its own properties and reads them one by one: it decodes them into no upstream type"
	wholeObjects  = "it carries whole objects the author writes, not the fields of one type"
	fluxNotYet    = "not yet held: no CRD in the linked modules, and the marker source does not read those modules yet"
	ownKindsTable = "it has its row in externalSecretsKinds, whose tests hold its required and defaulted fields to the source of the linked module"
)

// kindListExceptions names, for each list that is held complete, the
// registered component types that have no row in it, each with its reason.
// TestKindLists_Complete fails on a registered type that has neither a row nor
// a reason here, so a new kind is given one of the two.
var kindListExceptions = map[string]map[string]string{
	"coreKindSchemas": {
		"bucket":                "its fields are held by TestFluxSourceHandlers_EveryFieldReachable",
		"cnpg-cluster":          "its fields are held by TestCnpgClusterSchema_EveryFieldReachable",
		"cnpg-database":         "it has its row in cnpgKindSchemas",
		"cnpg-objectstore":      "it has its row in cnpgKindSchemas",
		"cnpg-pooler":           "it has its row in cnpgKindSchemas",
		"configmap":             ownProperties,
		"crd":                   wholeObjects,
		"cronjob":               ownProperties,
		"daemonset":             ownProperties,
		"deployment":            ownProperties,
		"fluxcd-kustomization":  "its fields are held by TestFluxcdKustomizationHandler_EveryFieldReachable",
		"gitrepository":         "its fields are held by TestFluxSourceHandlers_EveryFieldReachable",
		"helmchart":             "its fields are held by TestFluxSourceHandlers_EveryFieldReachable",
		"helmrelease":           "its fields are held by TestHelmReleaseHandler_EveryFieldReachable",
		"helmrepository":        "its fields are held by TestFluxSourceHandlers_EveryFieldReachable",
		"helmtemplate":          "its fields are held by TestHelmTemplateHandler_EveryFieldReachable",
		"job":                   ownProperties,
		"manifests":             wholeObjects,
		"ocirepository":         "its fields are held by TestFluxSourceHandlers_EveryFieldReachable",
		"passthrough":           wholeObjects,
		"persistentvolumeclaim": ownProperties,
		"secret":                ownProperties,
		"service":               ownProperties,
		"serviceaccount":        ownProperties,
		"statefulset":           ownProperties,
	},
	"apiSetKinds": {
		"bucket":                fluxNotYet,
		"clusterexternalsecret": ownKindsTable,
		"clustersecretstore":    ownKindsTable,
		"configmap":             ownProperties,
		"crd":                   wholeObjects,
		"cronjob":               ownProperties,
		"daemonset":             ownProperties,
		"deployment":            ownProperties,
		"externalsecret":        ownKindsTable,
		"fluxcd-kustomization":  fluxNotYet,
		"gitrepository":         fluxNotYet,
		"helmchart":             fluxNotYet,
		"helmrelease":           fluxNotYet,
		"helmrepository":        fluxNotYet,
		"helmtemplate":          "it decodes its properties into a type of its own, not an upstream one",
		"httproute":             "the experimental-channel CRD requires the protocol of an externalAuth filter, the type leaves an empty one out, and the kind does not refuse it",
		"job":                   ownProperties,
		"manifests":             wholeObjects,
		"ocirepository":         fluxNotYet,
		"passthrough":           wholeObjects,
		"persistentvolumeclaim": ownProperties,
		"secret":                ownProperties,
		"secretstore":           ownKindsTable,
		"service":               ownProperties,
		"serviceaccount":        ownProperties,
		"statefulset":           ownProperties,
	},
}

// TestKindLists_InOrder fails on an entry that stands before its place, naming
// it and the entry it follows. It reads the lists from their source: the order
// a map literal is written in is not the map's.
func TestKindLists_InOrder(t *testing.T) {
	for _, list := range kindLists {
		t.Run(list.name, func(t *testing.T) {
			keys := goListKeys(t, list.file, list.name)
			if len(keys) == 0 {
				t.Fatalf("%s: %s holds no entry", list.file, list.name)
			}
			for _, defect := range kindListMisplaced(keys) {
				t.Errorf("%s: in %s, %s", list.file, list.name, defect)
			}
		})
	}
	for _, table := range kindTables {
		t.Run(table.file, func(t *testing.T) {
			for _, defect := range kindListMisplaced(markdownTableKeys(t, table.file, table.heading)) {
				t.Errorf("%s: in the table under %q, %s", table.file, table.heading, defect)
			}
		})
	}
}

// TestKindLists_Complete fails where a list of kindListExceptions and the
// registered component types disagree: a registered type with no row and no
// reason, a type with two rows, a row or a reason for a type that is not
// registered, a type with both. The registered types are the keys of
// builtinComponentHandlers, so no third list is kept by hand.
func TestKindLists_Complete(t *testing.T) {
	registered := make([]string, 0, len(builtinComponentHandlers()))
	for typ := range builtinComponentHandlers() {
		registered = append(registered, typ)
	}
	sort.Strings(registered)

	held := map[string]bool{}
	for _, list := range kindLists {
		exceptions, ok := kindListExceptions[list.name]
		if !ok {
			continue
		}
		held[list.name] = true
		t.Run(list.name, func(t *testing.T) {
			for _, defect := range kindListGaps(registered, goListKeys(t, list.file, list.name), exceptions) {
				t.Errorf("%s: in %s, %s", list.file, list.name, defect)
			}
		})
	}
	for name := range kindListExceptions {
		if !held[name] {
			t.Errorf("kindListExceptions names %s, which is no list of kindLists", name)
		}
	}
}

// TestKindListChecks_NameEachDefect shows the two checks on lists that are
// wrong in one way each, so that neither passes for want of looking.
func TestKindListChecks_NameEachDefect(t *testing.T) {
	registered := []string{"alpha", "beta", "gamma"}
	exceptions := map[string]string{"gamma": "it has no spec type"}

	order := []struct {
		name string
		rows []string
		want []string
	}{
		{"in order", []string{"alpha", "beta"}, nil},
		{"misplaced", []string{"beta", "alpha"}, []string{`"alpha" stands after "beta"`}},
		{"doubled", []string{"alpha", "alpha", "beta"}, []string{`"alpha" stands after "alpha"`}},
	}
	for _, tt := range order {
		t.Run("order/"+tt.name, func(t *testing.T) {
			expectDefects(t, kindListMisplaced(tt.rows), tt.want)
		})
	}

	gaps := []struct {
		name       string
		rows       []string
		exceptions map[string]string
		want       []string
	}{
		{"complete", []string{"alpha", "beta"}, exceptions, nil},
		{"missing row", []string{"alpha"}, exceptions, []string{`"beta" is registered and has no row`}},
		{"doubled row", []string{"alpha", "beta", "beta"}, exceptions, []string{`"beta" has 2 rows`}},
		{"row of no type", []string{"alpha", "beta", "delta"}, exceptions, []string{`the row "delta" names no registered component type`}},
		{"row and reason", []string{"alpha", "beta", "gamma"}, exceptions, []string{`"gamma" has a row and a reason`}},
		{
			"reason of no type",
			[]string{"alpha", "beta"},
			map[string]string{"gamma": "it has no spec type", "omega": "gone"},
			[]string{`the exception "omega" names no registered component type`},
		},
		{"no reason", []string{"alpha", "beta"}, map[string]string{"gamma": ""}, []string{`the exception "gamma" gives no reason`}},
	}
	for _, tt := range gaps {
		t.Run("complete/"+tt.name, func(t *testing.T) {
			expectDefects(t, kindListGaps(registered, tt.rows, tt.exceptions), tt.want)
		})
	}
}

// expectDefects holds got to one defect per entry of want, each beginning
// with it.
func expectDefects(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("defects = %q, want %d beginning with %q", got, len(want), want)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("defect %d = %q, want it to begin with %q", i, got[i], want[i])
		}
	}
}

// kindListMisplaced names every entry that does not stand after the one
// before it in the order of sort.Strings; an entry written twice is one.
func kindListMisplaced(keys []string) []string {
	var defects []string
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			defects = append(defects, fmt.Sprintf("%q stands after %q; the entries are in the order of their component type, and a new one goes at its position", keys[i], keys[i-1]))
		}
	}
	return defects
}

// kindListGaps names every disagreement between the rows of a list, the
// registered component types and the types excepted from the list: each
// registered type has exactly one row or a reason, and neither a row nor a
// reason names another type.
func kindListGaps(registered, rows []string, exceptions map[string]string) []string {
	var defects []string
	count := map[string]int{}
	for _, row := range rows {
		count[row]++
	}
	known := map[string]bool{}
	for _, typ := range registered {
		known[typ] = true
		reason, excepted := exceptions[typ]
		switch {
		case count[typ] == 0 && !excepted:
			defects = append(defects, fmt.Sprintf("%q is registered and has no row: add its row at its position, or name it in kindListExceptions with the reason", typ))
		case count[typ] > 0 && excepted:
			defects = append(defects, fmt.Sprintf("%q has a row and a reason for having none (%s): drop the one that is not true", typ, reason))
		}
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row] {
			continue
		}
		seen[row] = true
		if count[row] > 1 {
			defects = append(defects, fmt.Sprintf("%q has %d rows", row, count[row]))
		}
		if !known[row] {
			defects = append(defects, fmt.Sprintf("the row %q names no registered component type", row))
		}
	}
	excepted := make([]string, 0, len(exceptions))
	for typ := range exceptions {
		excepted = append(excepted, typ)
	}
	sort.Strings(excepted)
	for _, typ := range excepted {
		if !known[typ] {
			defects = append(defects, fmt.Sprintf("the exception %q names no registered component type", typ))
		}
		if exceptions[typ] == "" {
			defects = append(defects, fmt.Sprintf("the exception %q gives no reason", typ))
		}
	}
	return defects
}

// goListKeys returns, in the order they are written, the component types of
// the entries of the composite literal a Go source file gives the named
// variable.
func goListKeys(t *testing.T, file, name string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	lit := goListLiteral(parsed, name)
	if lit == nil {
		t.Fatalf("%s: no composite literal is given to %s", file, name)
	}
	keys := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		key, ok := goListKey(elt)
		if !ok {
			t.Fatalf("%s: an entry of %s names no component type this test can read: write the type as a string literal", file, name)
		}
		keys = append(keys, key)
	}
	return keys
}

// goListLiteral finds the literal: the value of the package-level variable
// name, or, in the function before the dot, of the variable after it or of the
// first return where "return" stands there.
func goListLiteral(file *ast.File, name string) *ast.CompositeLit {
	fn, local, inFunc := strings.Cut(name, ".")
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			if inFunc {
				continue
			}
			for _, spec := range decl.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || len(value.Values) != 1 || value.Names[0].Name != name {
					continue
				}
				if lit, ok := value.Values[0].(*ast.CompositeLit); ok {
					return lit
				}
			}
		case *ast.FuncDecl:
			if !inFunc || decl.Name.Name != fn || decl.Body == nil {
				continue
			}
			var found *ast.CompositeLit
			ast.Inspect(decl.Body, func(n ast.Node) bool {
				if found != nil {
					return false
				}
				switch n := n.(type) {
				case *ast.AssignStmt:
					if len(n.Lhs) != 1 || len(n.Rhs) != 1 {
						return true
					}
					if ident, ok := n.Lhs[0].(*ast.Ident); ok && ident.Name == local {
						found, _ = n.Rhs[0].(*ast.CompositeLit)
					}
				case *ast.ReturnStmt:
					if local == "return" && len(n.Results) == 1 {
						found, _ = n.Results[0].(*ast.CompositeLit)
					}
				}
				return found == nil
			})
			return found
		}
	}
	return nil
}

// goListKey reads the component type of one entry: a string, the key of a map
// entry, or of a struct the field named component, or its first field where
// the struct is written without field names.
func goListKey(elt ast.Expr) (string, bool) {
	switch elt := elt.(type) {
	case *ast.BasicLit:
		if elt.Kind != token.STRING {
			return "", false
		}
		key, err := strconv.Unquote(elt.Value)
		return key, err == nil
	case *ast.KeyValueExpr:
		return goListKey(elt.Key)
	case *ast.CompositeLit:
		if len(elt.Elts) == 0 {
			return "", false
		}
		if _, named := elt.Elts[0].(*ast.KeyValueExpr); !named {
			return goListKey(elt.Elts[0])
		}
		for _, field := range elt.Elts {
			pair, ok := field.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if ident, ok := pair.Key.(*ast.Ident); ok && ident.Name == "component" {
				return goListKey(pair.Value)
			}
		}
	}
	return "", false
}

// markdownType is a table cell that holds one component type in backquotes.
var markdownType = regexp.MustCompile("^`([a-z][a-z0-9-]*)`$")

// markdownTableKeys reads the component types of the tables under a heading
// of a Markdown file (markdownTableTypes).
func markdownTableKeys(t *testing.T, file, heading string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	keys, defect := markdownTableTypes(string(data), heading)
	if defect != "" {
		t.Fatalf("%s: under %q, %s", file, heading, defect)
	}
	return keys
}

// markdownTableTypes returns, in the order they are written, the component
// types in the first cell of the body rows of the tables under a heading of a
// Markdown text, up to the next heading.
//
// It reads one way of writing a table and names a defect for every other, so
// that no row is passed over: every row starts with "|" after at most three
// blanks, and the rows end at a blank line, a code block or a heading. A table
// written another way is known by its row of dashes, which every table has. A
// defect is also a body row whose first cell is not one type in backquotes,
// and a heading with no row, which is what a heading or a table inside a code
// block comes to.
func markdownTableTypes(text, heading string) (keys []string, defect string) {
	inSection, inBody, header := false, false, false
	fence := "" // the run that opened the code block a line stands in
	for n, line := range strings.Split(text, "\n") {
		run, rest := markdownFence(line)
		if fence != "" {
			// A block ends at a run of its own character, at least as long, alone on its line.
			if run != "" && run[0] == fence[0] && len(run) >= len(fence) && rest == "" {
				fence = ""
			}
			continue
		}
		if run != "" {
			fence, inBody, header = run, false, false
			continue
		}
		row := strings.TrimSpace(line)
		shallow := len(line)-len(strings.TrimLeft(line, " ")) <= 3 && !strings.HasPrefix(line, "\t")
		if shallow && strings.HasPrefix(row, "#") {
			if inSection {
				break
			}
			inSection = line == heading
			continue
		}
		if !inSection {
			continue
		}
		readable := shallow && strings.HasPrefix(row, "|")
		switch {
		case inBody && row == "":
			inBody, header = false, false
		case inBody && !readable:
			return nil, fmt.Sprintf("line %d: a line under the rows of a table does not start with |, or is indented: %s", n+1, row)
		case inBody:
			first := strings.TrimSpace(strings.Split(strings.Trim(row, "|"), "|")[0])
			m := markdownType.FindStringSubmatch(first)
			if m == nil {
				return nil, fmt.Sprintf("line %d: the first cell of a row is not one component type in backquotes: %s", n+1, row)
			}
			keys = append(keys, m[1])
		case strings.Contains(row, "|") && markdownRule.MatchString(row):
			// The row of dashes under the header opens the body.
			if !readable || !header {
				return nil, fmt.Sprintf("line %d: a table this test cannot read: its rows do not start with |, or are indented: %s", n+1, row)
			}
			inBody = true
		default:
			header = readable
		}
	}
	if len(keys) == 0 {
		return nil, "no row with a component type"
	}
	return keys, ""
}

// markdownRule is the row of dashes between the header and the body of a
// table, with or without the bars at its ends. A line of dashes with no bar
// is no table.
var markdownRule = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)

// markdownFence returns the run of three or more backquotes or tildes a line
// opens with, after at most three blanks, and what follows the run.
func markdownFence(line string) (run, rest string) {
	text := strings.TrimLeft(line, " ")
	if len(line)-len(text) > 3 || text == "" || (text[0] != '`' && text[0] != '~') {
		return "", ""
	}
	end := len(text) - len(strings.TrimLeft(text, text[:1]))
	if end < 3 {
		return "", ""
	}
	return text[:end], strings.TrimSpace(text[end:])
}

// TestKindLists_MarkdownReader holds the reader of the Markdown tables to what
// it must not pass over: a row it cannot read, a table written another way
// than it reads, a table in a code block, a heading that is not there.
func TestKindLists_MarkdownReader(t *testing.T) {
	const table = "| `type` | text |\n|---|---|\n"
	tests := []struct {
		name, text string
		want       []string
		defect     string
	}{
		{"rows up to the next heading", "## H\n\n" + table + "| `a` | x |\n| `b-2` | y |\n\ntext\n\n## Next\n\n" + table + "| `z` | x |\n", []string{"a", "b-2"}, ""},
		{"blanks around a cell", "## H\n" + table + "| `b` | x |\n|  `a`  | y |\n", []string{"b", "a"}, ""},
		{"rule row with alignment", "## H\n| `type` | text |\n| :--- | ---: |\n| `a` | x |\n", []string{"a"}, ""},
		{"row with no type", "## H\n" + table + "| `a` | x |\n| b | y |\n", nil, "line 5"},
		{"row with two types", "## H\n" + table + "| `a`, `b` | x |\n", nil, "line 4"},
		{"table in a tilde block", "~~~markdown\n## H\n" + table + "| `a` | x |\n~~~\n", nil, "no row"},
		{"shorter run inside a block", "````\n```\n## H\n" + table + "| `a` | x |\n````\n", nil, "no row"},
		{"block inside the section", "## H\n```\n" + table + "| `b` | x |\n```\n" + table + "| `a` | x |\n", []string{"a"}, ""},
		{"heading that is not there", "## Other\n" + table + "| `a` | x |\n", nil, "no row"},
		{"row without its first bar", "## H\n" + table + "| `b` | x |\n`a` | y |\n| `c` | z |\n", nil, "line 5"},
		{"indented heading ends the section", "## H\n\n  ## Next\n" + table + "| `a` | x |\n", nil, "no row"},
		{"indented table", "## H\n\n    | `type` | text |\n    |---|---|\n    | `a` | x |\n", nil, "line 4"},
		{"table without the bars at the ends", "## H\n\n`type` | text\n---|---\n`a` | x\n", nil, "line 4"},
		{"rule row with no header", "## H\n\n|---|---|\n| `a` | x |\n", nil, "line 3"},
		{"line of dashes is no table", "## H\n\n---\n\n" + table + "| `a` | x |\n", []string{"a"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, defect := markdownTableTypes(tt.text, "## H")
			if tt.defect == "" && (defect != "" || !slices.Equal(got, tt.want)) {
				t.Fatalf("got %q and defect %q, want %q and none", got, defect, tt.want)
			}
			if tt.defect != "" && !strings.Contains(defect, tt.defect) {
				t.Fatalf("got %q and defect %q, want a defect that names %q", got, defect, tt.defect)
			}
		})
	}
}
