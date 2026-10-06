package components

import (
	"crypto/sha256"
	"encoding/hex"
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

// volsyncReadPackages are the two packages of the linked module in which the
// rsync mover's container is built: the mover's own, and the operator's helper
// package, which holds every function of the module that the mover hands its
// Job or the Job's pod template to. volsyncReadDigest is the SHA-256 over
// their Go files that are no tests, as they are at volsyncOperatorVersion and
// as they were read for volsyncRsyncCapabilities.
var volsyncReadPackages = []string{"internal/controller/mover/rsync", "internal/controller/utils"}

const volsyncReadDigest = "f88e7b4ffe4340136a9f4a87707ea59491e92bcbf2619992e167c6d91d192532"

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
// a capability that is no string literal. It is a reading of one literal, not
// a proof of what the source does to the container: volsyncReadDigest is what
// holds the source to the form that was read.
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

// sourceDigest is the SHA-256, in hexadecimal, over the Go files that are no
// tests of the named directories under root, each directory's files in the
// order of their names: every file's path from root, its size and its bytes.
// It returns an error for a directory that cannot be read or holds no such
// file.
func sourceDigest(root string, directories []string) (string, error) {
	var all []byte
	for _, directory := range directories {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(directory)))
		if err != nil {
			return "", errors.Wrap(err, directory)
		}
		files := 0
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(directory), name))
			if err != nil {
				return "", errors.Wrap(err, directory+"/"+name)
			}
			all = append(all, directory+"/"+name+"\x00"+strconv.Itoa(len(src))+"\x00"...)
			all = append(all, src...)
			files++
		}
		if files == 0 {
			return "", errors.Errorf("%s holds no Go source", directory)
		}
	}
	sum := sha256.Sum256(all)
	return hex.EncodeToString(sum[:]), nil
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
// operator's source in the linked module, in two ways. Nothing is skipped.
//
// It holds the source to the bytes that were read (volsyncReadDigest): the
// two packages the mover's container is built in are, file for file, what
// they were when a person read them at volsyncOperatorVersion. A dependency
// bump that changes a file of either fails here, whatever the change and also
// when it leaves the container alone, and so does one that moves a package.
//
// And it states what was read: the list the kind states is the list the
// source's one container literal adds, which that literal also runs as root,
// not privileged, with every other capability dropped; the mover's builder
// drops the namespace's answer on privileged movers, and the builders of the
// four other movers take it.
//
// When the digest fails after a bump, read the linked version again:
//  1. The file: internal/controller/mover/rsync/mover.go, ensureJob, with
//     every write to the Job's containers there and in the helper functions
//     the Job or its pod template is handed to (at v0.16.0 only
//     UpdatePodTemplateSpecFromMoverConfig writes them: their resources).
//  2. The literal: the one assigned to job.Spec.Template.Spec.Containers, its
//     securityContext (capabilities add and drop, privileged,
//     allowPrivilegeEscalation, runAsUser).
//  3. The builder parameters: in builder.go of each mover, the last one of
//     FromSource and FromDestination (rsync drops it as "_", the four others
//     name it privileged).
//
// Then restate volsyncRsyncCapabilities and the comment on it,
// volsyncOperatorVersion and volsyncReadDigest.
//
// Limits. Restating the digest is a person's act that means "I read the mover
// again"; the test cannot check that it was done. The other packages of the
// module are outside the digest, and so is what the functions of other
// modules that the Job is handed to do with it: at v0.16.0 those are
// controller-runtime's and client-go's, which read the Job's key, set its
// owner reference, create or update it, delete it, look up its kind and
// record an event about it.
func TestVolsyncKinds_RsyncCapabilities(t *testing.T) {
	testReadMoverContainerGuards(t)
	testSourceDigestGuards(t)

	dir := linkedModuleDir(t, volsyncModulePath)
	if !strings.HasSuffix(dir, "@"+volsyncOperatorVersion) {
		t.Fatalf("the linked module is at %s, and volsyncOperatorVersion says %s: read the rsync mover's source at the linked version (the procedure is in this test's comment) and restate both", dir, volsyncOperatorVersion)
	}
	digest, err := sourceDigest(dir, volsyncReadPackages)
	if err != nil {
		t.Fatalf("a package the rsync mover's container is built in is not where it was at %s, so the capabilities the kind states cannot be held to it: %v", volsyncOperatorVersion, err)
	}
	if digest != volsyncReadDigest {
		t.Fatalf("the source of %v in the linked module is not what was read for volsyncRsyncCapabilities: its digest is %s, and volsyncReadDigest says %s. Read the rsync mover again (the procedure is in this test's comment) before restating the digest", volsyncReadPackages, digest, volsyncReadDigest)
	}

	src, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(volsyncMoverSources+"rsync/mover.go")))
	if err != nil {
		t.Fatalf("the rsync mover's source is not where it was at %s: %v", volsyncOperatorVersion, err)
	}
	got, err := readMoverContainer(src)
	if err != nil {
		t.Fatalf("the rsync mover's source is not of the form the capabilities are read from: %v", err)
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

// testReadMoverContainerGuards shows on made-up sources that the reading
// reads the list a source holds and refuses a source it cannot answer from.
func testReadMoverContainerGuards(t *testing.T) {
	t.Helper()
	const container = `package rsync
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
}
`
	source := func(added, more string) string {
		return strings.NewReplacer("ADDED", added, "MORE", more).Replace(container)
	}
	edited := func(old, replacement string) string {
		return strings.Replace(source(`"CHOWN"`, ""), old, replacement, 1)
	}
	if got, err := readMoverContainer([]byte(source(`"NET_ADMIN", "CHOWN"`, ""))); err != nil || !slices.Equal(got.add, []string{"NET_ADMIN", "CHOWN"}) {
		t.Fatalf("the reading of a source that adds NET_ADMIN and CHOWN is %+v, %v", got, err)
	}
	for name, refusal := range map[string][2]string{
		"a second container":           {source(`"CHOWN"`, ", {}"), "want one list of one container"},
		"no container":                 {"package rsync\n", "want one list of one container"},
		"a capability that is a name":  {source(`capChown`, ""), "want a string literal"},
		"capabilities built by a call": {edited(`[]corev1.Capability{"CHOWN"}`, `capabilities("CHOWN")`), "want a []corev1.Capability literal"},
		"a setting written twice":      {edited("Privileged: ptr.To(false),", "Privileged: ptr.To(false), Privileged: ptr.To(true),"), "sets Privileged 2 times"},
		"a setting not written":        {edited("RunAsUser: ptr.To[int64](0),", ""), "sets RunAsUser 0 times"},
		"a setting outside the list":   {edited("RunAsUser: ptr.To[int64](0),", "") + "var x = T{RunAsUser: ptr.To[int64](0)}\n", "sets RunAsUser outside the container"},
		"init containers beside it":    {source(`"CHOWN"`, "") + "var y = T{InitContainers: nil}\n", "writes init containers"},
		"a source that does not parse": {"package", "parse"},
	} {
		got, err := readMoverContainer([]byte(refusal[0]))
		if err == nil || !strings.Contains(err.Error(), refusal[1]) {
			t.Fatalf("the reading of a source with %s answers %+v, %v: want an error that says %q", name, got, err, refusal[1])
		}
	}
}

// testSourceDigestGuards shows on made-up directories that the digest is the
// same for the same source, changes with any byte of it, with a file's name
// and with a file added, leaves tests and other files out, and is refused for
// a directory that is missing or holds no Go source.
func testSourceDigestGuards(t *testing.T) {
	t.Helper()
	write := func(files map[string]string) string {
		root := t.TempDir()
		for name, content := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	digest := func(files map[string]string) string {
		got, err := sourceDigest(write(files), []string{"a", "b"})
		if err != nil {
			t.Fatalf("the digest of %v: %v", files, err)
		}
		return got
	}
	base := map[string]string{"a/x.go": "package a\n", "a/y.go": "package a\nvar y int\n", "b/z.go": "package b\n"}
	with := func(name, content string) map[string]string {
		out := map[string]string{name: content}
		for file, text := range base {
			if _, ok := out[file]; !ok {
				out[file] = text
			}
		}
		return out
	}
	without := func(name string) map[string]string {
		out := map[string]string{}
		for file, text := range base {
			if file != name {
				out[file] = text
			}
		}
		return out
	}
	want := digest(base)
	for name, files := range map[string]map[string]string{
		"the same source again":          base,
		"a test beside it":               with("a/x_test.go", "package a\n"),
		"a file that is no Go beside it": with("b/notes.txt", "notes\n"),
		"a package below it":             with("a/sub/s.go", "package sub\n"),
	} {
		if got := digest(files); got != want {
			t.Fatalf("the digest of %s is %s, want the digest of the source alone, %s", name, got, want)
		}
	}
	renamed := without("a/y.go")
	renamed["a/w.go"] = base["a/y.go"]
	moved := without("a/y.go")
	moved["b/y.go"] = base["a/y.go"]
	// The two files of the first package, their bytes in the same order, cut
	// at another place.
	recut := with("a/x.go", "package a\npackage a\n")
	recut["a/y.go"] = "var y int\n"
	for name, files := range map[string]map[string]string{
		"one byte changed in the first package":  with("a/y.go", "package a\nvar y int8\n"),
		"one byte changed in the second package": with("b/z.go", "package b \n"),
		"a file added":                           with("b/more.go", "package b\n"),
		"a file removed":                         without("a/y.go"),
		"a file renamed":                         renamed,
		"a file moved to the other package":      moved,
		"bytes moved from one file to the next":  recut,
	} {
		if got := digest(files); got == want {
			t.Fatalf("the digest of the source with %s is the digest of the source without it", name)
		}
	}
	if got, err := sourceDigest(write(without("b/z.go")), []string{"a", "b"}); err == nil {
		t.Fatalf("the digest of a source whose second package is missing answers %s, want an error", got)
	}
	if got, err := sourceDigest(write(with("c/only_test.go", "package c\n")), []string{"a", "c"}); err == nil {
		t.Fatalf("the digest of a package that holds only tests answers %s, want an error", got)
	}
}
