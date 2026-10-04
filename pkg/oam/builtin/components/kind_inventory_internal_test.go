package components

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// kureModulePath is the base library whose generated constructors the README's
// kind inventory lists (go-kure/launcher#790).
const kureModulePath = "github.com/go-kure/kure"

// kureKubernetesImportPath is the package tree those constructors live in: the
// package itself and one directory per supported operator.
const kureKubernetesImportPath = kureModulePath + "/pkg/kubernetes"

// The values of the inventory's Status column.
const (
	inventoryKind          = "kind"
	inventoryComponent     = "component"
	inventoryTrait         = "trait"
	inventoryMissing       = "missing"
	inventoryNotAuthorable = "not authorable"
)

// inventoryRow is one row of the README's kind inventory table.
type inventoryRow struct {
	line                             int
	kind, status, typ, decode, notes string
}

// TestKindInventory_CoversEveryConstructor holds the README's kind inventory to
// the base library: every generated Create<Kind> constructor of the linked
// module has exactly one row, and every row names one. A base-library bump that
// adds or removes a constructor fails here, naming it. It also checks what a
// row must carry for its status.
func TestKindInventory_CoversEveryConstructor(t *testing.T) {
	constructors := kureGeneratedConstructors(t)
	rows := readKindInventory(t)

	for _, name := range slices.Sorted(maps.Keys(constructors)) {
		if _, ok := rows[name]; !ok {
			t.Errorf("%s has no row in README.md's kind inventory: add one, with status %q if no component projects it yet", name, inventoryMissing)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(rows)) {
		row := rows[name]
		clusterScoped, ok := constructors[name]
		if !ok {
			t.Errorf("README.md:%d: stale row %s: the linked %s has no such generated constructor", row.line, name, kureModulePath)
			continue
		}
		// The Kind cell is "<apiVersion> <Kind>", with "(cluster-scoped)" after
		// it exactly when the constructor takes no namespace.
		want := []string{strings.TrimPrefix(name[strings.Index(name, ".")+1:], "Create")}
		if clusterScoped {
			want = append(want, "(cluster-scoped)")
		}
		if got := strings.Fields(row.kind); len(got) < 2 || !slices.Equal(got[1:], want) {
			t.Errorf("README.md:%d: %s: Kind cell is %q, want \"<apiVersion> %s\"", row.line, name, row.kind, strings.Join(want, " "))
		}
		switch row.status {
		case inventoryKind, inventoryTrait:
			if row.typ == "" || row.decode == "" {
				t.Errorf("README.md:%d: %s: a %q row needs a Type and a Decode", row.line, name, row.status)
			}
		case inventoryComponent:
			if row.typ == "" || row.decode == "" || row.notes == "" {
				t.Errorf("README.md:%d: %s: a %q row needs a Type, a Decode and, in Notes, why there is no kind component", row.line, name, row.status)
			}
		case inventoryMissing:
			if row.typ != "" || row.decode != "" {
				t.Errorf("README.md:%d: %s: a %q row has no Type and no Decode", row.line, name, row.status)
			}
		case inventoryNotAuthorable:
			if row.typ != "" || row.decode != "" || row.notes == "" {
				t.Errorf("README.md:%d: %s: a %q row has no Type and no Decode, and gives its reason in Notes", row.line, name, row.status)
			}
		default:
			t.Errorf("README.md:%d: %s: unknown status %q", row.line, name, row.status)
		}
	}
}

// TestKindInventory_MatchesCallSites holds the inventory's Status column to the
// code: a kind row's constructor is called from this package, a trait row's
// from ../traits and not from here, and a missing or not authorable row's from
// neither. So a new kind component fails here until its row says so, and a row
// cannot claim a component that builds nothing. A component row (an object a
// non-kind component emits, or the crd exception) is not held to either.
func TestKindInventory_MatchesCallSites(t *testing.T) {
	constructors := kureGeneratedConstructors(t)
	here := kureConstructorCalls(t, ".", constructors)
	traits := kureConstructorCalls(t, filepath.Join("..", "traits"), constructors)
	// Vacuity guards: a walk that resolves no import finds no call, and every
	// missing row would then pass. Both packages call far more than this.
	if len(here) < 10 || len(traits) < 5 {
		t.Fatalf("found %d constructors called here and %d in ../traits, want >= 10 and >= 5; the call-site walk is broken", len(here), len(traits))
	}
	rows := readKindInventory(t)
	for _, name := range slices.Sorted(maps.Keys(rows)) {
		row := rows[name]
		switch row.status {
		case inventoryKind:
			if !here[name] {
				t.Errorf("README.md:%d: %s has status %q, but no file of this package calls it", row.line, name, row.status)
			}
		case inventoryTrait:
			if !traits[name] {
				t.Errorf("README.md:%d: %s has status %q, but no file of ../traits calls it", row.line, name, row.status)
			}
			if here[name] {
				t.Errorf("README.md:%d: %s has status %q, but this package calls it: the row is %q or %q now", row.line, name, row.status, inventoryKind, inventoryComponent)
			}
		case inventoryMissing, inventoryNotAuthorable:
			if here[name] || traits[name] {
				t.Errorf("README.md:%d: %s has status %q, but this package or ../traits calls it: update the row", row.line, name, row.status)
			}
		}
	}
}

// kureGeneratedConstructors returns the generated Create<Kind> constructors of
// the linked base library, keyed "<package directory>.Create<Kind>", with
// whether the constructor is for a cluster-scoped kind (it takes a name and no
// namespace). It reads every zz_generated_create.go under the module's
// pkg/kubernetes, at any depth, since Go cannot list a package's functions at
// run time, and fails when the module directory or those files cannot be found.
func kureGeneratedConstructors(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join(linkedModuleDir(t, kureModulePath), filepath.FromSlash("pkg/kubernetes"))
	const generated = "zz_generated_create.go"
	var files []string
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == generated {
			files = append(files, file)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s for the %s files: %v", root, generated, err)
	}

	constructors := map[string]bool{}
	fset := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s of %s: %v (the base library moved its generated constructors; update this test)", generated, kureModulePath, err)
		}
		dir := filepath.Base(filepath.Dir(file))
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Create") {
				continue
			}
			params := 0
			for _, field := range fn.Type.Params.List {
				params += max(len(field.Names), 1)
			}
			if params != 1 && params != 2 {
				t.Fatalf("%s: %s takes %d parameters, want (name) or (name, namespace); update this test", file, fn.Name.Name, params)
			}
			key := dir + "." + fn.Name.Name
			if _, dup := constructors[key]; dup {
				t.Fatalf("%s: a second generated constructor reads %s: two package directories share a name, so the inventory's keys are ambiguous; update this test", file, key)
			}
			constructors[key] = params == 1
		}
	}
	// Vacuity guard: 128 at v0.2.0-beta.15, over nine files.
	if len(files) < 9 || len(constructors) < 100 {
		t.Fatalf("found %d constructors in %d %s files under %s, want >= 100 in >= 9; the walk is broken", len(constructors), len(files), generated, root)
	}
	return constructors
}

// readKindInventory returns the rows of the "Kind inventory" table in this
// package's README.md, keyed by constructor. A "-" cell reads as empty.
func readKindInventory(t *testing.T) map[string]inventoryRow {
	t.Helper()
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	rows, problem := parseKindInventory(string(data))
	if problem != "" {
		t.Fatalf("README.md:%s", problem)
	}
	// Vacuity guard: a renamed heading or a reformatted table reads as no rows.
	if len(rows) < 100 {
		t.Fatalf("read %d rows under README.md's \"## Kind inventory\" heading, want >= 100; the table moved or changed form", len(rows))
	}
	return rows
}

// parseKindInventory reads the rows of the one table in readme's "Kind
// inventory" section. It returns a problem, starting with the line number,
// for anything in the section it cannot place: every line there that holds a
// "|" is a line of that table (header, separator, then rows up to the first
// blank line), so a row in another form is refused instead of skipped.
func parseKindInventory(readme string) (rows map[string]inventoryRow, problem string) {
	cell := func(s string) string {
		if s = strings.TrimSpace(s); s == "-" {
			return ""
		}
		return s
	}
	const (
		outside = iota
		wantSeparator
		inRows
	)
	rows = map[string]inventoryRow{}
	inSection, state, tableSeen := false, outside, false
	for i, line := range strings.Split(readme, "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection, state = line == "## Kind inventory", outside
			continue
		}
		if !inSection {
			continue
		}
		line = strings.TrimSpace(line)
		if state == outside && !strings.Contains(line, "|") {
			continue
		}
		// "| a | b | c | d | e | f |" splits into eight parts, the outer two empty.
		parts := strings.Split(line, "|")
		if len(parts) != 8 || parts[0] != "" || parts[7] != "" {
			if line == "" && state == inRows {
				state = outside // the blank line that ends the table
				continue
			}
			return nil, fmt.Sprintf("%d: the kind inventory section reads %q where a line of its table is expected: six cells between \"|\", outer ones included (a cell and the section's prose cannot contain a \"|\")", i+1, line)
		}
		first := cell(parts[1])
		switch state {
		case outside:
			if tableSeen || first != "Constructor" {
				return nil, fmt.Sprintf("%d: the kind inventory section holds one table, whose header starts with \"Constructor\"; found another table line, starting with %q", i+1, first)
			}
			state, tableSeen = wantSeparator, true
			continue
		case wantSeparator:
			for _, part := range parts[1:7] {
				if dashes := strings.TrimSpace(part); dashes == "" || strings.Trim(dashes, "-:") != "" {
					return nil, fmt.Sprintf("%d: want the separator line under the kind inventory's header, found %q", i+1, line)
				}
			}
			state = inRows
			continue
		}
		name := strings.Trim(first, "`")
		if name == "" || first != "`"+name+"`" {
			return nil, fmt.Sprintf("%d: a kind inventory row starts with %q, want a constructor in backticks", i+1, first)
		}
		if _, dup := rows[name]; dup {
			return nil, fmt.Sprintf("%d: second kind inventory row for %s", i+1, name)
		}
		rows[name] = inventoryRow{
			line:   i + 1,
			kind:   cell(parts[2]),
			status: cell(parts[3]),
			typ:    cell(parts[4]),
			decode: cell(parts[5]),
			notes:  cell(parts[6]),
		}
	}
	return rows, ""
}

// TestKindInventory_TableParser pins what the README parser accepts and what
// it refuses, on tables the README does not hold.
func TestKindInventory_TableParser(t *testing.T) {
	const (
		head = "# Title\n\n## Other\n\n| `x.CreateIgnored` | a | b | c | d | e |\n\n## Kind inventory\n\nProse.\n\n"
		top  = "| Constructor | Kind | Status | Type | Decode | Notes |\n|---|:--|--:|---|---|---|\n"
		rowA = "| `kubernetes.CreateA` | v1 A | kind | `a` | hand-written parser | - |\n"
		rowB = "|  `kubernetes.CreateB`  | v1 B (cluster-scoped) | missing | - | - | - |\n"
		tail = "\nMore prose.\n\n## Next\n\n| `x.CreateIgnored` | a | b | c | d | e |\n"
	)
	tests := []struct {
		name, readme string
		want         []string
		wantProblem  string
	}{
		{name: "rows, with loose spacing", readme: head + top + rowA + rowB + tail, want: []string{"kubernetes.CreateA", "kubernetes.CreateB"}},
		{name: "no table", readme: head + tail},
		// The header is line 11 of these documents, the separator line 12.
		{name: "a first cell without backticks", readme: head + top + rowA + "| kubernetes.CreateB | v1 B | missing | - | - | - |\n" + tail, wantProblem: "14: a kind inventory row starts with"},
		{name: "an empty first cell", readme: head + top + rowA + "| | v1 B | missing | - | - | - |\n" + tail, wantProblem: "14: a kind inventory row starts with"},
		{name: "a second separator", readme: head + top + rowA + "|---|---|---|---|---|---|\n" + tail, wantProblem: "14: a kind inventory row starts with"},
		{name: "a row where the separator belongs", readme: head + "| Constructor | Kind | Status | Type | Decode | Notes |\n" + rowA + tail, wantProblem: "12: want the separator line"},
		{name: "a row with a cell too many", readme: head + top + "| `kubernetes.CreateA` | v1 A | kind | `a` | x | y | z |\n" + tail, wantProblem: "13: the kind inventory section reads"},
		{name: "a row without outer pipes", readme: head + top + rowA + "`kubernetes.CreateB` | v1 B | missing | - | - | -\n" + tail, wantProblem: "14: the kind inventory section reads"},
		{name: "a duplicate row", readme: head + top + rowA + rowA + tail, wantProblem: "14: second kind inventory row for kubernetes.CreateA"},
		{name: "a second table", readme: head + top + rowA + "\n" + top + rowB + tail, wantProblem: "15: the kind inventory section holds one table"},
		{name: "a second table without outer pipes", readme: head + top + rowA + "\nConstructor | Kind | Status | Type | Decode | Notes\n---|---|---|---|---|---\n`kubernetes.CreateB` | v1 B | missing | - | - | -\n" + tail, wantProblem: "15: the kind inventory section reads"},
		{name: "a table before the inventory's own", readme: head + "| Status | Meaning |\n|---|---|\n\n" + top + rowA + tail, wantProblem: "11: the kind inventory section reads"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, problem := parseKindInventory(tt.readme)
			if tt.wantProblem != "" {
				if !strings.HasPrefix(problem, tt.wantProblem) {
					t.Fatalf("problem = %q, want it to start with %q", problem, tt.wantProblem)
				}
				return
			}
			if problem != "" {
				t.Fatalf("unexpected problem: %s", problem)
			}
			if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, tt.want) {
				t.Errorf("rows = %v, want %v", got, tt.want)
			}
		})
	}
}

// kureConstructorCalls returns which of constructors the non-test Go files of
// dir call, in the same "<package directory>.Create<Kind>" form.
func kureConstructorCalls(t *testing.T, dir string, constructors map[string]bool) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	called := map[string]bool{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Mode 0: the parser resolves identifiers, which the walk relies on.
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Join(dir, name), err)
		}
		calls, problem := kureConstructorCallsIn(fset, parsed, constructors)
		if problem != "" {
			t.Fatalf("%s; this walk cannot tell whether the constructor is called, update this test", problem)
		}
		maps.Copy(called, calls)
	}
	return called
}

// kureConstructorCallsIn returns which of constructors one file calls. A call
// is a call expression whose function is a selector on a name the file
// imports a package of the base library's kubernetes tree under, where that
// name is the package: the parser's identifier resolution gives a qualifier
// an object when a declaration in the file is in scope for it (a parameter or
// a variable of the same name), and none when it denotes the import. A
// declaration of that name in another file of the package cannot exist beside
// the import, so the file alone decides. The file must have been parsed with
// that resolution.
//
// It returns a problem for what it cannot attribute: a dot or blank import of
// such a package, and a constructor that is named without being called (stored
// in a variable, say), which may or may not run.
func kureConstructorCallsIn(fset *token.FileSet, file *ast.File, constructors map[string]bool) (called map[string]bool, problem string) {
	if file.Scope == nil {
		return nil, fmt.Sprintf("%s was parsed without identifier resolution", fset.Position(file.Pos()).Filename)
	}
	// local import name -> package directory, for the base library's
	// kubernetes packages only.
	imports := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Sprintf("%s: import path %s: %v", fset.Position(spec.Pos()), spec.Path.Value, err)
		}
		if importPath != kureKubernetesImportPath && !strings.HasPrefix(importPath, kureKubernetesImportPath+"/") {
			continue
		}
		local := path.Base(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		if local == "." || local == "_" {
			return nil, fmt.Sprintf("%s: %s is imported as %q", fset.Position(spec.Pos()), importPath, local)
		}
		imports[local] = path.Base(importPath)
	}

	called = map[string]bool{}
	callees := map[ast.Expr]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			// Visited before the selector it calls.
			callees[ast.Unparen(n.Fun)] = true
		case *ast.SelectorExpr:
			ident, ok := n.X.(*ast.Ident)
			if !ok || ident.Obj != nil {
				return true
			}
			dir, ok := imports[ident.Name]
			if !ok {
				return true
			}
			key := dir + "." + n.Sel.Name
			if _, generated := constructors[key]; !generated {
				return true
			}
			if callees[n] {
				called[key] = true
			} else if problem == "" {
				problem = fmt.Sprintf("%s: %s is named without being called", fset.Position(n.Pos()), key)
			}
		}
		return true
	})
	if problem != "" {
		return nil, problem
	}
	return called, ""
}

// TestKindInventory_CallSiteWalk pins what the call-site walk counts, on
// source it cannot find in the two packages it reads.
func TestKindInventory_CallSiteWalk(t *testing.T) {
	constructors := map[string]bool{"kubernetes.CreateSecret": false, "cilium.CreateCiliumNetworkPolicy": false}
	const header = "package p\n\n"
	tests := []struct {
		name, src   string
		want        []string
		wantProblem string
	}{
		{
			name: "a call under the default import name",
			src:  `import "github.com/go-kure/kure/pkg/kubernetes"` + "\n" + `var _ = kubernetes.CreateSecret("a", "b")`,
			want: []string{"kubernetes.CreateSecret"},
		},
		{
			name: "calls under local import names",
			src: `import (k8s "github.com/go-kure/kure/pkg/kubernetes"; cil "github.com/go-kure/kure/pkg/kubernetes/cilium")` + "\n" +
				`var _, _ = k8s.CreateSecret("a", "b"), cil.CreateCiliumNetworkPolicy("a", "b")`,
			want: []string{"cilium.CreateCiliumNetworkPolicy", "kubernetes.CreateSecret"},
		},
		{
			name: "a call in parentheses",
			src:  `import "github.com/go-kure/kure/pkg/kubernetes"` + "\n" + `var _ = ((kubernetes.CreateSecret))("a", "b")`,
			want: []string{"kubernetes.CreateSecret"},
		},
		{
			name: "calls beside expressions of other imports",
			src: `import ("example.com/stack"; "github.com/go-kure/kure/pkg/kubernetes"; "github.com/go-kure/kure/pkg/kubernetes/cilium")` + "\n" +
				`func f(app *stack.Application) map[string]any {` + "\n" +
				`	return map[string]any{app.Name: kubernetes.CreateSecret(app.Name, app.Namespace), stack.Key: cilium.CreateCiliumNetworkPolicy(app.Name, stack.Namespace)}` + "\n" +
				`}`,
			want: []string{"cilium.CreateCiliumNetworkPolicy", "kubernetes.CreateSecret"},
		},
		{
			name: "a call in a closure, after a later shadowing declaration's scope has not begun",
			src: `import "github.com/go-kure/kure/pkg/kubernetes"` + "\n" +
				`func f() { g := func() any { return kubernetes.CreateSecret("a", "b") }; kubernetes := g(); _ = kubernetes }`,
			want: []string{"kubernetes.CreateSecret"},
		},
		{
			name: "a function that is not a generated constructor",
			src:  `import "github.com/go-kure/kure/pkg/kubernetes"` + "\n" + `var _ = kubernetes.CreateSomethingElse("a")`,
		},
		{
			name: "another module's package of the same name",
			src:  `import "example.com/other/kubernetes"` + "\n" + `var _ = kubernetes.CreateSecret("a", "b")`,
		},
		{
			name: "a parameter that shadows the import",
			src: `import "github.com/go-kure/kure/pkg/kubernetes"` + "\n" + `var _ = kubernetes.CreateSomethingElse("a")` + "\n" +
				`func f(kubernetes interface{ CreateSecret(string, string) }) { kubernetes.CreateSecret("a", "b") }`,
		},
		{
			name: "a local variable that shadows the import",
			src: `import "github.com/go-kure/kure/pkg/kubernetes"` + "\n" + `var _ = kubernetes.CreateSomethingElse("a")` + "\n" +
				`type builder struct{}` + "\n" + `func (builder) CreateSecret(string, string) {}` + "\n" +
				`func f() { kubernetes := builder{}; kubernetes.CreateSecret("a", "b") }`,
		},
		{
			name: "a field and a method named like the import, beside a call",
			src: `import "github.com/go-kure/kure/pkg/kubernetes"` + "\n" + `type config struct{ kubernetes string }` + "\n" +
				`func (c config) cilium() string { return c.kubernetes }` + "\n" +
				`var _ = kubernetes.CreateSecret(config{kubernetes: "a"}.cilium(), "b")`,
			want: []string{"kubernetes.CreateSecret"},
		},
		{
			name:        "a constructor named without being called",
			src:         `import "github.com/go-kure/kure/pkg/kubernetes"` + "\n" + `var _ = kubernetes.CreateSecret`,
			wantProblem: "kubernetes.CreateSecret is named without being called",
		},
		{
			name:        "a dot import",
			src:         `import . "github.com/go-kure/kure/pkg/kubernetes"` + "\n" + `var _ = CreateSecret("a", "b")`,
			wantProblem: `is imported as "."`,
		},
		{
			name:        "a blank import",
			src:         `import _ "github.com/go-kure/kure/pkg/kubernetes/cilium"`,
			wantProblem: `is imported as "_"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "src.go", header+tt.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			called, problem := kureConstructorCallsIn(fset, file, constructors)
			if tt.wantProblem != "" {
				if !strings.Contains(problem, tt.wantProblem) {
					t.Fatalf("problem = %q, want it to contain %q", problem, tt.wantProblem)
				}
				return
			}
			if problem != "" {
				t.Fatalf("unexpected problem: %s", problem)
			}
			if got := slices.Sorted(maps.Keys(called)); !slices.Equal(got, tt.want) {
				t.Errorf("called = %v, want %v", got, tt.want)
			}
		})
	}
}
