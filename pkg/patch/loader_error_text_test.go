package patch

import (
	"bufio"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// loaderErrorRow names an input that reaches one error site of loader.go. A row holds the
// exact text of the error, or its prefix where the tail comes from a library (yaml.v3).
type loaderErrorRow struct {
	name   string
	call   func() error
	want   string
	prefix string
}

func loaderTestResource(kind, name, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{}}
	u.SetAPIVersion("v1")
	u.SetKind(kind)
	u.SetName(name)
	if namespace != "" {
		u.SetNamespace(namespace)
	}
	return u
}

func loadPatchText(in string) func() error {
	return func() error {
		_, err := LoadPatchFile(strings.NewReader(in))
		return err
	}
}

func resolvePatchSpec(resources []*unstructured.Unstructured, spec PatchSpec) func() error {
	return func() error {
		_, err := NewPatchableAppSet(resources, []PatchSpec{spec})
		return err
	}
}

// TestLoaderErrorText pins the text of the loader errors that are built with pkg/errors. The
// rows pass on the code that built them with fmt.Errorf as well: the change moves the call and
// keeps the text. A caller reads one of these texts ("not found in base resources").
//
// Ten wrapped sites have no row. Their cause never fails today: SubstituteVariables,
// substituteVariablesInValue, ParsePatchLine, ResolveTOMLPath and tomlValue return a nil error
// on every input, so no loader input reaches them.
func TestLoaderErrorText(t *testing.T) {
	long := strings.Repeat("x", 70000)
	app := loaderTestResource("Deployment", "app", "")
	svc := loaderTestResource("Service", "app", "")
	prodApp := loaderTestResource("Deployment", "web", "prod")
	stageApp := loaderTestResource("Deployment", "web", "stage")
	resources := []*unstructured.Unstructured{app, svc, prodApp, stageApp}

	rows := []loaderErrorRow{
		{name: "read content", call: func() error {
			_, err := LoadPatchFile(iotest.ErrReader(errors.New("boom")))
			return err
		}, want: "failed to read patch content: boom"},
		{name: "read input", call: loadPatchText(""), want: "failed to read patch input: EOF"},
		{name: "simple map decode", call: loadPatchText("a: 1\na: 2\n"), prefix: "invalid simple patch map: yaml: unmarshal errors:"},
		{name: "patch list decode", call: loadPatchText("- 1\n"), prefix: "invalid patch list: yaml: unmarshal errors:"},
		{name: "unknown patch type", call: loadPatchText("- target: x\n  type: foo\n  patch: {a: b}\n"),
			want: `unknown patch type "foo" for target "x": must be "" or "strategic"`},
		{name: "strategic without payload", call: loadPatchText("- target: x\n  type: strategic\n"),
			want: "strategic patch targeting 'x' has no patch payload"},
		{name: "yaml patch path", call: loadPatchText("- target: x\n  patch:\n    'a[': 1\n"),
			want: `invalid patch path syntax: a[: NormalizePath failed for a[: malformed selector in segment "a["`},
		{name: "unrecognized format", call: loadPatchText("hello\n"), want: "unrecognized patch format"},
		{name: "toml header", call: loadPatchText("[x]\n"),
			want: "invalid TOML header at line 1: TOML header must have at least kind.name: [x]"},
		{name: "toml value without header", call: loadPatchText("a = b\n"), want: "patch value without header at line 1: a = b"},
		{name: "toml line format", call: loadPatchText("[deployment.app]\nfoo\n"), want: "invalid patch line format at line 2: foo"},
		{name: "toml patch path", call: loadPatchText("[deployment.app]\na[: 1\n"),
			want: `invalid patch path syntax: a[: NormalizePath failed for a[: malformed selector in segment "a["`},
		{name: "toml scan", call: loadPatchText("[deployment.app]\na: " + long + "\n"),
			want: "error reading patch file: bufio.Scanner: token too long"},
		{name: "resource decode", call: func() error {
			_, err := LoadResourcesFromMultiYAML(strings.NewReader("a: [\n"))
			return err
		}, prefix: "failed to decode resource document: yaml: line 1:"},
		{name: "namespaced target not found", call: func() error {
			_, err := ResolveTargetKey(resources, "stage/deployment.none")
			return err
		}, want: `target "stage/deployment.none" not found in base resources`},
		{name: "kind name ambiguous", call: func() error {
			_, err := ResolveTargetKey(resources, "deployment.web")
			return err
		}, want: `target "deployment.web" is ambiguous, matches 2 resources; use namespace/kind.name to disambiguate`},
		{name: "short name not found", call: func() error {
			_, err := ResolveTargetKey(resources, "none")
			return err
		}, want: `target "none" not found in base resources`},
		{name: "short name ambiguous", call: func() error {
			_, err := ResolveTargetKey(resources, "app")
			return err
		}, want: `target "app" is ambiguous, matches: deployment.app, service.app; use kind.name format`},
		{name: "strategic without target", call: resolvePatchSpec(resources, PatchSpec{Strategic: &StrategicPatch{Patch: map[string]any{"a": "b"}}}),
			want: "strategic merge patch requires an explicit target"},
		{name: "strategic target not found", call: resolvePatchSpec(resources, PatchSpec{Target: "none", Strategic: &StrategicPatch{Patch: map[string]any{"a": "b"}}}),
			want: `strategic merge patch: target "none" not found in base resources`},
		{name: "resolve patch path", call: resolvePatchSpec(resources, PatchSpec{Patch: PatchOp{Op: "replace", Path: "a[", Value: 1}}),
			want: `invalid patch path syntax: a[: NormalizePath failed for a[: malformed selector in segment "a["`},
		{name: "explicit target not found", call: resolvePatchSpec(resources, PatchSpec{Target: "none", Patch: PatchOp{Op: "replace", Path: "a", Value: 1}}),
			want: "explicit target not found: none"},
		{name: "explicit target ambiguous", call: resolvePatchSpec(resources, PatchSpec{Target: "app", Patch: PatchOp{Op: "replace", Path: "a", Value: 1}}),
			want: `explicit target "app": target "app" is ambiguous, matches: deployment.app, service.app; use kind.name format`},
		{name: "no target for path", call: resolvePatchSpec(resources, PatchSpec{Patch: PatchOp{Op: "replace", Path: "nothing.here", Value: 1}}),
			want: "could not determine target resource for patch path: nothing.here"},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := row.call()
			if err == nil {
				t.Fatalf("no error, want %q%q", row.want, row.prefix)
			}
			got := err.Error()
			if row.prefix != "" {
				if !strings.HasPrefix(got, row.prefix) {
					t.Fatalf("error text %q does not start with %q", got, row.prefix)
				}
				return
			}
			if got != row.want {
				t.Fatalf("error text %q, want %q", got, row.want)
			}
		})
	}
}

// TestLoaderErrorChain pins that a wrapping site keeps its cause on the unwrap chain, for one
// site built with Wrap and one built with Wrapf.
func TestLoaderErrorChain(t *testing.T) {
	boom := errors.New("boom")
	if _, err := LoadPatchFile(iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Fatalf("Wrap site: the read error is not on the chain: %v", err)
	}

	_, err := LoadPatchFile(strings.NewReader("[deployment.app]\na: " + strings.Repeat("x", 70000) + "\n"))
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("Wrap site: the scanner error is not on the chain: %v", err)
	}

	_, err = LoadPatchFile(strings.NewReader("[deployment.app]\na[: 1\n"))
	inner := errors.Unwrap(err)
	if inner == nil {
		t.Fatalf("Wrapf site: no cause on the chain: %v", err)
	}
	if want := `NormalizePath failed for a[: malformed selector in segment "a["`; inner.Error() != want {
		t.Fatalf("Wrapf site: cause text %q, want %q", inner.Error(), want)
	}
}
