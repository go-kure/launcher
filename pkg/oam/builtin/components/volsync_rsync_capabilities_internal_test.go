package components

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
)

// volsyncMoverSources is where the linked module keeps its operator's movers,
// one directory a mover, and volsyncHelperSources the package of helpers the
// movers hand a job to, which their sources import as utils.
const (
	volsyncMoverSources  = "internal/controller/mover/"
	volsyncHelperSources = "internal/controller/utils"
)

// moverContainer is what the operator's source says of the container a mover
// runs: where the source writes it, the capabilities it adds and drops and the
// three settings beside them, each as the source writes it, and the functions
// the job that carries the container is handed to.
type moverContainer struct {
	where                    string
	add                      []string
	drop                     string
	privileged               string
	allowPrivilegeEscalation string
	runAsUser                string
	handedTo                 []string
}

// podSource is Go source parsed to be read for what it writes of a pod's
// containers: the files of one package, by name.
type podSource struct {
	fset  *token.FileSet
	files []*ast.File
	texts map[string][]byte
}

func parsePodSource(sources map[string][]byte) (*podSource, error) {
	p := &podSource{fset: token.NewFileSet(), texts: sources}
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		file, err := parser.ParseFile(p.fset, name, sources[name], parser.SkipObjectResolution)
		if err != nil {
			return nil, errors.Wrap(err, "parse")
		}
		p.files = append(p.files, file)
	}
	return p, nil
}

func (p *podSource) text(n ast.Node) string {
	from, to := p.fset.Position(n.Pos()), p.fset.Position(n.End())
	return string(p.texts[from.Filename][from.Offset:to.Offset])
}

// function is the one package-level function of that name, nil when the
// source has none or several.
func (p *podSource) function(name string) *ast.FuncDecl {
	var found []*ast.FuncDecl
	for _, file := range p.files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
				found = append(found, fn)
			}
		}
	}
	if len(found) != 1 {
		return nil
	}
	return found[0]
}

// walkWithParents visits every node under root with its ancestors, the
// nearest last.
func walkWithParents(root ast.Node, visit func(n ast.Node, parents []ast.Node)) {
	var parents []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			parents = parents[:len(parents)-1]
			return false
		}
		visit(n, parents)
		parents = append(parents, n)
		return true
	})
}

// rootIdent is the name a chain of field selections starts from, nil for any
// other expression.
func rootIdent(expr ast.Expr) *ast.Ident {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e
		case *ast.SelectorExpr:
			expr = e.X
		default:
			return nil
		}
	}
}

// unreadWrites lists, by position, what under root this reading cannot answer
// for. Outside the one container list (nil where root may hold none) that is
// every mention of a container type, of a container's capabilities or
// security context and of init or ephemeral containers, and every use of a
// pod's containers but three: the assignment of that list, a field written on
// one container by index, and a range over them. A pod's own security context
// (<pod>.Spec.SecurityContext) is not a container's and is left.
func (p *podSource) unreadWrites(root ast.Node, list *ast.CompositeLit) []string {
	var out []string
	walkWithParents(root, func(n ast.Node, parents []ast.Node) {
		ident, ok := n.(*ast.Ident)
		if !ok || (list != nil && ident.Pos() >= list.Pos() && ident.End() <= list.End()) {
			return
		}
		up := func(levels int) ast.Node {
			if levels > len(parents) {
				return nil
			}
			return parents[len(parents)-levels]
		}
		selector, _ := up(1).(*ast.SelectorExpr)
		if selector != nil && selector.Sel != ident {
			selector = nil
		}
		known := true
		switch ident.Name {
		case "Container", "Capabilities", "Capability", "InitContainers", "EphemeralContainers":
			known = false
		case "SecurityContext":
			known = false
			if selector != nil {
				if of, ok := selector.X.(*ast.SelectorExpr); ok && of.Sel.Name == "Spec" {
					known = true
				}
			}
		case "Containers":
			known = false
			if selector == nil {
				break
			}
			switch use := up(2).(type) {
			case *ast.AssignStmt:
				known = list != nil && use.Tok == token.ASSIGN &&
					len(use.Lhs) == 1 && use.Lhs[0] == selector && len(use.Rhs) == 1 && use.Rhs[0] == list
			case *ast.RangeStmt:
				known = use.X == selector
			case *ast.IndexExpr:
				field, _ := up(3).(*ast.SelectorExpr)
				write, _ := up(4).(*ast.AssignStmt)
				known = use.X == selector && field != nil && field.X == use &&
					write != nil && write.Tok == token.ASSIGN && len(write.Lhs) == 1 && write.Lhs[0] == field
			}
		}
		if !known {
			out = append(out, p.fset.Position(ident.Pos()).String()+": "+ident.Name)
		}
	})
	return out
}

// readMoverContainer reads the one container the source of a mover's package
// writes, from every file of it. It returns an error, and no answer, wherever
// the source is not of the form the answer is read from: no container list or
// several, a list of several containers, a list that is not assigned to the
// containers of a named object, anything unreadWrites lists (a capability
// appended after the list, a container appended or reached by its address, an
// init container, a container or a security setting written in another file),
// a security setting written twice or not at all, a capability that is no
// string literal.
func readMoverContainer(sources map[string][]byte) (*moverContainer, error) {
	p, err := parsePodSource(sources)
	if err != nil {
		return nil, err
	}
	var lists []*ast.CompositeLit
	var owners []ast.Node
	values := map[string][]ast.Expr{}
	for _, file := range p.files {
		walkWithParents(file, func(n ast.Node, parents []ast.Node) {
			switch node := n.(type) {
			case *ast.CompositeLit:
				if node.Type != nil && p.text(node.Type) == "[]corev1.Container" {
					lists = append(lists, node)
					owners = append(owners, parents[len(parents)-1])
				}
			case *ast.KeyValueExpr:
				if key, ok := node.Key.(*ast.Ident); ok {
					values[key.Name] = append(values[key.Name], node.Value)
				}
			}
		})
	}
	if len(lists) != 1 || len(lists[0].Elts) != 1 {
		return nil, errors.Errorf("the source writes %d container lists, want one list of one container", len(lists))
	}
	list := lists[0]
	var job *ast.Ident
	if assign, ok := owners[0].(*ast.AssignStmt); ok && assign.Tok == token.ASSIGN && len(assign.Lhs) == 1 && len(assign.Rhs) == 1 {
		if target, ok := assign.Lhs[0].(*ast.SelectorExpr); ok && target.Sel.Name == "Containers" {
			job = rootIdent(target)
		}
	}
	if job == nil {
		return nil, errors.New("the source does not assign its container list to the containers of a named object")
	}
	var unread []string
	for _, file := range p.files {
		unread = append(unread, p.unreadWrites(file, list)...)
	}
	if len(unread) > 0 {
		return nil, errors.Errorf("the source writes or mentions a pod's containers where this reading does not cover it: %s", strings.Join(unread, "; "))
	}
	one := func(key string) (ast.Expr, error) {
		if n := len(values[key]); n != 1 {
			return nil, errors.Errorf("the source sets %s %d times, want once", key, n)
		}
		value := values[key][0]
		if value.Pos() < list.Pos() || value.End() > list.End() {
			return nil, errors.Errorf("the source sets %s outside the container", key)
		}
		return value, nil
	}
	from, to := p.fset.Position(list.Pos()), p.fset.Position(list.End())
	out := &moverContainer{where: from.Filename + ", lines " + strconv.Itoa(from.Line) + " to " + strconv.Itoa(to.Line)}
	for key, into := range map[string]*string{
		"Drop": &out.drop, "Privileged": &out.privileged,
		"AllowPrivilegeEscalation": &out.allowPrivilegeEscalation, "RunAsUser": &out.runAsUser,
	} {
		value, err := one(key)
		if err != nil {
			return nil, err
		}
		*into = p.text(value)
	}
	added, err := one("Add")
	if err != nil {
		return nil, err
	}
	capabilities, ok := added.(*ast.CompositeLit)
	if !ok || capabilities.Type == nil || p.text(capabilities.Type) != "[]corev1.Capability" {
		return nil, errors.Errorf("the added capabilities are written as %s, want a []corev1.Capability literal", p.text(added))
	}
	for _, element := range capabilities.Elts {
		literal, ok := element.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return nil, errors.Errorf("an added capability is written as %s, want a string literal", p.text(element))
		}
		name, err := strconv.Unquote(literal.Value)
		if err != nil {
			return nil, errors.Wrapf(err, "added capability %s", literal.Value)
		}
		out.add = append(out.add, name)
	}
	out.handedTo = p.handedTo(job.Name)
	return out, nil
}

// handedTo names, sorted, every function the source calls with the object of
// that name, a field of it or the address of either as an argument.
func (p *podSource) handedTo(object string) []string {
	callees := map[string]bool{}
	for _, file := range p.files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, arg := range call.Args {
				if address, ok := arg.(*ast.UnaryExpr); ok && address.Op == token.AND {
					arg = address.X
				}
				if root := rootIdent(arg); root != nil && root.Name == object {
					callees[p.text(call.Fun)] = true
				}
			}
			return true
		})
	}
	return slices.Sorted(maps.Keys(callees))
}

// packageSources reads the Go files of one directory of the linked module
// that are no tests, by file name.
func packageSources(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("a package of the operator is not where it was at %s, so the capabilities the kind states cannot be held to it: %v", volsyncOperatorVersion, err)
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = src
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no Go source", dir)
	}
	return out
}

// privilegedParameter is the name the builder of one mover gives the last
// parameter of FromSource or FromDestination, the namespace's answer on
// privileged movers: "_" where the builder drops it.
func privilegedParameter(t *testing.T, moduleDir, mover, function string) string {
	t.Helper()
	path := filepath.Join(moduleDir, filepath.FromSlash(volsyncMoverSources+mover+"/builder.go"))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("the builder of the %s mover cannot be read where it was at %s: %v", mover, volsyncOperatorVersion, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function || fn.Recv == nil {
			continue
		}
		params := fn.Type.Params.List
		if len(params) == 0 {
			break
		}
		last := params[len(params)-1]
		if typ, ok := last.Type.(*ast.Ident); !ok || typ.Name != "bool" || len(last.Names) != 1 {
			t.Fatalf("%s of the %s mover's builder no longer ends in one bool parameter", function, mover)
		}
		return last.Names[0].Name
	}
	t.Fatalf("the %s mover's builder has no method %s", mover, function)
	return ""
}

// TestVolsyncKinds_RsyncCapabilities holds volsyncRsyncCapabilities, the
// capabilities an authored rsync mover is held to the policy for, to the
// operator's source in the linked module: the list the kind states is the list
// that source adds to the mover's one container, which it also runs as root,
// not privileged, with every other capability dropped. It holds beside it what
// makes authoring that mover the choice: the mover's builder drops the
// namespace's answer on privileged movers, and the builders of the four other
// movers take it. A dependency bump that changes any of it fails here, and so
// does one that moves or rewrites the source: nothing is skipped.
//
// The reading follows a pod's containers by the names of the fields and types
// that hold them. It covers every source file of the mover's package, and the
// functions of the operator's own helper package that the job is handed to;
// the functions the job is handed to are themselves a stated list, so a new
// one fails here until it is read. It does not follow a job, a pod template or
// a pod spec carried under another name into a function outside those, nor
// what a function of another module does with a job: the four such callees at
// the linked version are controller-runtime's and client-go's, and read the
// job's key, set its owner reference, delete it and record an event about it.
func TestVolsyncKinds_RsyncCapabilities(t *testing.T) {
	testReadMoverContainerGuards(t)

	dir := linkedModuleDir(t, volsyncModulePath)
	if !strings.HasSuffix(dir, "@"+volsyncOperatorVersion) {
		t.Fatalf("the linked module is at %s, and volsyncOperatorVersion says %s: read the rsync mover's source at the linked version and restate both", dir, volsyncOperatorVersion)
	}
	got, err := readMoverContainer(packageSources(t, filepath.Join(dir, filepath.FromSlash(volsyncMoverSources+"rsync"))))
	if err != nil {
		t.Fatalf("the rsync mover's source is no longer of the form the capabilities are read from, so the kind's list cannot be held to it: %v", err)
	}
	want := make([]string, 0, len(volsyncRsyncCapabilities))
	for _, capability := range volsyncRsyncCapabilities {
		want = append(want, string(capability))
	}
	if !slices.Equal(got.add, want) {
		t.Errorf("the operator adds %v to the rsync mover's container\nvolsyncRsyncCapabilities says %v", got.add, want)
	}
	for what, pair := range map[string][2]string{
		"drops":                    {got.drop, `[]corev1.Capability{"ALL"}`},
		"privileged":               {got.privileged, "ptr.To(false)"},
		"allowPrivilegeEscalation": {got.allowPrivilegeEscalation, "ptr.To(false)"},
		"runAsUser":                {got.runAsUser, "ptr.To[int64](0)"},
		// The comment on volsyncRsyncCapabilities cites it.
		"its place in the source": {got.where, "mover.go, lines 409 to 432"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("the rsync mover's container: %s is written %s, and the kind's documentation says %s", what, pair[0], pair[1])
		}
	}

	// Every function the job is handed to. Those of the operator's helper
	// package are read below. The four others are controller-runtime's and
	// client-go's (the job's key, its owner reference, its deletion and an
	// event about it) and are not read.
	handedTo := []string{
		"client.ObjectKeyFromObject", "ctrl.SetControllerReference", "m.client.Delete", "m.eventRecorder.Eventf",
		"utils.AddAllLabels", "utils.CreateOrUpdateDeleteOnImmutableErr", "utils.KindAndName", "utils.MarkForCleanup",
		"utils.SetOwnedByVolSync", "utils.UpdatePodTemplateSpecFromMoverConfig",
	}
	if !slices.Equal(got.handedTo, handedTo) {
		t.Fatalf("the rsync mover hands its job to %v\nthis test has read the source for %v: read what a new one does to the job's containers and restate the list", got.handedTo, handedTo)
	}
	helpers, err := parsePodSource(packageSources(t, filepath.Join(dir, filepath.FromSlash(volsyncHelperSources))))
	if err != nil {
		t.Fatalf("the operator's helper package: %v", err)
	}
	for _, callee := range handedTo {
		name, ok := strings.CutPrefix(callee, "utils.")
		if !ok {
			continue
		}
		fn := helpers.function(name)
		if fn == nil {
			t.Fatalf("the operator's helper package does not declare %s once, so what it does to the job's containers cannot be read", name)
		}
		if unread := helpers.unreadWrites(fn, nil); len(unread) > 0 {
			t.Errorf("%s, which the rsync mover hands its job to, writes or mentions a pod's containers where this reading does not cover it: %v", callee, unread)
		}
	}

	for _, function := range []string{"FromSource", "FromDestination"} {
		if name := privilegedParameter(t, dir, "rsync", function); name != "_" {
			t.Errorf("%s of the rsync mover's builder names the namespace's answer %q: it dropped it at %s, which is why authoring the mover is the choice", function, name, volsyncOperatorVersion)
		}
	}
	taking := map[string][]string{
		"rsynctls": {"FromSource", "FromDestination"}, "rclone": {"FromSource", "FromDestination"},
		"restic": {"FromSource", "FromDestination"}, "syncthing": {"FromSource"},
	}
	for mover, functions := range taking {
		for _, function := range functions {
			if name := privilegedParameter(t, dir, mover, function); name != "privileged" {
				t.Errorf("%s of the %s mover's builder names the namespace's answer %q, want privileged: the kind's documentation says that builder takes it", function, mover, name)
			}
		}
	}
}

// testReadMoverContainerGuards shows on made-up sources that the reading
// answers from a source of the form it knows and refuses, with an error, every
// form it cannot answer from.
func testReadMoverContainerGuards(t *testing.T) {
	t.Helper()
	const mover = `package rsync
func f() {
	job.Spec.Template.Spec.Containers = []corev1.Container{{
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{ADDED},
				Drop: []corev1.Capability{"ALL"},
			},
			Privileged: ptr.To(false),
			RunAsUser: ptr.To[int64](0),
		},
	}MORE}
	job.Spec.Template.Spec.Containers[0].VolumeMounts = mounts
	utils.SetOwnedByVolSync(&job.Spec.Template)
	other.Read(ctx, job)
	AFTER
}
`
	source := func(added, more, after string) map[string][]byte {
		return map[string][]byte{"mover.go": []byte(strings.NewReplacer("ADDED", added, "MORE", more, "AFTER", after).Replace(mover))}
	}
	edited := func(old, replacement string) map[string][]byte {
		return map[string][]byte{"mover.go": []byte(strings.Replace(string(source(`"CHOWN"`, "", "")["mover.go"]), old, replacement, 1))}
	}
	beside := func(file string) map[string][]byte {
		out := source(`"CHOWN"`, "", "")
		out["other.go"] = []byte("package rsync\n" + file)
		return out
	}

	got, err := readMoverContainer(beside("func g() { pod.Spec.SecurityContext = nil }\n"))
	if err != nil || !slices.Equal(got.add, []string{"CHOWN"}) ||
		!slices.Equal(got.handedTo, []string{"other.Read", "utils.SetOwnedByVolSync"}) || got.where != "mover.go, lines 3 to 13" {
		t.Fatalf("the reading of a source that adds CHOWN and hands its job to two functions is %+v, %v", got, err)
	}
	if got, err := readMoverContainer(source(`"NET_ADMIN", "CHOWN"`, "", "")); err != nil || !slices.Equal(got.add, []string{"NET_ADMIN", "CHOWN"}) {
		t.Fatalf("the reading of a source that adds NET_ADMIN and CHOWN is %+v, %v", got, err)
	}
	const containers = "job.Spec.Template.Spec.Containers"
	for name, src := range map[string]map[string][]byte{
		"a second container":                       source(`"CHOWN"`, ", {}", ""),
		"a capability that is a name":              source(`capChown`, "", ""),
		"no container":                             {"mover.go": []byte("package rsync\n")},
		"a setting written twice":                  edited("Privileged: ptr.To(false),", "Privileged: ptr.To(false), Privileged: ptr.To(true),"),
		"a setting outside the list":               beside("var x = T{RunAsUser: ptr.To[int64](0)}\n"),
		"a setting not written":                    edited("RunAsUser: ptr.To[int64](0),", ""),
		"a source that does not parse":             {"mover.go": []byte("package")},
		"a list that is not assigned":              edited(containers+" = ", "list := "),
		"a list assigned beside another":           edited(containers+" = ", containers+", x = nil, "),
		"a capability appended after the list":     source(`"CHOWN"`, "", "sc := "+containers+"[0].SecurityContext\n\tsc.Capabilities.Add = append(sc.Capabilities.Add, \"NET_ADMIN\")"),
		"a capability appended through a name":     source(`"CHOWN"`, "", "c := &"+containers+"[0]\n\tgrant(c)"),
		"a container appended":                     source(`"CHOWN"`, "", containers+" = append("+containers+", sidecar)"),
		"the containers handed to a function":      source(`"CHOWN"`, "", "extend("+containers+")"),
		"the containers assigned again":            source(`"CHOWN"`, "", containers+" = others"),
		"a security context written by index":      source(`"CHOWN"`, "", containers+"[0].SecurityContext = unconfined"),
		"init containers through a selector":       source(`"CHOWN"`, "", "job.Spec.Template.Spec.InitContainers = initial"),
		"ephemeral containers through a selector":  source(`"CHOWN"`, "", "job.Spec.Template.Spec.EphemeralContainers = debug"),
		"init containers in another file":          beside("var y = T{InitContainers: nil}\n"),
		"a container written in another file":      beside("func g(job *batchv1.Job) { " + containers + "[0].SecurityContext = nil }\n"),
		"a container type in another file":         beside("func g() corev1.Container { return sidecar }\n"),
		"capabilities in another file":             beside("func g(sc *T) { sc.Capabilities = nil }\n"),
		"a pod spec's containers in another file":  beside("func g(spec *T) { spec.Containers = append(spec.Containers, sidecar) }\n"),
		"a container's context through a variable": beside("func g(c *T) { c.SecurityContext = nil }\n"),
	} {
		if got, err := readMoverContainer(src); err == nil {
			t.Fatalf("the reading of a source with %s answers %+v, want an error", name, got)
		}
	}

	helper, err := parsePodSource(map[string][]byte{"utils.go": []byte(`package utils
func Known(t *T) {
	t.Spec.SecurityContext = config.MoverSecurityContext
	for i := range t.Spec.Containers {
		t.Spec.Containers[i].Resources = resources
	}
}
func Index(t *T) { t.Spec.Containers[0].SecurityContext = nil }
func Address(t *T) { c := &t.Spec.Containers[0]; grant(c) }
func Init(t *T) { t.Spec.InitContainers = initial }
func Append(t *T) { t.Spec.Containers = append(t.Spec.Containers, sidecar) }
func Assign(t *T) { t.Spec.Containers = others }
func Value(t *T) { for _, c := range t.Spec.Containers { c.SecurityContext.Capabilities.Add = nil } }
func Twice() {}
func Twice() {}
`)})
	if err != nil {
		t.Fatal(err)
	}
	if unread := helper.unreadWrites(helper.function("Known"), nil); len(unread) > 0 {
		t.Fatalf("a helper that writes a pod's own security context and its containers' resources is not read: %v", unread)
	}
	for _, name := range []string{"Index", "Address", "Init", "Append", "Assign", "Value"} {
		if unread := helper.unreadWrites(helper.function(name), nil); len(unread) == 0 {
			t.Fatalf("the helper %s writes or reaches a container, and the reading lists nothing", name)
		}
	}
	if helper.function("Twice") != nil || helper.function("Absent") != nil {
		t.Fatal("a function declared twice or not at all is answered")
	}
}
