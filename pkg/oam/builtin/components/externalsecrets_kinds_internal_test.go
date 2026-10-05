package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// externalSecretsModulePath is the module whose Go source the tests below
// read: the External Secrets Operator's API types publish no field
// descriptions and the module ships no CRD, so the markers its CRDs are
// generated from are the source.
const externalSecretsModulePath = "github.com/external-secrets/external-secrets/apis"

// externalSecretsPackages are the packages of that module the kinds' spec
// types are declared in, by their path under the module.
var externalSecretsPackages = []string{"externalsecrets/v1", "meta/v1"}

// externalSecretsKinds lists the kind components of that API with the spec
// type each decodes into and its required list.
var externalSecretsKinds = []struct {
	component string
	typ       reflect.Type
	required  map[string]string
	// defaulted is the kind's defaulted-zero list (policyFreeKind.defaultedZeros).
	defaulted map[string]string
}{
	{"secretstore", reflect.TypeFor[esv1.SecretStoreSpec](), secretStoreKind.required, secretStoreKind.defaultedZeros.fields},
	{"clustersecretstore", reflect.TypeFor[esv1.SecretStoreSpec](), clusterSecretStoreKind.required, clusterSecretStoreKind.defaultedZeros.fields},
	{"externalsecret", reflect.TypeFor[esv1.ExternalSecretSpec](), externalSecretKind.required, externalSecretKind.defaultedZeros.fields},
	{"clusterexternalsecret", reflect.TypeFor[esv1.ClusterExternalSecretSpec](), clusterExternalSecretKind.required, clusterExternalSecretKind.defaultedZeros.fields},
}

// externalSecretsSource is what the tests read from the module's source.
type externalSecretsSource struct {
	// fields holds the markers of every struct field, keyed by
	// "<package path>.<type>.<Go field name>".
	fields map[string]fieldMarkers
	// rules holds every rule marker over more than one field, as
	// "<type> <marker>" for one on a type and "<type>.<Go field name> <marker>"
	// for one on a field, the marker without its "+kubebuilder:validation:"
	// prefix. Only the externalsecrets/v1 package declares any.
	rules []string
}

// externalSecretsRuleMarkers are the markers read as a rule over more than one
// field: an expression rule, and a bound on the number of properties.
var externalSecretsRuleMarkers = []string{"XValidation", "MinProperties", "MaxProperties"}

// readExternalSecretsSource reads the linked module's source.
func readExternalSecretsSource(t *testing.T) externalSecretsSource {
	t.Helper()
	root := linkedModuleDir(t, externalSecretsModulePath)
	src := externalSecretsSource{fields: map[string]fieldMarkers{}}
	ruleOf := func(doc *ast.CommentGroup) []string {
		var out []string
		if doc == nil {
			return nil
		}
		for _, comment := range doc.List {
			line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			marker, ok := strings.CutPrefix(line, "+kubebuilder:validation:")
			if ok && slices.ContainsFunc(externalSecretsRuleMarkers, func(m string) bool { return strings.HasPrefix(marker, m) }) {
				out = append(out, marker)
			}
		}
		return out
	}
	for _, pkg := range externalSecretsPackages {
		dir := filepath.Join(root, filepath.FromSlash(pkg))
		// A package-level optional marker would turn every unmarked field
		// optional, and what this file reads as required with it.
		doc, err := os.ReadFile(filepath.Join(dir, "doc.go"))
		if err != nil {
			t.Fatalf("read the package comment of %s: %v", pkg, err)
		}
		if bytes.Contains(doc, []byte("+kubebuilder:validation:Optional")) {
			t.Fatalf("%s marks its fields optional by default; what these tests read as required no longer holds", pkg)
		}
		sourceStructs(t, dir, func(typ *ast.TypeSpec, doc *ast.CommentGroup, st *ast.StructType) {
			for _, rule := range ruleOf(doc) {
				src.rules = append(src.rules, typ.Name.Name+" "+rule)
			}
			for _, field := range st.Fields.List {
				markers := markersOf(field.Doc)
				for _, name := range sourceFieldNames(field) {
					src.fields[externalSecretsModulePath+"/"+pkg+"."+typ.Name.Name+"."+name] = markers
					for _, rule := range ruleOf(field.Doc) {
						src.rules = append(src.rules, typ.Name.Name+"."+name+" "+rule)
					}
				}
			}
		})
	}
	slices.Sort(src.rules)
	return src
}

// markers returns the markers of one field the walk reached, and whether its
// source was read: only the module's is.
func (s externalSecretsSource) markers(f kindField) (fieldMarkers, bool) {
	m, ok := s.fields[f.owner.PkgPath()+"."+f.owner.Name()+"."+f.field.Name]
	return m, ok
}

// required says the API requires the field, as the CRD generator reads the
// source of a package with no package-level optional marker: the field is
// marked +required or +kubebuilder:validation:Required, or its json tag has
// no omitempty and it is not marked +optional or
// +kubebuilder:validation:Optional.
func (s externalSecretsSource) required(f kindField) bool {
	m, ok := s.markers(f)
	if !ok {
		return false
	}
	return m.required || (!slices.Contains(f.jsonOptions(), "omitempty") && !m.optional)
}

// derive returns the fields of typ an author must write, by json path. Of
// those the API requires: written, the ones the type encodes whether or not
// they were authored, which a required list holds; and omitted, the ones it
// drops when unauthored, which the decoded value shows and a kind's validate
// holds. And defaulted, the optional ones the type encodes whether or not they
// were authored and to which the CRD gives a default, each with that default:
// the default is applied to an absent field only, so the empty value the
// object carries stands, and a required list holds these too
// (unappliedDefault). It fails the test on a field it cannot place: one whose
// source was not read, one marked both required and optional, one required
// under a map value, and one required or defaulted under a parent that is
// written unauthored without being required itself.
func (s externalSecretsSource) derive(t *testing.T, typ reflect.Type) (written, omitted []string, defaulted map[string]string) {
	t.Helper()
	fields := 0
	defaulted = map[string]string{}
	walkKindFields(typ, s.required, func(f kindField) {
		if !strings.HasPrefix(f.owner.PkgPath(), externalSecretsModulePath+"/") {
			return
		}
		fields++
		m, read := s.markers(f)
		switch {
		case !read:
			t.Errorf("%s (%s.%s) has no field in the module's source; the markers are keyed wrongly", f.path, f.owner, f.field.Name)
			return
		case m.required && m.optional:
			t.Errorf("%s (%s.%s) is marked both required and optional", f.path, f.owner, f.field.Name)
			return
		case !s.required(f):
			def, unapplied := s.unappliedDefault(t, f, m)
			switch {
			case !unapplied:
			case f.forced || strings.Contains(f.path, "{}"):
				t.Errorf("%s is written unauthored with the default %s under a map value or a parent the type writes unauthored; a required list cannot hold it, and this test knows no such field", f.path, def)
			default:
				defaulted[f.path] = def
			}
			return
		case strings.Contains(f.path, "{}"):
			t.Errorf("%s is required under a map value, which a required list cannot name", f.path)
		case !f.writtenUnauthored():
			omitted = append(omitted, f.path)
		case f.forced:
			t.Errorf("%s is required under a parent the type writes unauthored and the API does not require; the kind's validate must refuse it, and this test knows no such field", f.path)
		default:
			written = append(written, f.path)
		}
	})
	if fields == 0 {
		t.Fatalf("the walk found no field of %s", typ)
	}
	slices.Sort(written)
	slices.Sort(omitted)
	return written, omitted, defaulted
}

// unappliedDefault returns the CRD default of an optional field that the API
// server never applies to an object these kinds build, and whether f is such a
// field: the type encodes it whether or not it was authored, and its default
// is not the empty value the type writes.
//
// A field that encodes as an object is not one: its default is an object of
// defaults, and the test holds each of them to be the default of a field of
// that object which the type omits when unset, so that the API server fills
// it into the empty object the type writes.
func (s externalSecretsSource) unappliedDefault(t *testing.T, f kindField, m fieldMarkers) (string, bool) {
	t.Helper()
	if !m.hasDefault || !f.writtenUnauthored() {
		return "", false
	}
	typ := f.field.Type
	if typ.Kind() == reflect.Struct && !typ.Implements(reflect.TypeFor[json.Marshaler]()) {
		inner, ok := strings.CutPrefix(m.def, "{")
		inner, closed := strings.CutSuffix(inner, "}")
		if !ok || !closed {
			t.Errorf("%s is an object with the default %s, which is no object of defaults", f.path, m.def)
			return "", false
		}
		for _, entry := range strings.Split(inner, ",") {
			name, def, _ := strings.Cut(entry, ":")
			found := false
			for i := range typ.NumField() {
				child := kindField{owner: typ, field: typ.Field(i)}
				if json, _, _ := strings.Cut(child.field.Tag.Get("json"), ","); json != name {
					continue
				}
				found = true
				if cm, read := s.markers(child); !read || cm.def != def || child.writtenUnauthored() {
					t.Errorf("%s defaults its %s to %s, and that field's own default is %q (source read: %v, written unauthored: %v): the default would not reach the empty object the type writes",
						f.path, name, def, cm.def, read, child.writtenUnauthored())
				}
			}
			if !found {
				t.Errorf("%s defaults a %s, which is no field of %s", f.path, name, typ)
			}
		}
		return "", false
	}
	if typ.Kind() == reflect.Pointer {
		// A pointer written unauthored is written null, which the API server
		// reads as absent before it applies a default.
		t.Errorf("%s is a pointer the type writes unauthored, with the default %s; this test knows no such field", f.path, m.def)
		return "", false
	}
	if m.def == "" || crdDefaultIsZero(m.def) {
		return "", false
	}
	return m.def, true
}

// pathsDiffer returns the paths of want that got lacks, and those of got that
// want does not hold.
func pathsDiffer(got, want []string) (missing, extra []string) {
	for _, path := range want {
		if !slices.Contains(got, path) {
			missing = append(missing, path)
		}
	}
	for _, path := range got {
		if !slices.Contains(want, path) {
			extra = append(extra, path)
		}
	}
	return missing, extra
}

// TestExternalSecretsKinds_RequiredMatchSource derives, from the linked
// module's source, the fields of each kind that the API requires, and holds
// the kind to them.
//
// What is read as required is what the CRD generator makes required
// (externalSecretsSource.required). A required field the type writes whether
// or not it was authored must be in the kind's required list, and the list
// may hold nothing else: a dependency bump that adds, drops or moves one fails
// here, naming it. A required field the type omits when unauthored is not the
// list's: the decoded value shows the omission, and the kind's validate must
// refuse it. At this pin the API has none on these types, and the test fails
// on the first, naming it.
//
// The list holds one more class: an optional field the type writes whether or
// not it was authored, to which the CRD gives a default
// (externalSecretsSource.unappliedDefault). The API server applies a default
// to an absent field only, so the empty value the object carries would stand
// and be refused or misread; the kind refuses the field unauthored, with a
// sentence that names the default. Each such field must be in the list with
// that sentence, and the list may hold no other under it.
//
// Only the module's own types are derived. A field of a Kubernetes type these
// specs embed (the key and operator of a label selector requirement) is not,
// and no kind refuses its omission.
func TestExternalSecretsKinds_RequiredMatchSource(t *testing.T) {
	src := readExternalSecretsSource(t)
	// Vacuity guards: both spellings of a required field are read.
	if m := src.fields[externalSecretsModulePath+"/externalsecrets/v1.ManifestReference.Kind"]; !m.required {
		t.Fatal("ManifestReference.Kind is not read as marked required; the source is not being read")
	}
	if m := src.fields[externalSecretsModulePath+"/externalsecrets/v1.SecretStoreSpec.Controller"]; !m.optional {
		t.Fatal("SecretStoreSpec.Controller is not read as marked optional; the source is not being read")
	}
	for _, kind := range externalSecretsKinds {
		t.Run(kind.component, func(t *testing.T) {
			written, omitted, defaulted := src.derive(t, kind.typ)
			for _, path := range written {
				if _, both := defaulted[path]; both {
					t.Errorf("%s is derived both as required and as optional with a default", path)
				}
			}
			listed := slices.Sorted(maps.Keys(kind.required))
			missing, extra := pathsDiffer(listed, slices.Concat(written, slices.Sorted(maps.Keys(defaulted))))
			for _, path := range missing {
				t.Errorf("%s is written unauthored, and required by the API or given a default by it, and the required list does not hold it", path)
			}
			for _, path := range extra {
				t.Errorf("%s is in the required list, and the source makes it neither a required field the type writes unauthored nor one it writes unauthored with a default", path)
			}
			for _, path := range omitted {
				t.Errorf("%s is required by the API and omitted when unauthored; the kind's validate must refuse a decoded value without it", path)
			}
			for path, says := range kind.required {
				def, unapplied := defaulted[path]
				switch {
				case strings.TrimSpace(says) == "":
					t.Errorf("required field %s says nothing of itself", path)
				case unapplied && says != fmt.Sprintf(externalSecretsUnappliedDefault, def):
					t.Errorf("%s is written unauthored with the default %s, and the list says %q of it", path, def, says)
				case !unapplied && strings.Contains(says, "default"):
					t.Errorf("%s has no default the source gives it, and the list says %q of it", path, says)
				}
			}
			t.Logf("%d required fields written unauthored, %d omitted when unauthored, %d optional fields written unauthored with a default", len(written), len(omitted), len(defaulted))
		})
	}
}

// The generated file of the stores' required paths, and the switch under
// which TestExternalSecretsKinds_StoreRequiredIsGenerated writes it.
const (
	secretStoreRequiredFile   = "zz_generated_externalsecrets_required.go"
	secretStoreRequiredUpdate = "UPDATE_EXTERNALSECRETS_REQUIRED"
)

// renderSecretStoreRequired returns the generated file for the given paths of
// the two classes: those the API requires, and those it gives a default.
func renderSecretStoreRequired(t *testing.T, paths []string, defaults map[string]string) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString("// Code generated by TestExternalSecretsKinds_StoreRequiredIsGenerated with " + secretStoreRequiredUpdate + "=1. DO NOT EDIT.\n\n")
	b.WriteString("package components\n\n")
	b.WriteString("// secretStoreRequiredPaths lists the fields of an external-secrets.io/v1\n")
	b.WriteString("// SecretStoreSpec that the API requires and the Go type writes whether or not\n")
	b.WriteString("// they were authored: see secretStoreRequired.\n")
	b.WriteString("var secretStoreRequiredPaths = []string{\n")
	for _, path := range paths {
		b.WriteString("\t" + `"` + path + `"` + ",\n")
	}
	b.WriteString("}\n\n")
	b.WriteString("// secretStoreUnappliedDefaults lists the optional fields of an\n")
	b.WriteString("// external-secrets.io/v1 SecretStoreSpec that the Go type writes whether or\n")
	b.WriteString("// not they were authored and to which the API gives a default, each with that\n")
	b.WriteString("// default: see secretStoreRequired.\n")
	b.WriteString("var secretStoreUnappliedDefaults = map[string]string{\n")
	for _, path := range slices.Sorted(maps.Keys(defaults)) {
		b.WriteString("\t" + strconv.Quote(path) + ": " + strconv.Quote(defaults[path]) + ",\n")
	}
	b.WriteString("}\n")
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		t.Fatalf("format the generated file: %v", err)
	}
	return out
}

// TestExternalSecretsKinds_StoreRequiredIsGenerated holds the two generated
// lists of the stores' paths, those the API requires and those it gives a
// default, each to its derivation from the linked module's source, in both
// directions and path by path (the second with each default), and the
// checked-in file to the bytes the derivation writes. After a bump of the
// module that changes either list, run this test once with
// UPDATE_EXTERNALSECRETS_REQUIRED=1: it writes the file and compares nothing.
func TestExternalSecretsKinds_StoreRequiredIsGenerated(t *testing.T) {
	derived, _, defaults := readExternalSecretsSource(t).derive(t, reflect.TypeFor[esv1.SecretStoreSpec]())
	// Vacuity guards: the derivation reaches a provider's own fields, in each
	// class.
	for _, path := range []string{"provider", "provider.vault.server", "provider.aws.region", "provider.vault.auth.kubernetes.serviceAccountRef.name"} {
		if !slices.Contains(derived, path) {
			t.Fatalf("the derivation does not hold %s; it found %d paths", path, len(derived))
		}
	}
	if def := defaults["provider.vault.version"]; def != "v2" {
		t.Fatalf("the derivation gives provider.vault.version the default %q, want v2; it found %d defaults", def, len(defaults))
	}
	want := renderSecretStoreRequired(t, derived, defaults)
	if os.Getenv(secretStoreRequiredUpdate) == "1" {
		if err := os.WriteFile(secretStoreRequiredFile, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", secretStoreRequiredFile, err)
		}
		t.Logf("wrote %s: %d required paths, %d defaults", secretStoreRequiredFile, len(derived), len(defaults))
		return
	}
	missing, extra := pathsDiffer(secretStoreRequiredPaths, derived)
	for _, path := range missing {
		t.Errorf("%s is required by the API and written unauthored, and %s does not list it; regenerate with %s=1", path, secretStoreRequiredFile, secretStoreRequiredUpdate)
	}
	for _, path := range extra {
		t.Errorf("%s is listed in %s, and the source does not make it a required field the type writes unauthored; regenerate with %s=1", path, secretStoreRequiredFile, secretStoreRequiredUpdate)
	}
	for _, diff := range defaultsDiffer(secretStoreUnappliedDefaults, defaults) {
		t.Errorf("%s; regenerate %s with %s=1", diff, secretStoreRequiredFile, secretStoreRequiredUpdate)
	}
	got, err := os.ReadFile(secretStoreRequiredFile)
	if err != nil {
		t.Fatalf("read %s: %v", secretStoreRequiredFile, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the derivation writes (%d required paths, %d defaults); regenerate with %s=1", secretStoreRequiredFile, len(derived), len(defaults), secretStoreRequiredUpdate)
	}
	t.Logf("%d required paths, %d defaults", len(derived), len(defaults))
}

// defaultsDiffer says, path by path and sorted, how the generated defaults
// differ from the derived ones: a path one side lacks, and a default that is
// not the source's.
func defaultsDiffer(got, want map[string]string) []string {
	var out []string
	for path, def := range want {
		switch listed, ok := got[path]; {
		case !ok:
			out = append(out, path+" is written unauthored with the default "+def+", and the generated defaults do not list it")
		case listed != def:
			out = append(out, path+" is listed with the default "+listed+", and the source gives it "+def)
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			out = append(out, path+" is listed in the generated defaults, and the source does not make it an optional field the type writes unauthored with a default")
		}
	}
	slices.Sort(out)
	return out
}

// TestExternalSecretsKinds_GeneratedComparisonBites: the comparison
// TestExternalSecretsKinds_StoreRequiredIsGenerated makes finds a path taken
// out of the list and one put into it, by name, and the file such a list
// would be is not the checked-in one.
func TestExternalSecretsKinds_GeneratedComparisonBites(t *testing.T) {
	const dropped, added = "provider.vault.server", "provider.vault.nothing"
	if !slices.Contains(secretStoreRequiredPaths, dropped) {
		t.Fatalf("the generated list does not hold %s", dropped)
	}
	shorter := slices.DeleteFunc(slices.Clone(secretStoreRequiredPaths), func(path string) bool { return path == dropped })
	if missing, extra := pathsDiffer(shorter, secretStoreRequiredPaths); !slices.Equal(missing, []string{dropped}) || extra != nil {
		t.Errorf("a list without %s: missing %v, extra %v; want that one path missing", dropped, missing, extra)
	}
	longer := append(slices.Clone(secretStoreRequiredPaths), added)
	if missing, extra := pathsDiffer(longer, secretStoreRequiredPaths); missing != nil || !slices.Equal(extra, []string{added}) {
		t.Errorf("a list with %s: missing %v, extra %v; want that one path extra", added, missing, extra)
	}
	file, err := os.ReadFile(secretStoreRequiredFile)
	if err != nil {
		t.Fatalf("read %s: %v", secretStoreRequiredFile, err)
	}
	if bytes.Equal(renderSecretStoreRequired(t, shorter, secretStoreUnappliedDefaults), file) {
		t.Errorf("the file of a list without %s equals the checked-in one", dropped)
	}

	// The same of the defaults: one taken out, one put in, one changed.
	const undefaulted, defaultless = "provider.vault.version", "provider.vault.nothing"
	if secretStoreUnappliedDefaults[undefaulted] != "v2" {
		t.Fatalf("the generated defaults give %s %q, want v2", undefaulted, secretStoreUnappliedDefaults[undefaulted])
	}
	fewer := maps.Clone(secretStoreUnappliedDefaults)
	delete(fewer, undefaulted)
	more := maps.Clone(secretStoreUnappliedDefaults)
	more[defaultless] = "none"
	other := maps.Clone(secretStoreUnappliedDefaults)
	other[undefaulted] = "v1"
	for name, c := range map[string]struct {
		defaults map[string]string
		want     string
	}{
		"one taken out": {fewer, undefaulted + " is written unauthored with the default v2, and the generated defaults do not list it"},
		"one put in":    {more, defaultless + " is listed in the generated defaults, and the source does not make it an optional field the type writes unauthored with a default"},
		"one changed":   {other, undefaulted + " is listed with the default v1, and the source gives it v2"},
	} {
		if diff := defaultsDiffer(c.defaults, secretStoreUnappliedDefaults); !slices.Equal(diff, []string{c.want}) {
			t.Errorf("defaults with %s: %q, want only %q", name, diff, c.want)
		}
		if bytes.Equal(renderSecretStoreRequired(t, secretStoreRequiredPaths, c.defaults), file) {
			t.Errorf("the file of defaults with %s equals the checked-in one", name)
		}
	}

	// What a refusal says of a generated path is one of three sentences, by
	// the class of the path and not by the field.
	for path, says := range secretStoreRequired {
		want := externalSecretsRequiresField
		if strings.HasSuffix(path, ".name") {
			want = externalSecretsRequiresName
		}
		if def, ok := secretStoreUnappliedDefaults[path]; ok {
			want = fmt.Sprintf(externalSecretsUnappliedDefault, def)
		}
		if says != want {
			t.Errorf("%s says %q, want %q", path, says, want)
		}
	}
	if len(secretStoreRequired) != len(secretStoreRequiredPaths)+len(secretStoreUnappliedDefaults) {
		t.Errorf("the required list holds %d paths, the generated ones %d and %d", len(secretStoreRequired), len(secretStoreRequiredPaths), len(secretStoreUnappliedDefaults))
	}
}

// What a kind does about one rule of the API over more than one field.
const (
	// ruleChecked: a kind's validate holds it.
	ruleChecked = "checked"
	// ruleLeft: it is left to the API server, which holds it at admission.
	ruleLeft = "left to the API server"
)

// externalSecretsRules classifies every rule the module's source declares over
// more than one field of a type these kinds decode: the expression rules
// (XValidation) and the bounds on a number of properties (MinProperties,
// MaxProperties), keyed as externalSecretsSource.rules lists them. The kinds
// hold two: that a store configures exactly one provider
// (validateSecretStore), and that a `data` entry's source reference holds no
// more than one property, which a generator reference beside the store
// reference the object always carries would break (refuseDataGeneratorRef);
// the lower bound on that reference is met by the same store reference and is
// left. A bump of the module that adds, drops or rewords a rule fails
// TestExternalSecretsKinds_Rules, naming its type or field.
var externalSecretsRules = map[string]string{
	"SecretStoreProvider MaxProperties=1": ruleChecked,
	"SecretStoreProvider MinProperties=1": ruleChecked,

	"AWSProvider.CustomSessionTags XValidation:rule=\"!('esoNamespace' in self) && !('esoStoreName' in self) && !('esoStoreKind' in self)\",message=\"customSessionTags cannot contain automatically injected reserved keys: esoNamespace, esoStoreName, esoStoreKind\"": ruleLeft,
	"AuthorizationProtocol MaxProperties=1": ruleLeft,
	"AuthorizationProtocol MinProperties=1": ruleLeft,
	"BarbicanAuth XValidation:rule=\"(has(self.authType) && self.authType == 'applicationCredential') || (!has(self.applicationCredentialID) && !has(self.applicationCredentialSecret))\",message=\"password auth should not include applicationCredential fields\"":          ruleLeft,
	"BarbicanAuth XValidation:rule=\"(has(self.authType) && self.authType == 'applicationCredential') || (has(self.username) && has(self.password))\",message=\"password auth requires both username and password\"":                                                          ruleLeft,
	"BarbicanAuth XValidation:rule=\"self.authType != 'applicationCredential' || (!has(self.username) && !has(self.password))\",message=\"applicationCredential auth should not include password fields\"":                                                                    ruleLeft,
	"BarbicanAuth XValidation:rule=\"self.authType != 'applicationCredential' || (has(self.applicationCredentialID) && has(self.applicationCredentialSecret))\",message=\"applicationCredential auth requires both applicationCredentialID and applicationCredentialSecret\"": ruleLeft,
	"BarbicanProviderAppCredIDRef MaxProperties=1": ruleLeft,
	"BarbicanProviderAppCredIDRef MinProperties=1": ruleLeft,
	"BarbicanProviderUsernameRef MaxProperties=1":  ruleLeft,
	"BarbicanProviderUsernameRef MinProperties=1":  ruleLeft,
	"CRDProvider XValidation:rule=\"has(self.auth) || has(self.authRef)\",message=\"one of auth or authRef is required\"": ruleLeft,
	"ConjurAuth MaxProperties=1": ruleLeft,
	"ConjurAuth MinProperties=1": ruleLeft,
	"DopplerAuth XValidation:rule=\"(has(self.secretRef) && !has(self.oidcConfig)) || (!has(self.secretRef) && has(self.oidcConfig))\",message=\"Exactly one of 'secretRef' or 'oidcConfig' must be specified\"": ruleLeft,
	"ExternalSecretRewrite MaxProperties=1": ruleLeft,
	"ExternalSecretRewrite MinProperties=1": ruleLeft,
	"FetchingPolicy MaxProperties=1":        ruleLeft,
	"FetchingPolicy MinProperties=1":        ruleLeft,
	"GithubProvider XValidation:rule=\"self.secretType != 'Dependabot' || !has(self.environment) || size(self.environment) == 0\",message=\"Dependabot secrets do not support environments\"": ruleLeft,
	"IBMAuth MaxProperties=1":        ruleLeft,
	"IBMAuth MinProperties=1":        ruleLeft,
	"KubernetesAuth MaxProperties=1": ruleLeft,
	"KubernetesAuth MinProperties=1": ruleLeft,
	"NebiusAuth XValidation:rule=\"(has(self.serviceAccountCredsSecretRef) && has(self.serviceAccountCredsSecretRef.name) && size(self.serviceAccountCredsSecretRef.name) > 0 ? 1 : 0) + (has(self.tokenSecretRef) && has(self.tokenSecretRef.name) && size(self.tokenSecretRef.name) > 0 ? 1 : 0) + (has(self.workloadIdentity) ? 1 : 0) == 1\",message=\"exactly one of serviceAccountCredsSecretRef, tokenSecretRef, or workloadIdentity must be set\"": ruleLeft,
	"NgrokAuth MaxProperties=1": ruleLeft,
	"NgrokAuth MinProperties=1": ruleLeft,
	"PulumiAuth XValidation:rule=\"(has(self.accessToken) && !has(self.oidcConfig)) || (!has(self.accessToken) && has(self.oidcConfig))\",message=\"Exactly one of 'accessToken' or 'oidcConfig' must be specified\"": ruleLeft,
	"PulumiProvider XValidation:rule=\"(has(self.auth) && !has(self.accessToken)) || (!has(self.auth) && has(self.accessToken))\",message=\"Exactly one of 'auth' or deprecated 'accessToken' must be specified\"":    ruleLeft,
	"SecretServerProvider XValidation:rule=\"has(self.token) || (has(self.username) && has(self.password))\",message=\"either token, or both username and password, must be set\"":                                    ruleLeft,
	"SecretServerProviderRef XValidation:rule=\"has(self.value) != has(self.secretRef)\",message=\"exactly one of value or secretRef must be set\"":                                                                   ruleLeft,
	"StoreGeneratorSourceRef MaxProperties=1": ruleLeft,
	"StoreGeneratorSourceRef MinProperties=1": ruleLeft,
	"StoreSourceRef MaxProperties=1":          ruleChecked,
	"StoreSourceRef MinProperties=1":          ruleLeft,
}

// TestExternalSecretsKinds_Rules holds externalSecretsRules to the source, in
// both directions, and every rule in it to a type one of the kinds decodes.
func TestExternalSecretsKinds_Rules(t *testing.T) {
	src := readExternalSecretsSource(t)
	reached := map[string]bool{}
	for _, kind := range externalSecretsKinds {
		reached[kind.typ.Name()] = true
		walkKindFields(kind.typ, src.required, func(f kindField) {
			if strings.HasPrefix(f.owner.PkgPath(), externalSecretsModulePath+"/") {
				reached[f.owner.Name()] = true
			}
		})
	}
	for _, rule := range src.rules {
		at, _, _ := strings.Cut(rule, " ")
		typ, _, _ := strings.Cut(at, ".")
		if !reached[typ] {
			continue
		}
		answer, ok := externalSecretsRules[rule]
		switch {
		case !ok:
			t.Errorf("the source declares the rule %q, and externalSecretsRules does not classify it: %q: ruleLeft,", rule, rule)
		case answer != ruleChecked && answer != ruleLeft:
			t.Errorf("the rule %q is classified %q, which is no answer", rule, answer)
		}
	}
	for rule := range externalSecretsRules {
		if !slices.Contains(src.rules, rule) {
			t.Errorf("externalSecretsRules holds %q, which the source does not declare on a type these kinds decode", rule)
		}
	}
	if len(src.rules) == 0 {
		t.Fatal("the source declares no rule; it is not being read")
	}
}

// TestExternalSecretsKinds_ProviderUnion: a store configures exactly one
// provider, the rule the kinds hold. Every field of the provider type is a
// pointer the type omits when unset, so counting the set ones counts the
// authored keys.
func TestExternalSecretsKinds_ProviderUnion(t *testing.T) {
	typ := reflect.TypeFor[esv1.SecretStoreProvider]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		_, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Type.Kind() != reflect.Pointer || f.Type.Elem().Kind() != reflect.Struct || !slices.Contains(strings.Split(opts, ","), "omitempty") {
			t.Errorf("SecretStoreProvider.%s is a %s tagged %q; authoredProviders counts pointers to a provider that are omitted when unset", f.Name, f.Type, f.Tag.Get("json"))
		}
	}
	for name, tc := range map[string]struct {
		provider *esv1.SecretStoreProvider
		want     string
	}{
		"one":          {&esv1.SecretStoreProvider{Vault: &esv1.VaultProvider{}}, ""},
		"none":         {&esv1.SecretStoreProvider{}, "provider: configures no provider; the API takes exactly one"},
		"no provider":  {nil, "provider: configures no provider; the API takes exactly one"},
		"two":          {&esv1.SecretStoreProvider{Vault: &esv1.VaultProvider{}, AWS: &esv1.AWSProvider{}}, "provider: configures 2 providers (aws, vault); the API takes exactly one"},
		"three, named": {&esv1.SecretStoreProvider{Webhook: &esv1.WebhookProvider{}, Fake: &esv1.FakeProvider{}, AWS: &esv1.AWSProvider{}}, "provider: configures 3 providers (aws, fake, webhook); the API takes exactly one"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateSecretStore(&esv1.SecretStoreSpec{Provider: tc.provider})
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want none", err)
			case tc.want != "" && (err == nil || err.Error() != tc.want):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// externalSecretsInlineValues classifies every union of a `value` and a
// `secretRef` a store's provider holds, by the path of the union: true where
// what it holds is a credential, which a policy that forbids explicit secrets
// refuses as a `value`, false where it is an identifier, which it does not.
var externalSecretsInlineValues = map[string]bool{
	"provider.barbican.auth.applicationCredentialID": false,
	"provider.barbican.auth.username":                false,
	"provider.beyondtrust.auth.apiKey":               true,
	"provider.beyondtrust.auth.certificate":          false,
	"provider.beyondtrust.auth.certificateKey":       true,
	"provider.beyondtrust.auth.clientId":             false,
	"provider.beyondtrust.auth.clientSecret":         true,
	"provider.delinea.clientId":                      false,
	"provider.delinea.clientSecret":                  true,
	"provider.scaleway.accessKey":                    false,
	"provider.scaleway.secretKey":                    true,
	"provider.secretserver.password":                 true,
	"provider.secretserver.token":                    true,
	"provider.secretserver.username":                 false,
}

// nestedAt returns the JSON object that holds leaf under the dotted path.
func nestedAt(path string, leaf any) map[string]any {
	keys := strings.Split(path, ".")
	out := map[string]any{keys[len(keys)-1]: leaf}
	for i := len(keys) - 2; i >= 0; i-- {
		out = map[string]any{keys[i]: out}
	}
	return out
}

// secretStoreSpecOf decodes a spec from the JSON object props.
func secretStoreSpecOf(t *testing.T, props map[string]any) *esv1.SecretStoreSpec {
	t.Helper()
	data, err := json.Marshal(props)
	if err != nil {
		t.Fatalf("marshal the spec: %v", err)
	}
	var spec esv1.SecretStoreSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("decode the spec: %v", err)
	}
	return &spec
}

// TestExternalSecretsKinds_InlineValues finds, in the linked module's types,
// every struct a store's provider holds that has a string `value` beside a
// `secretRef`, and holds each to its row of externalSecretsInlineValues: an
// unclassified one fails, and so does a row no such struct stands behind.
// Under a policy that forbids explicit secrets the `value` of a credential is
// refused with the explicit-secret class, naming the field and its
// replacement and nothing of the value; the `value` of an identifier is not,
// and neither is a reference to a Secret. A policy that allows explicit
// secrets, and one that does not answer, pass every one.
func TestExternalSecretsKinds_InlineValues(t *testing.T) {
	src := readExternalSecretsSource(t)
	found := map[string]bool{}
	walkKindFields(reflect.TypeFor[esv1.SecretStoreSpec](), src.required, func(f kindField) {
		name, _, _ := strings.Cut(f.field.Tag.Get("json"), ",")
		if name != "value" || f.field.Type.Kind() != reflect.String {
			return
		}
		for i := range f.owner.NumField() {
			if sibling, _, _ := strings.Cut(f.owner.Field(i).Tag.Get("json"), ","); sibling == "secretRef" {
				found[strings.TrimSuffix(f.path, ".value")] = true
			}
		}
	})
	if got, want := slices.Sorted(maps.Keys(found)), slices.Sorted(maps.Keys(externalSecretsInlineValues)); !slices.Equal(got, want) {
		t.Fatalf("the provider types hold a value beside a secretRef at %v\nexternalSecretsInlineValues classifies %v", got, want)
	}
	var credentials, listed []string
	for path, credential := range externalSecretsInlineValues {
		if credential {
			credentials = append(credentials, path)
		}
	}
	for _, field := range secretStoreInlineCredentials {
		listed = append(listed, field.at)
	}
	slices.Sort(credentials)
	if !slices.Equal(listed, credentials) {
		t.Fatalf("secretStoreInlineCredentials lists %v\nthe credentials are %v", listed, credentials)
	}

	forbidding := noExplicitSecrets{&oam.NoopPolicy{}}
	for path, credential := range externalSecretsInlineValues {
		t.Run(path, func(t *testing.T) {
			literal := secretStoreSpecOf(t, nestedAt(path, map[string]any{"value": sensitiveValue}))
			if got := reflect.ValueOf(literal.Provider).Elem(); got.IsZero() {
				t.Fatalf("the spec decoded from %s holds no provider", path)
			}
			err := enforceSecretStorePolicy(literal, forbidding)
			if !credential {
				if err != nil {
					t.Errorf("an identifier as a value: %v, want it passed", err)
				}
			} else {
				var refusal *oam.PolicyRefusal
				want := path + ".value: holds the credential in the object, and the environment policy forbids explicit secrets; name the key of a Secret created out of band in " + path + ".secretRef instead"
				switch {
				case err == nil:
					t.Fatal("a credential as a value passed a policy that forbids explicit secrets")
				case !errors.As(err, &refusal) || refusal.Class != oam.RefusalExplicitSecret:
					t.Errorf("err = %v (%T), want a refusal of class %s", err, err, oam.RefusalExplicitSecret)
				case err.Error() != want:
					t.Errorf("err = %q\nwant  %q", err, want)
				}
				if err != nil && strings.Contains(err.Error(), sensitiveValue) {
					t.Errorf("the refusal carries the value: %v", err)
				}
			}
			if err := enforceSecretStorePolicy(literal, &oam.NoopPolicy{}); err != nil {
				t.Errorf("under a policy that does not answer: %v, want it passed", err)
			}
			referenced := secretStoreSpecOf(t, nestedAt(path, map[string]any{"secretRef": map[string]any{"name": "credentials", "key": "value"}}))
			if err := enforceSecretStorePolicy(referenced, forbidding); err != nil {
				t.Errorf("a reference to a Secret: %v, want it passed", err)
			}
		})
	}
}

// TestExternalSecretsKinds_FakeData: the data of the fake provider is nothing
// but values, and a policy that forbids explicit secrets refuses any entry of
// it. A fake provider with an empty list holds none and passes.
func TestExternalSecretsKinds_FakeData(t *testing.T) {
	forbidding := noExplicitSecrets{&oam.NoopPolicy{}}
	entry := map[string]any{"key": "/database/password", "value": sensitiveValue}
	withData := secretStoreSpecOf(t, nestedAt("provider.fake.data", []any{entry}))
	err := enforceSecretStorePolicy(withData, forbidding)
	var refusal *oam.PolicyRefusal
	switch {
	case err == nil:
		t.Fatal("fake data passed a policy that forbids explicit secrets")
	case !errors.As(err, &refusal) || refusal.Class != oam.RefusalExplicitSecret:
		t.Errorf("err = %v (%T), want a refusal of class %s", err, err, oam.RefusalExplicitSecret)
	case !strings.HasPrefix(err.Error(), "provider.fake.data: holds the values the store serves in the object"):
		t.Errorf("err = %v, want it to name provider.fake.data", err)
	case strings.Contains(err.Error(), sensitiveValue):
		t.Errorf("the refusal carries the value: %v", err)
	}
	if err := enforceSecretStorePolicy(withData, &oam.NoopPolicy{}); err != nil {
		t.Errorf("under a policy that does not answer: %v, want it passed", err)
	}
	empty := secretStoreSpecOf(t, nestedAt("provider.fake.data", []any{}))
	if err := enforceSecretStorePolicy(empty, forbidding); err != nil {
		t.Errorf("a fake provider with no data: %v, want it passed", err)
	}
}

// TestExternalSecretsKinds_DefaultedZeros is
// TestPolicyFreeKinds_NoDefaultedZeros for the kinds of the External Secrets
// Operator's API, whose types publish no field description to read a default
// from. A number or a boolean these kinds decode that is omitted when zero
// and that the CRD defaults to something else is one on which an authored 0
// or false would be replaced: each kind's defaulted-zero list
// (policyFreeKind.defaultedZeros) must hold exactly those fields, each with
// its default, so that the value is refused. The default is the field's
// kubebuilder marker, read from the linked module's source. A field of that
// shape on a type whose source is not read fails too.
//
// The two external-secret kinds have none at this pin: the API's defaults on
// their types are strings and durations. An authored empty string there is
// omitted and defaulted, as on every kind: only an authored 0 or false is
// held to be carried.
func TestExternalSecretsKinds_DefaultedZeros(t *testing.T) {
	src := readExternalSecretsSource(t)
	// Vacuity guard: a default marker is read.
	if m := src.fields[externalSecretsModulePath+"/externalsecrets/v1.ExternalSecretTarget.CreationPolicy"]; m.def != "Owner" {
		t.Fatalf("ExternalSecretTarget.CreationPolicy has the default %q (read: %v), want Owner; the source is not being read", m.def, m.hasDefault)
	}
	walked := map[string]bool{}
	for _, kind := range externalSecretsKinds {
		derived := map[string]string{}
		walkKindFields(kind.typ, src.required, func(f kindField) {
			if !f.omitemptyScalar() {
				return
			}
			at := kind.typ.Name() + ": " + f.path
			walked[at] = true
			m, read := src.markers(f)
			switch {
			case !read:
				t.Errorf("%s (%s.%s) is omitted when zero, and its default cannot be read: the source of its type is not", at, f.owner, f.field.Name)
			case m.hasDefault && !crdDefaultIsZero(m.def):
				derived[f.path] = m.def
			}
		})
		for _, path := range slices.Sorted(maps.Keys(derived)) {
			switch def, listed := kind.defaulted[path]; {
			case !listed:
				t.Errorf("%s: %s is omitted when zero and defaults to %s: an authored zero would be replaced, and the kind's defaulted-zero list does not hold the field", kind.component, path, derived[path])
			case def != derived[path]:
				t.Errorf("%s: the defaulted-zero list gives %s the default %s; the source says %s", kind.component, path, def, derived[path])
			}
		}
		for _, path := range slices.Sorted(maps.Keys(kind.defaulted)) {
			if _, ok := derived[path]; !ok {
				t.Errorf("%s: the defaulted-zero list holds %s, which the source does not show as omitted when zero and defaulted to another value", kind.component, path)
			}
		}
	}
	// The list refuses: an authored false on a listed field does not reach the
	// object, on either store.
	for _, kind := range []*policyHeldKind[esv1.SecretStoreSpec]{secretStoreKind, clusterSecretStoreKind} {
		_, err := kind.config(&oam.Component{Name: "store", Properties: map[string]any{
			"provider": map[string]any{"infisical": map[string]any{
				"secretsScope": map[string]any{"expandSecretReferences": false},
			}},
		}})
		const want = "provider.infisical.secretsScope.expandSecretReferences: false cannot be carried by the external-secrets API types (the field is omitted when zero, so the API server would apply its default true)"
		if err == nil || err.Error() != want {
			t.Errorf("an authored false on a defaulted-zero field: got %v, want %s", err, want)
		}
	}
	for _, at := range []string{
		"ExternalSecretSpec: target.immutable",
		"ClusterExternalSecretSpec: externalSecretSpec.target.immutable",
		"SecretStoreSpec: provider.beyondtrust.server.decrypt",
	} {
		if !walked[at] {
			t.Errorf("the walk did not reach %s; it found %v", at, slices.Sorted(maps.Keys(walked)))
		}
	}
}
