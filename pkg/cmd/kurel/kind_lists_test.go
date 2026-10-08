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
// Some lists hold an entry only for the kinds of one family, or one per type a
// kind decodes, as imageFieldTypes holds one per spec type that names an
// image. kindListExceptions names none of them: they are held to their order,
// not to the registered types. A kind's helpers, the functions and values only
// its entries use, go in a test file of the kind's own, not beside the list
// they serve, for the same reason.
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
	{"kind_trait_object_claim_test.go", "kindTraitPairs"},
	{"../../oam/builtin/components/core_kinds_test.go", "coreKindSchemas"},
	{"../../oam/builtin/components/image_fields_internal_test.go", "imageFieldTypes"},
	{"../../oam/builtin/components/kind_api_sets_internal_test.go", "apiSetKinds"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "policyFreeKinds"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "TestPolicyFreeKinds_GenerateCopies.reaches"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "TestPolicyFreeKinds_Refusals.cases"},
	{"../../oam/builtin/components/monitoring_kinds_internal_test.go", "monitoringKinds"},
	{"../../oam/builtin/components/required_written_pin_internal_test.go", "requiredWrittenKinds"},
	{"../../oam/validate.go", "validComponentTypes"},
}

// kindListKeyFields names, for a list of kindLists whose entries are structs
// written with field names and name their type in a field other than
// component, that field.
var kindListKeyFields = map[string]string{
	"imageFieldTypes": "name",
	"kindTraitPairs":  "typ",
}

// kindTables names the Markdown tables that hold one row per component type,
// the type in backquotes in the first cell. They stand in the same order, for
// the same reason. file is relative to this package, heading is the heading
// line the table stands under.
//
// One list of the same documents is not read: the "Per-type highlights" of
// pkg/oam/builtin/components/README.md are not yet in order of their types.
var kindTables = []struct {
	file, heading string
}{
	{"README.md", "## `kurel build`"},
	{allowlistTable.file, allowlistTable.heading},
	{"../../oam/builtin/components/README.md", "## Component types"},
	{"../../../docs/oam/design-kurel-package.md", "### 4.2 Supported component types (Phase 1)"},
}

// allowlistTable is the table of pkg/oam/README.md that names every type of
// pkg/oam's validComponentTypes, one row each. TestKindLists_Complete holds
// its rows to that list, which holds more than the registered types: it also
// names the types a lowering rule lowers away (webservice, helm, postgresql).
var allowlistTable = struct {
	file, heading, list string
}{"../../oam/README.md", "### Component type allowlist", "../../oam/validate.go"}

// The reasons more than one component type gives for having no row.
const (
	ownProperties = "it authors its own properties and reads them one by one: it decodes them into no upstream type"
	wholeObjects  = "it carries whole objects the author writes, not the fields of one type"
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
		"clusterexternalsecret": ownKindsTable,
		"clustersecretstore":    ownKindsTable,
		"configmap":             ownProperties,
		"crd":                   wholeObjects,
		"cronjob":               ownProperties,
		"daemonset":             ownProperties,
		"deployment":            ownProperties,
		"externalsecret":        ownKindsTable,
		"helmtemplate":          "it decodes its properties into a type of its own, not an upstream one",
		"httproute":             "the experimental-channel CRD requires the protocol of an externalAuth filter, the type leaves an empty one out, and the kind does not refuse it",
		"job":                   ownProperties,
		"manifests":             wholeObjects,
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

	t.Run("allowlist table", func(t *testing.T) {
		allowed := goListKeys(t, allowlistTable.list, "validComponentTypes")
		rows := markdownTableKeys(t, allowlistTable.file, allowlistTable.heading)
		for _, defect := range allowlistTableGaps(allowed, rows) {
			t.Errorf("%s: in the table under %q, %s", allowlistTable.file, allowlistTable.heading, defect)
		}
	})
}

// parityKindsFile is the source of parityKinds, the hand-parsed kinds whose
// properties TestHandParsedKinds_CoverEveryUpstreamField holds to every field
// of their upstream type. It is not a list of kindLists: its rows stand in no
// order of their types, and it names only the hand-parsed kinds.
const parityKindsFile = "../../oam/builtin/components/hand_parsed_parity_internal_test.go"

// TestKindLists_OwnPropertiesHaveParityRows fails where the types
// kindListExceptions excepts with ownProperties and the rows of parityKinds
// disagree. ownProperties says a kind decodes its properties into no upstream
// type; the parity rows are what then hold it to that type, so a kind given the
// reason without a row would be held by nothing.
func TestKindLists_OwnPropertiesHaveParityRows(t *testing.T) {
	var owned []string
	for _, exceptions := range kindListExceptions {
		for typ, reason := range exceptions {
			if reason == ownProperties && !slices.Contains(owned, typ) {
				owned = append(owned, typ)
			}
		}
	}
	sort.Strings(owned)
	if len(owned) == 0 {
		t.Fatal("kindListExceptions gives no type the ownProperties reason: the check would pass vacuously")
	}
	rows := goListKeys(t, parityKindsFile, "parityKinds")
	if len(rows) == 0 {
		t.Fatalf("%s: parityKinds holds no entry", parityKindsFile)
	}
	for _, defect := range ownPropertiesParityGaps(owned, rows) {
		t.Errorf("%s: in parityKinds, %s", parityKindsFile, defect)
	}
}

// ownPropertiesParityGaps names every type with the ownProperties reason and
// no parity row, and every parity row of a type without that reason.
func ownPropertiesParityGaps(owned, rows []string) []string {
	var defects []string
	for _, typ := range owned {
		if !slices.Contains(rows, typ) {
			defects = append(defects, fmt.Sprintf("%q reads its own properties (ownProperties) and has no row: add one, or the kind is held to its upstream type by nothing", typ))
		}
	}
	for _, row := range rows {
		if !slices.Contains(owned, row) {
			defects = append(defects, fmt.Sprintf("the row %q names no type kindListExceptions excepts with ownProperties", row))
		}
	}
	return defects
}

// allowlistTableGaps names every type of the allowlist with no row and every
// row that names no type of it. A row written twice is left to
// kindListMisplaced.
func allowlistTableGaps(allowed, rows []string) []string {
	var defects []string
	for _, typ := range allowed {
		if !slices.Contains(rows, typ) {
			defects = append(defects, fmt.Sprintf("%q is in validComponentTypes and has no row: add its row at its position", typ))
		}
	}
	for _, row := range rows {
		if !slices.Contains(allowed, row) {
			defects = append(defects, fmt.Sprintf("the row %q names no type of validComponentTypes", row))
		}
	}
	return defects
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

	allowed := []string{"alpha", "beta"}
	table := []struct {
		name string
		rows []string
		want []string
	}{
		{"complete", []string{"alpha", "beta"}, nil},
		{"missing row", []string{"alpha"}, []string{`"beta" is in validComponentTypes and has no row`}},
		{"row of no type", []string{"alpha", "beta", "delta"}, []string{`the row "delta" names no type`}},
	}
	for _, tt := range table {
		t.Run("allowlist/"+tt.name, func(t *testing.T) {
			expectDefects(t, allowlistTableGaps(allowed, tt.rows), tt.want)
		})
	}

	owned := []string{"alpha", "beta"}
	parity := []struct {
		name string
		rows []string
		want []string
	}{
		{"complete", []string{"beta", "alpha"}, nil},
		{"missing row", []string{"alpha"}, []string{`"beta" reads its own properties (ownProperties) and has no row`}},
		{"row of no such type", []string{"alpha", "beta", "delta"}, []string{`the row "delta" names no type`}},
	}
	for _, tt := range parity {
		t.Run("parity/"+tt.name, func(t *testing.T) {
			expectDefects(t, ownPropertiesParityGaps(owned, tt.rows), tt.want)
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
// variable, read from the field kindListKeyFields names for it.
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
		key, ok := goListKey(elt, kindListKeyFields[name])
		if !ok {
			t.Fatalf("%s: an entry of %s names no component type this test can read: write the type as a string literal", file, name)
		}
		keys = append(keys, key)
	}
	return keys
}

// goListLiteral finds the literal: the value of the package-level variable
// name, or, in the function before the dot, of the variable after it or of the
// first return where "return" stands there. A function written inside that
// function is not read: its returns and variables are its own.
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
				case *ast.FuncLit:
					return false
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
// entry, or of a struct the field named key, component where key is empty, or
// its first field where the struct is written without field names.
func goListKey(elt ast.Expr, key string) (string, bool) {
	if key == "" {
		key = "component"
	}
	switch elt := elt.(type) {
	case *ast.BasicLit:
		if elt.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(elt.Value)
		return value, err == nil
	case *ast.KeyValueExpr:
		return goListKey(elt.Key, key)
	case *ast.CompositeLit:
		if len(elt.Elts) == 0 {
			return "", false
		}
		if _, named := elt.Elts[0].(*ast.KeyValueExpr); !named {
			return goListKey(elt.Elts[0], key)
		}
		for _, field := range elt.Elts {
			pair, ok := field.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if ident, ok := pair.Key.(*ast.Ident); ok && ident.Name == key {
				return goListKey(pair.Value, key)
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
// It reads one form of table and accounts for every line of the section that
// holds a "|" outside a code block. Such a line is the header row of a table,
// the row of dashes under it, or a body row whose first cell is one type in
// backquotes, and each starts with "|" after at most three blanks; any other
// line with a "|" is a defect, whatever its form. The rows of a table end at
// a blank line, a code block or a heading, and a line with no "|" under them
// is a defect too. A heading is, after at most three blanks, one to six "#"
// and then a blank or the end of the line. A heading with no row is a defect,
// which is what a heading or a table inside a code block comes to.
func markdownTableTypes(text, heading string) (keys []string, defect string) {
	lines := strings.Split(text, "\n")
	bad := func(n int, why string) ([]string, string) {
		return nil, fmt.Sprintf("line %d: %s: %s", n, why, strings.TrimSpace(lines[n-1]))
	}
	const noTable = "a line with a | that is no row of a table: no row of dashes follows it"
	inSection, inBody := false, false
	header := 0 // the line of a header row that waits for its row of dashes
	fence := "" // the run that opened the code block a line stands in
	for n, line := range lines {
		run, rest := markdownFence(line)
		if fence != "" {
			// A block ends at a run of its own character, at least as long, alone on its line.
			if run != "" && run[0] == fence[0] && len(run) >= len(fence) && rest == "" {
				fence = ""
			}
			continue
		}
		row := strings.TrimSpace(line)
		shallow := len(line)-len(strings.TrimLeft(line, " ")) <= 3 && !strings.HasPrefix(line, "\t")
		bar := run == "" && strings.Contains(row, "|")
		rule := bar && markdownRule.MatchString(row)
		if header != 0 && !rule {
			return bad(header, noTable)
		}
		if run != "" {
			fence, inBody = run, false
			continue
		}
		if shallow && markdownHeading.MatchString(row) {
			if inSection {
				break
			}
			inSection = line == heading
			continue
		}
		if !inSection {
			continue
		}
		switch {
		case row == "":
			inBody = false
		case !bar:
			if inBody {
				return bad(n+1, "a line under the rows of a table has no |")
			}
		case !shallow || !strings.HasPrefix(row, "|"):
			return bad(n+1, "a line with a | does not start with it, or is indented")
		case inBody:
			// One bar opens the row: a second one is an empty first cell.
			first := strings.TrimSpace(strings.Split(strings.TrimPrefix(row, "|"), "|")[0])
			m := markdownType.FindStringSubmatch(first)
			if m == nil {
				return bad(n+1, "the first cell of a row is not one component type in backquotes")
			}
			keys = append(keys, m[1])
		case rule:
			if header == 0 {
				return bad(n+1, "a row of dashes with no header row over it")
			}
			header, inBody = 0, true
		default:
			header = n + 1
		}
	}
	if header != 0 {
		return bad(header, noTable)
	}
	if len(keys) == 0 {
		return nil, "no row with a component type"
	}
	return keys, ""
}

// markdownHeading is a heading line without its indent: one to six "#", then
// a blank or the end of the line.
var markdownHeading = regexp.MustCompile(`^#{1,6}([ \t]|$)`)

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
// it must not pass over: a row it cannot read, a line with a bar that is no
// row of a table it reads, a table in a code block, a heading that is not
// there.
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
		{"indented table", "## H\n\n    | `type` | text |\n    |---|---|\n    | `a` | x |\n", nil, "line 3"},
		{"table without the bars at the ends", "## H\n\n`type` | text\n---|---\n`a` | x\n", nil, "line 3"},
		{"rule row with no header", "## H\n\n|---|---|\n| `a` | x |\n", nil, "line 3"},
		{"line of dashes is no table", "## H\n\n---\n\n" + table + "| `a` | x |\n", []string{"a"}, ""},
		{"line with a hash and a bar under the rows", "## H\n" + table + "| `b` | x |\n#note | y |\n| `a` | z |\n", nil, "line 5"},
		{"row with an empty first cell", "## H\n" + table + "|| `a` | x |\n", nil, "line 4"},
		{"row with a bar and no table", "## H\n\n| `a` | x |\n\n" + table + "| `b` | x |\n", nil, "line 3"},
		{"text with a bar", "## H\n\none | two\n\n" + table + "| `a` | x |\n", nil, "line 3"},
		{"header row at the end", "## H\n" + table + "| `a` | x |\n\n| `type` | text |", nil, "line 6"},
		{"hashes alone end the section", "## H\n" + table + "| `b` | x |\n\n##\n\n" + table + "| `a` | x |\n", []string{"b"}, ""},
		{"seven hashes are no heading", "## H\n" + table + "| `b` | x |\n\n####### Next\n\n" + table + "| `a` | x |\n", []string{"b", "a"}, ""},
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

// TestKindLists_GoReader holds the reader of the Go lists to the body of the
// function it is given: a list in a function written inside it is not read in
// place of the function's own, and does not stand in for one that is gone.
func TestKindLists_GoReader(t *testing.T) {
	const src = `package p

func handlers() map[string]int {
	_ = func() []string { return []string{"alpha"} }
	return map[string]int{"b": 1, "a": 2}
}

func outer() {
	inner := func() { want := []string{"alpha"}; _ = want }
	inner()
	want := []string{"b", "a"}
	_ = want
}

func renamed() {
	inner := func() { want := []string{"alpha"}; _ = want }
	inner()
}

var images = []struct {
	name string
	typ  int
}{{typ: 1, name: "b spec"}, {name: "a spec", typ: 2}}

var pairs = []struct{ kind, typ string }{{kind: "x", typ: "b"}, {typ: "a"}}
`
	parsed, err := parser.ParseFile(token.NewFileSet(), "src.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tests := []struct {
		name, key string
		want      []string
	}{
		{"handlers.return", "", []string{"b", "a"}},
		{"outer.want", "", []string{"b", "a"}},
		{"renamed.want", "", nil},
		{"images", "name", []string{"b spec", "a spec"}},
		{"images", "", nil},
		{"pairs", "typ", []string{"b", "a"}},
		{"pairs", "kind", []string{"x"}},
	}
	for _, tt := range tests {
		name := tt.name
		if tt.key != "" {
			name += "/" + tt.key
		}
		t.Run(name, func(t *testing.T) {
			var got []string
			if lit := goListLiteral(parsed, tt.name); lit != nil {
				for _, elt := range lit.Elts {
					if key, ok := goListKey(elt, tt.key); ok {
						got = append(got, key)
					}
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("read %q, want %q", got, tt.want)
			}
		})
	}
}

// TestKindLists_InOrderNamesASwap holds each list of kindLists, as it is read,
// to failing TestKindLists_InOrder once two of its entries change places: its
// entries are read as the types they name, not as one value each.
func TestKindLists_InOrderNamesASwap(t *testing.T) {
	for _, list := range kindLists {
		t.Run(list.name, func(t *testing.T) {
			keys := goListKeys(t, list.file, list.name)
			if len(keys) < 2 {
				t.Fatalf("%s: %s holds %d entries, too few to swap", list.file, list.name, len(keys))
			}
			keys[0], keys[1] = keys[1], keys[0]
			expectDefects(t, kindListMisplaced(keys), []string{fmt.Sprintf("%q stands after %q", keys[1], keys[0])})
		})
	}
}
