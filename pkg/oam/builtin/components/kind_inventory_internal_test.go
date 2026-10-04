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

// generatedConstructor is what a generated Create<Kind> constructor says of its
// kind: the API version its doc comment names, and whether the kind is
// cluster-scoped.
type generatedConstructor struct {
	apiVersion    string
	clusterScoped bool
}

// TestKindInventory_CoversEveryConstructor holds the README's kind inventory to
// the base library: every generated Create<Kind> constructor of the linked
// module has exactly one row, every row names one, and a row's Kind cell gives
// the constructor's API version, kind and scope. A base-library bump that adds
// or removes a constructor, or moves a kind to another API version, fails
// here, naming it. It also checks what a row must carry for its status.
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
		constructor, ok := constructors[name]
		if !ok {
			t.Errorf("README.md:%d: stale row %s: the linked %s has no such generated constructor", row.line, name, kureModulePath)
			continue
		}
		// The Kind cell is "<apiVersion> <Kind>", with "(cluster-scoped)" after
		// it exactly when the kind is.
		want := []string{constructor.apiVersion, strings.TrimPrefix(name[strings.Index(name, ".")+1:], "Create")}
		if constructor.clusterScoped {
			want = append(want, "(cluster-scoped)")
		}
		if got := strings.Fields(row.kind); !slices.Equal(got, want) {
			t.Errorf("README.md:%d: %s: Kind cell is %q, want %q", row.line, name, row.kind, strings.Join(want, " "))
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
// from ../traits or from this package, and a missing or not authorable row's
// from neither. So a component or trait for a missing kind fails here until
// its row says so, and a row cannot claim one that builds nothing.
//
// It holds the Type column to the handlers: a kind or component row's Type is
// a type this package declares contract metadata for, a trait row's one
// ../traits does. So a row cannot name a type no handler has, and stops
// passing when its handler is removed.
//
// Not held: which handler makes the call (a kind row passes while its Type has
// a handler and any file of this package calls its constructor); whether a
// component row's constructor is called; a trait row whose kind gains a kind
// component, which the change adding the component updates; and code outside
// the two packages, which is not read.
func TestKindInventory_MatchesCallSites(t *testing.T) {
	constructors := kureGeneratedConstructors(t)
	hereFiles, traitFiles := parsePackageFiles(t, "."), parsePackageFiles(t, filepath.Join("..", "traits"))
	here := kureConstructorCalls(t, hereFiles, constructors)
	traits := kureConstructorCalls(t, traitFiles, constructors)
	hereTypes := contractFamilies(t, hereFiles)
	traitTypes := contractFamilies(t, traitFiles)
	// Vacuity guards: a walk that resolves no import finds no call, and every
	// missing row would then pass. Both packages hold far more than this.
	if len(here) < 10 || len(traits) < 5 || len(hereTypes) < 10 || len(traitTypes) < 5 {
		t.Fatalf("found %d constructors called and %d types declared here, %d and %d in ../traits, want >= 10 here and >= 5 there; the walk is broken", len(here), len(hereTypes), len(traits), len(traitTypes))
	}
	rows := readKindInventory(t)
	for _, name := range slices.Sorted(maps.Keys(rows)) {
		row := rows[name]
		// A Type cell is one type in backticks; the status says whose.
		checkType := func(types map[string]bool, where string) {
			if typ := strings.Trim(row.typ, "`"); row.typ != "`"+typ+"`" || !types[typ] {
				t.Errorf("README.md:%d: %s has status %q and Type %q, want one type in backticks that a handler or lowering rule of %s declares contract metadata for", row.line, name, row.status, row.typ, where)
			}
		}
		switch row.status {
		case inventoryKind:
			if !here[name] {
				t.Errorf("README.md:%d: %s has status %q, but no file of this package calls it", row.line, name, row.status)
			}
			checkType(hereTypes, "this package")
		case inventoryComponent:
			checkType(hereTypes, "this package")
		case inventoryTrait:
			// A trait may build through a generator of this package, as the
			// configmap trait does, so either package's call satisfies the row.
			if !traits[name] && !here[name] {
				t.Errorf("README.md:%d: %s has status %q, but no file of ../traits or of this package calls it", row.line, name, row.status)
			}
			checkType(traitTypes, "../traits")
		case inventoryMissing, inventoryNotAuthorable:
			if here[name] || traits[name] {
				t.Errorf("README.md:%d: %s has status %q, but this package or ../traits calls it: update the row", row.line, name, row.status)
			}
		}
	}
}

// kureGeneratedConstructors returns the generated Create<Kind> constructors of
// the linked base library, keyed "<package directory>.Create<Kind>", with what
// each says of its kind. It reads every zz_generated_create.go under the
// module's pkg/kubernetes, at any depth, since Go cannot list a package's
// functions at run time, and fails when the module directory or those files
// cannot be found, or when a constructor's doc comment and its parameters (a
// name, and a namespace unless the kind is cluster-scoped) do not read as the
// generator writes them.
func kureGeneratedConstructors(t *testing.T) map[string]generatedConstructor {
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

	constructors := map[string]generatedConstructor{}
	fset := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments|parser.SkipObjectResolution)
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
			constructor, ok := parseConstructorDoc(fn.Name.Name, fn.Doc.Text())
			if !ok || constructor.clusterScoped != (params == 1) {
				t.Fatalf("%s: %s takes %d parameter(s) and its doc comment reads %q, want \"%s returns a [cluster-scoped] <apiVersion> <Kind> ...\", cluster-scoped exactly when it takes no namespace; update this test", file, fn.Name.Name, params, strings.TrimSpace(fn.Doc.Text()), fn.Name.Name)
			}
			constructors[key] = constructor
		}
	}
	// Vacuity guard: 141 at kure main 97dce7a4b760, over nine files.
	if len(files) < 9 || len(constructors) < 100 {
		t.Fatalf("found %d constructors in %d %s files under %s, want >= 100 in >= 9; the walk is broken", len(constructors), len(files), generated, root)
	}
	return constructors
}

// parseConstructorDoc reads the doc comment the base library's generator
// writes on the constructor name: "<name> returns a[n] [cluster-scoped]
// <apiVersion> <Kind> carrying ...", where <Kind> is name without "Create".
// It reports false for a comment in any other form.
func parseConstructorDoc(name, doc string) (generatedConstructor, bool) {
	fields := strings.Fields(doc)
	if len(fields) < 5 || fields[0] != name || fields[1] != "returns" || (fields[2] != "a" && fields[2] != "an") {
		return generatedConstructor{}, false
	}
	constructor, rest := generatedConstructor{}, fields[3:]
	if rest[0] == "cluster-scoped" {
		constructor.clusterScoped, rest = true, rest[1:]
	}
	if len(rest) < 2 || rest[1] != strings.TrimPrefix(name, "Create") {
		return generatedConstructor{}, false
	}
	constructor.apiVersion = rest[0]
	return constructor, true
}

// TestKindInventory_ConstructorDoc pins how a generated constructor's doc
// comment is read.
func TestKindInventory_ConstructorDoc(t *testing.T) {
	tests := []struct {
		name, constructor, doc string
		want                   generatedConstructor
		wantOK                 bool
	}{
		{name: "a namespaced kind", constructor: "CreateConfigMap", doc: "CreateConfigMap returns a v1 ConfigMap carrying TypeMeta and identity only.\n", want: generatedConstructor{apiVersion: "v1"}, wantOK: true},
		{name: "a cluster-scoped kind", constructor: "CreateNamespace", doc: "CreateNamespace returns a cluster-scoped v1 Namespace carrying TypeMeta and identity only.\n", want: generatedConstructor{apiVersion: "v1", clusterScoped: true}, wantOK: true},
		{name: "the other article", constructor: "CreateDeployment", doc: "CreateDeployment returns an apps/v1 Deployment carrying TypeMeta.\n", want: generatedConstructor{apiVersion: "apps/v1"}, wantOK: true},
		{name: "no comment", constructor: "CreateConfigMap"},
		{name: "another constructor's comment", constructor: "CreateConfigMap", doc: "CreateSecret returns a v1 Secret carrying TypeMeta and identity only.\n"},
		{name: "another kind than the name's", constructor: "CreateConfigMap", doc: "CreateConfigMap returns a v1 Secret carrying TypeMeta and identity only.\n"},
		{name: "no API version", constructor: "CreateConfigMap", doc: "CreateConfigMap returns a ConfigMap carrying TypeMeta and identity only.\n"},
		{name: "a scope and nothing after it", constructor: "CreateNamespace", doc: "CreateNamespace returns a cluster-scoped Namespace\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseConstructorDoc(tt.constructor, tt.doc)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("parseConstructorDoc = %+v, %t, want %+v, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
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

// packageFiles is the parsed non-test Go files of one package directory.
type packageFiles struct {
	fset  *token.FileSet
	files []*ast.File
}

// parsePackageFiles parses the non-test Go files of dir, with the parser's
// identifier resolution.
func parsePackageFiles(t *testing.T, dir string) packageFiles {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	pkg := packageFiles{fset: token.NewFileSet()}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Mode 0: the parser resolves identifiers, which the walks rely on.
		parsed, err := parser.ParseFile(pkg.fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Join(dir, name), err)
		}
		pkg.files = append(pkg.files, parsed)
	}
	return pkg
}

// kureConstructorCalls returns which of constructors the files of pkg call, in
// the same "<package directory>.Create<Kind>" form.
func kureConstructorCalls(t *testing.T, pkg packageFiles, constructors map[string]generatedConstructor) map[string]bool {
	t.Helper()
	called := map[string]bool{}
	for _, file := range pkg.files {
		calls, problem := kureConstructorCallsIn(pkg.fset, file, constructors)
		if problem != "" {
			t.Fatalf("%s; this walk cannot tell whether the constructor is called, update this test", problem)
		}
		maps.Copy(called, calls)
	}
	return called
}

// contractFamilies returns the types the files of pkg declare contract
// metadata for, failing on a declaration it cannot read.
func contractFamilies(t *testing.T, pkg packageFiles) map[string]bool {
	t.Helper()
	families, problem := contractFamiliesIn(pkg.fset, pkg.files)
	if problem != "" {
		t.Fatalf("%s; this walk cannot read the type it declares, update this test", problem)
	}
	return families
}

// contractFamiliesIn returns the first argument of every call the files of one
// package make to its function contract, through which each handler and
// lowering rule of the two packages declares its contract metadata under the
// type it handles. The argument is a string literal or a package-level
// constant declared as one; it returns a problem for any other.
//
// A local declaration named contract is not the function: the parser's
// identifier resolution gives the callee no object in a file that only uses
// the function, and a function object in the file that declares it.
func contractFamiliesIn(fset *token.FileSet, files []*ast.File) (families map[string]bool, problem string) {
	stringLiteral := func(expr ast.Expr) (string, bool) {
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(lit.Value)
		return value, err == nil
	}
	consts := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				spec := spec.(*ast.ValueSpec)
				for i, name := range spec.Names {
					if i >= len(spec.Values) {
						continue
					}
					if value, ok := stringLiteral(spec.Values[i]); ok {
						consts[name.Name] = value
					}
				}
			}
		}
	}

	families = map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee, ok := ast.Unparen(call.Fun).(*ast.Ident)
			if !ok || callee.Name != "contract" || (callee.Obj != nil && callee.Obj.Kind != ast.Fun) {
				return true
			}
			family, ok := "", false
			if len(call.Args) > 0 {
				switch arg := call.Args[0].(type) {
				case *ast.BasicLit:
					family, ok = stringLiteral(arg)
				case *ast.Ident:
					// A package-level constant has no object in a file that
					// only uses it, and a constant object in the file that
					// declares it; a parameter or a variable is neither.
					if arg.Obj == nil || arg.Obj.Kind == ast.Con {
						family, ok = consts[arg.Name]
					}
				}
			}
			if !ok && problem == "" {
				problem = fmt.Sprintf("%s: contract is called with a first argument that is neither a string literal nor a package-level string constant", fset.Position(call.Pos()))
			}
			if ok {
				families[family] = true
			}
			return true
		})
	}
	if problem != "" {
		return nil, problem
	}
	return families, ""
}

// TestKindInventory_ContractFamilies pins which calls declare a type.
func TestKindInventory_ContractFamilies(t *testing.T) {
	const declares = "package p\n\nconst helmType = \"helm\"\n\nfunc contract(typ string, keys ...string) string { return typ }\n\n"
	tests := []struct {
		name        string
		srcs        []string
		want        []string
		wantProblem string
	}{
		{name: "literals, with and without further arguments", srcs: []string{declares + `var _, _ = contract("configmap"), contract("certificate", "cert-manager")`}, want: []string{"certificate", "configmap"}},
		{name: "a constant, in its own file and in another", srcs: []string{declares + `var _ = contract(helmType)`, "package p\n\n" + `func f() string { return contract(helmType) + contract("job") }`}, want: []string{"helm", "job"}},
		{name: "a local function value of that name", srcs: []string{"package p\n\n" + `func f() string { contract := func(string) string { return "" }; return contract("job") }`}},
		{name: "a parameter", srcs: []string{declares + `func f(typ string) string { return contract(typ) }`}, wantProblem: "neither a string literal nor a package-level string constant"},
		{name: "a constant that is not a string literal", srcs: []string{declares + "const other = helmType + \"x\"\n\n" + `var _ = contract(other)`}, wantProblem: "neither a string literal nor a package-level string constant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			var files []*ast.File
			for i, src := range tt.srcs {
				file, err := parser.ParseFile(fset, fmt.Sprintf("src%d.go", i), src, 0)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				files = append(files, file)
			}
			got, problem := contractFamiliesIn(fset, files)
			if tt.wantProblem != "" {
				if !strings.Contains(problem, tt.wantProblem) {
					t.Fatalf("problem = %q, want it to contain %q", problem, tt.wantProblem)
				}
				return
			}
			if problem != "" {
				t.Fatalf("unexpected problem: %s", problem)
			}
			if families := slices.Sorted(maps.Keys(got)); !slices.Equal(families, tt.want) {
				t.Errorf("families = %v, want %v", families, tt.want)
			}
		})
	}
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
func kureConstructorCallsIn(fset *token.FileSet, file *ast.File, constructors map[string]generatedConstructor) (called map[string]bool, problem string) {
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
	constructors := map[string]generatedConstructor{"kubernetes.CreateSecret": {}, "cilium.CreateCiliumNetworkPolicy": {}}
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
