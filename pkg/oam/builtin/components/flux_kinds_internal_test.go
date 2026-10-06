package components

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// fluxMarkerModules are the modules whose Go source the tests below read: the
// API modules of the Flux controllers hold the Go types and ship no CRD, so
// the markers the CRDs are generated from are the source, as for the kinds of
// the Prometheus operator's API. Nothing here is held to the API server's own
// validator (crdCreate), which answers from a CRD.
var fluxMarkerModules = []string{
	"github.com/fluxcd/notification-controller/api",
	"github.com/fluxcd/image-reflector-controller/api",
	"github.com/fluxcd/image-automation-controller/api",
	"github.com/fluxcd/source-controller/api",
	"github.com/fluxcd/source-watcher/api/v2",
	"github.com/fluxcd/pkg/apis/meta",
}

// fluxKindRows lists the kind components of those APIs with the spec type each
// decodes into, its required list and its duration fields. validated names the
// required fields the list cannot hold, because a parent of theirs is written
// whether or not it was authored; the kind's validate refuses those.
var fluxKindRows = []struct {
	component string
	typ       reflect.Type
	required  map[string]string
	validated []string
	durations map[string]fluxduration.Form
}{
	{fluxcdAlertType, reflect.TypeFor[notificationv1beta3.AlertSpec](), fluxcdAlertKind.required, nil, durationForms(fluxcdAlertKind.durations)},
	{imagePolicyType, reflect.TypeFor[imagev1.ImagePolicySpec](), imagePolicyKind.required, nil, durationForms(imagePolicyKind.durations)},
	{imageUpdateAutomationType, reflect.TypeFor[autov1.ImageUpdateAutomationSpec](), imageUpdateAutomationKind.required, nil, durationForms(imageUpdateAutomationKind.durations)},
	{artifactGeneratorType, reflect.TypeFor[swv1beta1.ArtifactGeneratorSpec](), artifactGeneratorKind.required, nil, durationForms(artifactGeneratorKind.durations)},
}

// durationForms is a kind's duration fields by path, each with its form.
func durationForms[T any](fields []fluxDurationField[T]) map[string]fluxduration.Form {
	forms := map[string]fluxduration.Form{}
	for _, f := range fields {
		forms[f.name()] = f.form
	}
	return forms
}

// fluxNoCRD is why no kind of these APIs checks an expression rule
// (go-kure/launcher#874): a check is held to the API server's own validator,
// which answers from a CRD, and the linked modules ship none.
const fluxNoCRD = "the linked module ships no CRD, so a check could not be held to the API server's validator"

// fluxRulesLeft lists, per kind, every expression rule the API declares on
// what the kind decodes, as "<path>: <rule>", with what the kind leaves to the
// API server and why. A kind with no entry decodes a type that declares none.
// TestFluxKinds_ExpressionRules holds the table to the markers of the linked
// source, in both directions.
var fluxRulesLeft = map[string]map[string]string{
	imagePolicyType: {
		"spec: !has(self.interval) || (has(self.digestReflectionPolicy) && self.digestReflectionPolicy == 'Always')": "an `interval` without `digestReflectionPolicy: Always` builds and is refused at apply: " + fluxNoCRD,
		"spec: has(self.interval) || !has(self.digestReflectionPolicy) || self.digestReflectionPolicy != 'Always'":   "`digestReflectionPolicy: Always` without an `interval` builds and is refused at apply: " + fluxNoCRD,
	},
	artifactGeneratorType: {
		`spec: has(self.pathPattern) && size(self.pathPattern) > 0 || self.artifacts.all(a, a.name.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$'))`: "without a `pathPattern`, an artifact whose `name` is no Kubernetes object name builds and is refused at apply: " + fluxNoCRD,
	},
}

// walkFluxFields is walkKindFields for a type of a Flux API: a field is
// required where its source marks it so.
func walkFluxFields(typ reflect.Type, markers func(kindField) (fieldMarkers, bool), visit func(kindField)) {
	walkKindFields(typ, func(f kindField) bool {
		m, _ := markers(f)
		return m.required
	}, visit)
}

// fluxMarkersRead fails the test unless the markers of the Flux modules are
// being read: a field of a controller's API and one of the shared reference
// types are marked required.
func fluxMarkersRead(t *testing.T, markers func(kindField) (fieldMarkers, bool)) {
	t.Helper()
	for path, want := range map[string]bool{"providerRef": true, "providerRef.name": true, "eventSeverity": false} {
		found := false
		walkFluxFields(reflect.TypeFor[notificationv1beta3.AlertSpec](), markers, func(f kindField) {
			if f.path != path {
				return
			}
			found = true
			if m, read := markers(f); !read || m.required != want {
				t.Fatalf("AlertSpec %s is marked required: %v (read: %v), want %v; the source is not being read", path, m.required, read, want)
			}
		})
		if !found {
			t.Fatalf("the walk of AlertSpec did not reach %s", path)
		}
	}
}

// TestFluxKinds_RequiredMatchMarkers derives, from the linked modules' source,
// the fields of each kind that the API requires and the type would write
// unauthored, and holds the kind's required list to them, as
// TestMonitoringKinds_RequiredMatchMarkers does for the Prometheus operator's
// kinds. A dependency bump that adds, drops or moves one fails here, naming
// it.
//
// The source read is the Flux modules' (fluxMarkerModules): a kind's own
// package, the packages of its module it embeds, and the shared reference
// types. A field of a Kubernetes type these specs embed (the key and operator
// of a label selector requirement) is not derived, and no kind refuses its
// omission.
func TestFluxKinds_RequiredMatchMarkers(t *testing.T) {
	markers := linkedFieldMarkers(t, fluxMarkerModules)
	fluxMarkersRead(t, markers)
	for _, kind := range fluxKindRows {
		t.Run(kind.component, func(t *testing.T) {
			listed, validated := map[string]bool{}, map[string]bool{}
			fields := 0
			walkFluxFields(kind.typ, markers, func(f kindField) {
				m, read := markers(f)
				if !read {
					if f.owner.PkgPath() == kind.typ.PkgPath() {
						t.Errorf("%s (%s.%s) has no field in the module's source; the markers are keyed wrongly", f.path, f.owner, f.field.Name)
					}
					return
				}
				fields++
				if !f.writtenUnauthored() {
					return
				}
				if !m.required && !m.optional {
					t.Errorf("%s (%s.%s) is written unauthored and is marked neither required nor optional; classify it", f.path, f.owner, f.field.Name)
				}
				if !m.required {
					return
				}
				switch {
				case strings.Contains(f.path, "{}"):
					t.Errorf("%s is required under a map value, which a required list cannot name", f.path)
				case f.forced:
					validated[f.path] = true
				default:
					listed[f.path] = true
				}
			})
			if fields == 0 {
				t.Fatalf("the walk found no field of %s", kind.typ)
			}
			t.Logf("walked %d fields of the Flux modules; required and written unauthored: %v; of those under a parent written unauthored: %v",
				fields, slices.Sorted(maps.Keys(listed)), slices.Sorted(maps.Keys(validated)))
			if got, want := slices.Sorted(maps.Keys(kind.required)), slices.Sorted(maps.Keys(listed)); !slices.Equal(got, want) {
				t.Errorf("required list = %v\nthe source marks %v", got, want)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}
			if got, want := slices.Sorted(slices.Values(kind.validated)), slices.Sorted(maps.Keys(validated)); !slices.Equal(got, want) {
				t.Errorf("fields left to validate = %v\nthe source marks %v", got, want)
			}
		})
	}
}

// TestFluxKinds_NoDefaultedZeros is TestMonitoringKinds_NoDefaultedZeros for
// the kinds of the Flux APIs: no field these kinds decode may be a number or a
// boolean that is omitted when zero and that the API defaults to something
// else, since policyFreeKind.config carries no defaulted-zero list. The
// default is the field's marker, read from the linked modules' source, the
// Kubernetes types these specs embed included. A field of that shape on a type
// whose source is not read fails too.
func TestFluxKinds_NoDefaultedZeros(t *testing.T) {
	markers := linkedFieldMarkers(t, markerModules)
	fluxMarkersRead(t, markers)
	walked := map[string]bool{}
	for _, kind := range fluxKindRows {
		walkFluxFields(kind.typ, markers, func(f kindField) {
			if !f.omitemptyScalar() {
				return
			}
			at := kind.typ.Name() + ": " + f.path
			walked[at] = true
			m, read := markers(f)
			switch {
			case !read:
				t.Errorf("%s (%s.%s) is omitted when zero, and its default cannot be read: the source of its type is not", at, f.owner, f.field.Name)
			case m.hasDefault && !crdDefaultIsZero(m.def):
				t.Errorf("%s is omitted when zero and defaults to %s: an authored zero would be replaced; the kind needs a defaulted-zero list, which policyFreeKind does not carry", at, m.def)
			}
		})
	}
	for _, at := range []string{"AlertSpec: suspend", "ImagePolicySpec: suspend", "ImageUpdateAutomationSpec: suspend"} {
		if !walked[at] {
			t.Errorf("the walk did not reach %s; it found %v", at, slices.Sorted(maps.Keys(walked)))
		}
	}
}

// expressionRuleMarker is the marker the CRD generator turns into an
// expression rule (x-kubernetes-validations).
const expressionRuleMarker = "+kubebuilder:validation:XValidation:"

// packageMarkerLines reads the marker lines (those starting with +) of the
// comments of the Go package in dir: of each type, keyed by its name, and of
// each struct field, keyed "<type>.<Go field name>", an embedded field under
// its type's name. A type's markers are those of its doc comment and of the
// comment that ends one blank line above it, where the CRD generator reads
// them too. It fails when the package holds an expression rule marker that it
// attached to no type and no field: a rule the tests below would not see.
func packageMarkerLines(t *testing.T, dir string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the package in %s: %v", dir, err)
	}
	linesOf := func(groups ...*ast.CommentGroup) []string {
		var lines []string
		for _, group := range groups {
			if group == nil {
				continue
			}
			for _, comment := range group.List {
				if line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//")); strings.HasPrefix(line, "+") {
					lines = append(lines, line)
				}
			}
		}
		return lines
	}
	rules := func(lines []string) int {
		n := 0
		for _, line := range lines {
			if strings.HasPrefix(line, expressionRuleMarker) {
				n++
			}
		}
		return n
	}
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		inFile, attached := rules(linesOf(file.Comments...)), 0
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typ := spec.(*ast.TypeSpec)
				doc, start := typ.Doc, typ.Pos()
				if doc == nil && !gen.Lparen.IsValid() {
					doc, start = gen.Doc, gen.Pos()
				}
				if doc != nil {
					start = doc.Pos()
				}
				var above *ast.CommentGroup
				for _, group := range file.Comments {
					if group != doc && fset.Position(group.End()).Line == fset.Position(start).Line-2 {
						above = group
					}
				}
				out[typ.Name.Name] = linesOf(above, doc)
				attached += rules(out[typ.Name.Name])
				st, ok := typ.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					lines := linesOf(field.Doc)
					attached += rules(lines)
					names := make([]string, 0, len(field.Names))
					for _, ident := range field.Names {
						names = append(names, ident.Name)
					}
					if len(names) == 0 {
						names = append(names, embeddedTypeName(field.Type))
					}
					for _, fieldName := range names {
						out[typ.Name.Name+"."+fieldName] = lines
					}
				}
			}
		}
		if attached != inFile {
			t.Fatalf("%s holds %d expression rule markers and %d are attached to a type or a field: a rule is not read", filepath.Join(dir, name), inFile, attached)
		}
	}
	return out
}

// linkedMarkerLines returns the marker lines of a type (key "<type>") or of a
// struct field (key "<type>.<Go field name>") of the package at pkgPath, read
// from the source of the linked modules, and whether the package is one of
// theirs.
func linkedMarkerLines(t *testing.T, modules []string) func(pkgPath, key string) ([]string, bool) {
	t.Helper()
	packages := map[string]map[string][]string{}
	return func(pkgPath, key string) ([]string, bool) {
		lines, loaded := packages[pkgPath]
		if !loaded {
			for _, module := range modules {
				if pkgPath == module || strings.HasPrefix(pkgPath, module+"/") {
					lines = packageMarkerLines(t, filepath.Join(linkedModuleDir(t, module), filepath.FromSlash(strings.TrimPrefix(pkgPath, module))))
					break
				}
			}
			packages[pkgPath] = lines
		}
		return lines[key], lines != nil
	}
}

// markerString is a marker's string argument: `name="text"`, as a Go string
// literal, or name=`text`.
func markerString(t *testing.T, line, name string) (string, bool) {
	t.Helper()
	quoted := regexp.MustCompile(name + `=("(?:[^"\\]|\\.)*"|` + "`[^`]*`)").FindStringSubmatch(line)
	if quoted == nil {
		return "", false
	}
	text, err := strconv.Unquote(quoted[1])
	if err != nil {
		t.Fatalf("the marker %s holds a %s that is no string: %v", line, name, err)
	}
	return text, true
}

// fluxExpressionRules returns the expression rules the API declares on what
// the encoding of typ reaches, as "<path>: <rule>" with the spec at "spec": a
// rule of a type at every path a value of the type sits at, a rule of a field
// at the field. A type whose source is not read fails the test.
func fluxExpressionRules(t *testing.T, typ reflect.Type, lines func(pkgPath, key string) ([]string, bool)) []string {
	t.Helper()
	found := map[string]bool{}
	add := func(path string, of reflect.Type, key string) {
		if of.PkgPath() == "" || of.Name() == "" {
			return
		}
		markers, read := lines(of.PkgPath(), key)
		if !read {
			t.Errorf("%s: the source of %s is not read, so its expression rules are not known", path, of)
			return
		}
		for _, line := range markers {
			if !strings.HasPrefix(line, expressionRuleMarker) {
				continue
			}
			rule, ok := markerString(t, line, "rule")
			if !ok {
				t.Fatalf("%s: the marker %s holds no rule", path, line)
			}
			found[path+": "+rule] = true
		}
	}
	add("spec", typ, typ.Name())
	walkKindFields(typ, func(kindField) bool { return false }, func(f kindField) {
		path, parent := "spec."+f.path, "spec"
		if i := strings.LastIndex(f.path, "."); i >= 0 {
			parent = "spec." + f.path[:i]
		}
		// The field's owner sits at the parent: the spec itself, a struct below
		// it, or a type one of them embeds.
		add(parent, f.owner, f.owner.Name())
		add(path, f.owner, f.owner.Name()+"."+f.field.Name)
		for child := f.field.Type; ; {
			add(path, child, child.Name())
			switch child.Kind() {
			case reflect.Pointer:
				child = child.Elem()
			case reflect.Slice, reflect.Array:
				child, path = child.Elem(), path+"[]"
			case reflect.Map:
				child, path = child.Elem(), path+"{}"
			default:
				return
			}
		}
	})
	return slices.Sorted(maps.Keys(found))
}

// TestFluxKinds_ExpressionRules reads, from the markers of the linked source,
// every expression rule the API declares on what each kind decodes, and holds
// fluxRulesLeft to them: a rule the table does not hold, one it holds that the
// source no longer declares in those words, and one with no reason each fail,
// so a dependency bump that adds or rewords a rule fails here until it is
// classified.
//
// No rule is checked by a kind, and none is shown against the API server's
// validator: that takes a CRD (crdCreate, go-kure/launcher#874), and these
// modules ship none. The table is what the README's "not checked" lists are
// held to say.
func TestFluxKinds_ExpressionRules(t *testing.T) {
	lines := linkedMarkerLines(t, markerModules)
	total := 0
	for _, kind := range fluxKindRows {
		t.Run(kind.component, func(t *testing.T) {
			declared := fluxExpressionRules(t, kind.typ, lines)
			total += len(declared)
			left := fluxRulesLeft[kind.component]
			for _, rule := range declared {
				if strings.TrimSpace(left[rule]) == "" {
					t.Errorf("the API declares the rule\n\t%s\nand fluxRulesLeft does not say why the kind leaves it", rule)
				}
			}
			for rule := range left {
				if !slices.Contains(declared, rule) {
					t.Errorf("fluxRulesLeft holds the rule\n\t%s\nwhich the source does not declare; it declares %q", rule, declared)
				}
			}
		})
	}
	for component := range fluxRulesLeft {
		if !slices.ContainsFunc(fluxKindRows, func(row struct {
			component string
			typ       reflect.Type
			required  map[string]string
			validated []string
			durations map[string]fluxduration.Form
		}) bool {
			return row.component == component
		}) {
			t.Errorf("fluxRulesLeft holds rules of %s, which is no row of fluxKindRows", component)
		}
	}
	// Vacuity guard: the two rules of an ImagePolicy are in the source read.
	if total < 2 {
		t.Fatalf("the source declares %d expression rules on the Flux kinds; the markers are not being read", total)
	}
}

// fluxDurationPatterns are the patterns the Flux APIs declare on a duration
// field, each with the form that holds a value to it.
var fluxDurationPatterns = map[string]fluxduration.Form{
	`^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$`: fluxduration.Interval,
	`^([0-9]+(\.[0-9]+)?(ms|s|m))+$`:   fluxduration.SourceTimeout,
}

// TestFluxKinds_DurationsMatchMarkers holds each kind's duration list to its
// type: every duration the encoding reaches is listed, by its json path, with
// the form of the pattern its marker declares, and nothing else is. A duration
// under a list or a map, which the list cannot name, and one whose pattern is
// neither form's fail, so a dependency bump that adds a duration field fails
// here until the kind checks it.
func TestFluxKinds_DurationsMatchMarkers(t *testing.T) {
	// The two patterns are told apart by the hour unit, as the forms are.
	if fluxduration.Interval.Validate("1h") != nil || fluxduration.SourceTimeout.Validate("1h") == nil {
		t.Fatal("the two duration forms no longer differ by the hour unit; fluxDurationPatterns is wrong")
	}
	lines := linkedMarkerLines(t, markerModules)
	duration := reflect.TypeFor[metav1.Duration]()
	reached := 0
	for _, kind := range fluxKindRows {
		t.Run(kind.component, func(t *testing.T) {
			want := map[string]fluxduration.Form{}
			walkKindFields(kind.typ, func(kindField) bool { return false }, func(f kindField) {
				if f.field.Type != duration && f.field.Type != reflect.PointerTo(duration) {
					return
				}
				reached++
				if strings.ContainsAny(f.path, "[{") {
					t.Errorf("%s is a duration under a list or a map, which the kind's duration list cannot name", f.path)
					return
				}
				markers, read := lines(f.owner.PkgPath(), f.owner.Name()+"."+f.field.Name)
				if !read {
					t.Errorf("%s: the source of %s is not read, so its pattern is not known", f.path, f.owner)
					return
				}
				for _, line := range markers {
					if !strings.HasPrefix(line, "+kubebuilder:validation:Pattern=") {
						continue
					}
					pattern, _ := markerString(t, line, "Pattern")
					form, known := fluxDurationPatterns[pattern]
					if !known {
						t.Errorf("%s declares the pattern %q, which is neither duration form's", f.path, pattern)
						return
					}
					want[f.path] = form
					return
				}
				t.Errorf("%s is a duration with no pattern marker; say what holds it", f.path)
			})
			if !reflect.DeepEqual(kind.durations, want) {
				t.Errorf("duration list = %v\nthe type holds %v", slices.Sorted(maps.Keys(kind.durations)), slices.Sorted(maps.Keys(want)))
			}
		})
	}
	// Vacuity guard: an ImagePolicy's interval is a duration.
	if reached == 0 {
		t.Fatal("the walk reached no duration field of a Flux kind")
	}
}
