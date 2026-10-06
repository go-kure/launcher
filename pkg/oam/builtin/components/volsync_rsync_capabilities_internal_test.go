package components

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/errors"
)

// volsyncMoverSources is where the linked module keeps its operator's movers,
// one directory a mover.
const volsyncMoverSources = "internal/controller/mover/"

// moverContainer is what the operator's source says of the container a mover
// runs: the capabilities it adds and drops and the three settings beside
// them, each as the source writes it.
type moverContainer struct {
	add                      []string
	drop                     string
	privileged               string
	allowPrivilegeEscalation string
	runAsUser                string
}

// readMoverContainer reads the one container a mover's source writes. It
// returns an error, and no answer, wherever the source is not of the form the
// answer is read from: no container list or several, a list of several
// containers, init containers, a security setting written twice or not at all,
// a capability that is no string literal.
func readMoverContainer(src []byte) (*moverContainer, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "mover.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.Wrap(err, "parse")
	}
	text := func(n ast.Node) string {
		return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
	}
	var lists []*ast.CompositeLit
	values := map[string][]ast.Expr{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if node.Type != nil && text(node.Type) == "[]corev1.Container" {
				lists = append(lists, node)
			}
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok {
				values[key.Name] = append(values[key.Name], node.Value)
			}
		}
		return true
	})
	if len(lists) != 1 || len(lists[0].Elts) != 1 {
		return nil, errors.Errorf("the source writes %d container lists, want one list of one container", len(lists))
	}
	if n := len(values["InitContainers"]); n != 0 {
		return nil, errors.Errorf("the source writes init containers (%d), which this reading does not cover", n)
	}
	one := func(key string) (ast.Expr, error) {
		if n := len(values[key]); n != 1 {
			return nil, errors.Errorf("the source sets %s %d times, want once", key, n)
		}
		value := values[key][0]
		if value.Pos() < lists[0].Pos() || value.End() > lists[0].End() {
			return nil, errors.Errorf("the source sets %s outside the container", key)
		}
		return value, nil
	}
	out := &moverContainer{}
	for key, into := range map[string]*string{
		"Drop": &out.drop, "Privileged": &out.privileged,
		"AllowPrivilegeEscalation": &out.allowPrivilegeEscalation, "RunAsUser": &out.runAsUser,
	} {
		value, err := one(key)
		if err != nil {
			return nil, err
		}
		*into = text(value)
	}
	added, err := one("Add")
	if err != nil {
		return nil, err
	}
	list, ok := added.(*ast.CompositeLit)
	if !ok || list.Type == nil || text(list.Type) != "[]corev1.Capability" {
		return nil, errors.Errorf("the added capabilities are written as %s, want a []corev1.Capability literal", text(added))
	}
	for _, element := range list.Elts {
		literal, ok := element.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return nil, errors.Errorf("an added capability is written as %s, want a string literal", text(element))
		}
		name, err := strconv.Unquote(literal.Value)
		if err != nil {
			return nil, errors.Wrapf(err, "added capability %s", literal.Value)
		}
		out.add = append(out.add, name)
	}
	return out, nil
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
func TestVolsyncKinds_RsyncCapabilities(t *testing.T) {
	// The reading refuses a source it cannot answer from, and reads the list a
	// source holds.
	const container = `package rsync
func f() {
	job.Spec.Template.Spec.Containers = []corev1.Container{{
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{%s},
				Drop: []corev1.Capability{"ALL"},
			},
			Privileged: ptr.To(false),
			RunAsUser: ptr.To[int64](0),
		},
	}%s}
}
`
	source := func(added, more string) []byte {
		return []byte(strings.NewReplacer("{%s}", "{"+added+"}", "}%s}", "}"+more+"}").Replace(container))
	}
	if got, err := readMoverContainer(source(`"NET_ADMIN", "CHOWN"`, "")); err != nil || !slices.Equal(got.add, []string{"NET_ADMIN", "CHOWN"}) {
		t.Fatalf("the reading of a source that adds NET_ADMIN and CHOWN is %+v, %v", got, err)
	}
	for name, src := range map[string][]byte{
		"a second container":           source(`"CHOWN"`, ", {}"),
		"a capability that is a name":  source(`capChown`, ""),
		"no container":                 []byte("package rsync\n"),
		"a setting written twice":      []byte(strings.Replace(string(source(`"CHOWN"`, "")), "Privileged: ptr.To(false),", "Privileged: ptr.To(false), Privileged: ptr.To(true),", 1)),
		"a setting outside the list":   []byte(strings.Replace(string(source(`"CHOWN"`, "")), "RunAsUser: ptr.To[int64](0),", "", 1) + "var x = T{RunAsUser: ptr.To[int64](0)}\n"),
		"init containers beside it":    []byte(string(source(`"CHOWN"`, "")) + "var y = T{InitContainers: nil}\n"),
		"a source that does not parse": []byte("package"),
	} {
		if got, err := readMoverContainer(src); err == nil {
			t.Fatalf("the reading of a source with %s answers %+v, want an error", name, got)
		}
	}

	dir := linkedModuleDir(t, volsyncModulePath)
	if !strings.HasSuffix(dir, "@"+volsyncOperatorVersion) {
		t.Fatalf("the linked module is at %s, and volsyncOperatorVersion says %s: read the rsync mover's source at the linked version and restate both", dir, volsyncOperatorVersion)
	}
	path := filepath.Join(dir, filepath.FromSlash(volsyncMoverSources+"rsync/mover.go"))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the rsync mover's source is not where it was at %s, so the capabilities the kind states cannot be held to it: %v", volsyncOperatorVersion, err)
	}
	got, err := readMoverContainer(src)
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
	} {
		if pair[0] != pair[1] {
			t.Errorf("the rsync mover's container: %s is written %s, and the kind's documentation says %s", what, pair[0], pair[1])
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
