package components

import (
	"bytes"
	"fmt"
	"go/format"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

// The generated file of the kinds' required paths (kind_required.go), and the
// switch under which TestKindComponents_RequiredIsGenerated writes it.
const (
	kindRequiredFile   = "zz_generated_kind_required.go"
	kindRequiredUpdate = "UPDATE_KIND_REQUIRED"
)

// requiredWrittenExceptions are the fields, by kind and path, that the
// derivation lists and a generated kind does not refuse unauthored, each with
// the reason: a harness sample no rule of the API admits, so the omission
// cannot be shown refused (go-kure/launcher#883). The generated list leaves
// them out, and the pin keeps them. An entry the derivation no longer lists,
// or the pin no longer holds, fails the test. None at this pin.
var requiredWrittenExceptions = map[string]string{}

// kindForcedParent is what the API server answers for a generated path under
// a parent the Go type writes whether or not it was authored and the API does
// not require: the object with that parent left out. The kind asks for the
// path only where the parent is authored; the object without it carries the
// parent empty.
type kindForcedParent struct {
	// parent is the path of that parent.
	parent string
	// answer is the API server's answer for the object without it, and why
	// the kind does not refuse that object.
	answer string
}

// kindRequiredForced holds, by kind and path, the answer for every generated
// path under such a parent; TestKindComponents_RequiredIsGenerated fails on
// one without an answer and on an answer for no such path, and
// TestKindComponents_GeneratedRequiredBothWays shows each answer on the CRD.
// The API server accepts every one, and in each the object without the parent
// is one cert-manager handles: refusing it would refuse a working document.
// Each is kept as it was, asked for only under an authored parent.
var kindRequiredForced = map[string]kindForcedParent{
	"certificate keystores.jks.passwordSecretRef.name":                         {"keystores.jks.passwordSecretRef", "accepted: the keystore's password is written in `password` instead"},
	"certificate keystores.pkcs12.passwordSecretRef.name":                      {"keystores.pkcs12.passwordSecretRef", "accepted: the keystore's password is written in `password` instead"},
	"clusterissuer acme.solvers[].dns01.route53.secretAccessKeySecretRef.name": {"acme.solvers[].dns01.route53.secretAccessKeySecretRef", "accepted: Route 53 takes ambient credentials or `accessKeyID`"},
	"clusterissuer acme.solvers[].dns01.rfc2136.tsigSecretSecretRef.name":      {"acme.solvers[].dns01.rfc2136.tsigSecretSecretRef", "accepted: an RFC 2136 server is updated without TSIG"},
	"clusterissuer vault.auth.kubernetes.secretRef.name":                       {"vault.auth.kubernetes.secretRef", "accepted: Kubernetes auth takes `serviceAccountRef` instead"},
	"issuer acme.solvers[].dns01.route53.secretAccessKeySecretRef.name":        {"acme.solvers[].dns01.route53.secretAccessKeySecretRef", "accepted: Route 53 takes ambient credentials or `accessKeyID`"},
	"issuer acme.solvers[].dns01.rfc2136.tsigSecretSecretRef.name":             {"acme.solvers[].dns01.rfc2136.tsigSecretSecretRef", "accepted: an RFC 2136 server is updated without TSIG"},
	"issuer vault.auth.kubernetes.secretRef.name":                              {"vault.auth.kubernetes.secretRef", "accepted: Kubernetes auth takes `serviceAccountRef` instead"},
}

// generatedKindRequiredLists are the required lists of the generated kinds,
// by component, as the kinds hold them.
var generatedKindRequiredLists = map[string]map[string]string{
	"certificate":           certificateKind.required,
	"clusterissuer":         clusterIssuerKind.required,
	"issuer":                issuerKind.required,
	"clusterexternalsecret": clusterExternalSecretKind.required,
	"clustersecretstore":    clusterSecretStoreKind.required,
	"externalsecret":        externalSecretKind.required,
	"secretstore":           secretStoreKind.required,
}

// requiredWrittenKind returns the measured kind of a component.
func requiredWrittenKind(component string) (pinKind, bool) {
	for _, k := range requiredWrittenKinds {
		if k.component == component {
			return k, true
		}
	}
	return pinKind{}, false
}

// underPath says path is at or under the path at: the same path, a field of
// it, or an element or a value of it.
func underPath(path, at string) bool {
	rest, ok := strings.CutPrefix(path, at)
	return ok && (rest == "" || rest[0] == '.' || rest[0] == '[' || rest[0] == '{')
}

// unauthorableAncestor returns the path requiredWrittenUnauthorable lists for
// the component that path is at or under.
func unauthorableAncestor(component, path string) (string, bool) {
	for _, key := range slices.Sorted(maps.Keys(requiredWrittenUnauthorable)) {
		if c, at, _ := strings.Cut(key, " "); c == component && underPath(path, at) {
			return at, true
		}
	}
	return "", false
}

// generatedRequiredPaths returns, sorted by path, the fields of a kind that its
// generated required list holds: what deriveRequiredWritten lists, but for a
// value requiredWrittenValidDefaults holds, a field requiredWrittenExceptions
// holds, and a field under a path requiredWrittenUnauthorable lists.
func generatedRequiredPaths(t *testing.T, k pinKind) []requiredWrittenField {
	t.Helper()
	var out []requiredWrittenField
	for _, d := range deriveRequiredWritten(t, k, k.schema(t)) {
		key := k.component + " " + d.path
		if _, ok := requiredWrittenValidDefaults[key]; ok {
			continue
		}
		if _, ok := requiredWrittenExceptions[key]; ok {
			continue
		}
		if _, ok := unauthorableAncestor(k.component, d.path); ok {
			continue
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b requiredWrittenField) int { return strings.Compare(a.path, b.path) })
	return out
}

// renderKindRequired returns the generated file for the given paths, by kind
// component, and the stores' unapplied defaults. A path kindRequiredForced
// answers for is named in the file's comment with its answer.
func renderKindRequired(t *testing.T, paths map[string][]string, defaults map[string]string) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteString("// Code generated by TestKindComponents_RequiredIsGenerated with " + kindRequiredUpdate + "=1. DO NOT EDIT.\n\n")
	b.WriteString("package components\n\n")
	b.WriteString("// kindRequiredPaths lists, by kind component, the fields the kind's API\n")
	b.WriteString("// requires and its Go type writes whether or not they were authored: see\n")
	b.WriteString("// generatedKindRequired.\n")
	var forced []string
	for _, component := range slices.Sorted(maps.Keys(paths)) {
		for _, path := range paths[component] {
			if f, ok := kindRequiredForced[component+" "+path]; ok {
				forced = append(forced, "//\t"+component+" "+path+" ("+f.parent+" left out): "+f.answer+"\n")
			}
		}
	}
	if len(forced) > 0 {
		b.WriteString("//\n")
		b.WriteString("// Of them, these are under a parent the type writes unauthored and the API\n")
		b.WriteString("// does not require; each is asked for only where the parent is authored, and\n")
		b.WriteString("// the API server's answer for the object with the parent left out is:\n")
		b.WriteString("//\n")
		for _, line := range forced {
			b.WriteString(line)
		}
	}
	b.WriteString("var kindRequiredPaths = map[string][]string{\n")
	for _, component := range slices.Sorted(maps.Keys(paths)) {
		b.WriteString("\t" + strconv.Quote(component) + ": {\n")
		for _, path := range paths[component] {
			b.WriteString("\t\t" + strconv.Quote(path) + ",\n")
		}
		b.WriteString("\t},\n")
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

// TestKindComponents_RequiredIsGenerated holds the generated required paths of
// every kind kindRequiredAPI lists to their derivation (generatedRequiredPaths),
// in both directions and path by path, the stores' unapplied defaults to
// theirs from the External Secrets Operator's source, and the checked-in file
// to the bytes the derivation writes. After a change that moves a path, in a
// kind or in a dependency, run it once with UPDATE_KIND_REQUIRED=1: it writes
// the file and compares nothing, so it is refused where CI is set.
//
// It holds kindRequiredForced to the paths under a parent the type writes
// unauthored, in both directions too: a new such path fails here until the
// API server's answer for its parent left out is recorded.
func TestKindComponents_RequiredIsGenerated(t *testing.T) {
	update := os.Getenv(kindRequiredUpdate) == "1"
	if update && os.Getenv("CI") != "" {
		t.Fatalf("%s=1 rewrites %s and compares nothing; it is refused where CI is set", kindRequiredUpdate, kindRequiredFile)
	}
	derived := map[string][]string{}
	forced := map[string]bool{}
	for _, component := range slices.Sorted(maps.Keys(kindRequiredAPI)) {
		k, ok := requiredWrittenKind(component)
		if !ok {
			t.Fatalf("%s is generated, and requiredWrittenKinds does not measure it", component)
		}
		for _, d := range generatedRequiredPaths(t, k) {
			derived[component] = append(derived[component], d.path)
			if d.field.forced {
				forced[component+" "+d.path] = true
			}
		}
		if len(derived[component]) == 0 {
			t.Fatalf("the derivation lists no path of %s", component)
		}
	}
	_, _, defaults := readExternalSecretsSource(t).derive(t, reflect.TypeFor[esv1.SecretStoreSpec]())
	// Vacuity guards: the derivation reaches a provider's own fields, a field
	// of a Kubernetes type a spec embeds, and the top level, and it gives a
	// default.
	for _, guard := range []struct{ component, path string }{
		{"secretstore", "provider"},
		{"secretstore", "provider.vault.server"},
		{"secretstore", "provider.aws.region"},
		{"secretstore", "provider.vault.auth.kubernetes.serviceAccountRef.name"},
		{"issuer", "acme.solvers[].http01.ingress.podTemplate.spec.affinity.podAffinity.requiredDuringSchedulingIgnoredDuringExecution[].topologyKey"},
		{"certificate", "secretName"},
	} {
		if !slices.Contains(derived[guard.component], guard.path) {
			t.Fatalf("the derivation does not hold %s of %s; it found %d paths", guard.path, guard.component, len(derived[guard.component]))
		}
	}
	if def := defaults["provider.vault.version"]; def != "v2" {
		t.Fatalf("the derivation gives provider.vault.version the default %q, want v2; it found %d defaults", def, len(defaults))
	}
	for _, key := range slices.Sorted(maps.Keys(forced)) {
		if _, ok := kindRequiredForced[key]; !ok {
			t.Errorf("%s is under a parent the type writes unauthored and the API does not require; record in kindRequiredForced what the API server answers for the object with that parent left out", key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(kindRequiredForced)) {
		component, path, _ := strings.Cut(key, " ")
		if !forced[key] {
			t.Errorf("kindRequiredForced answers for %s, which is no generated path under a parent the type writes unauthored; drop it", key)
		}
		if parent := kindRequiredForced[key].parent; parent == path || !underPath(path, parent) {
			t.Errorf("kindRequiredForced gives %s of %s the parent %s, which is not above it", path, component, parent)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(requiredWrittenExceptions)) {
		component, path, _ := strings.Cut(key, " ")
		k, ok := requiredWrittenKind(component)
		if !ok || !slices.ContainsFunc(deriveRequiredWritten(t, k, k.schema(t)), func(d requiredWrittenField) bool { return d.path == path }) {
			t.Errorf("requiredWrittenExceptions lists %s, which the derivation no longer lists; drop it", key)
		}
	}
	if t.Failed() {
		return
	}

	want := renderKindRequired(t, derived, defaults)
	if update {
		if err := os.WriteFile(kindRequiredFile, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", kindRequiredFile, err)
		}
		t.Logf("wrote %s: %d kinds, %d defaults", kindRequiredFile, len(derived), len(defaults))
		return
	}
	for _, component := range slices.Sorted(maps.Keys(kindRequiredPaths)) {
		if _, ok := kindRequiredAPI[component]; !ok {
			t.Errorf("%s lists %s, which kindRequiredAPI does not; regenerate with %s=1", kindRequiredFile, component, kindRequiredUpdate)
		}
	}
	for _, component := range slices.Sorted(maps.Keys(derived)) {
		missing, extra := pathsDiffer(kindRequiredPaths[component], derived[component])
		for _, path := range missing {
			t.Errorf("%s: %s is required by the API and written unauthored, and %s does not list it; regenerate with %s=1", component, path, kindRequiredFile, kindRequiredUpdate)
		}
		for _, path := range extra {
			t.Errorf("%s: %s is listed in %s, and the derivation does not list it; regenerate with %s=1", component, path, kindRequiredFile, kindRequiredUpdate)
		}
	}
	for _, diff := range defaultsDiffer(secretStoreUnappliedDefaults, defaults) {
		t.Errorf("%s; regenerate %s with %s=1", diff, kindRequiredFile, kindRequiredUpdate)
	}
	got, err := os.ReadFile(kindRequiredFile)
	if err != nil {
		t.Fatalf("read %s: %v", kindRequiredFile, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the derivation writes; regenerate with %s=1", kindRequiredFile, kindRequiredUpdate)
	}
	for _, component := range slices.Sorted(maps.Keys(derived)) {
		t.Logf("%s: %d paths", component, len(derived[component]))
	}
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

// TestKindComponents_GeneratedComparisonBites: the comparison
// TestKindComponents_RequiredIsGenerated makes finds a path taken out of a
// kind's list and one put into it, by name, and a default taken out, put in
// or changed; and the file such lists would be is not the checked-in one,
// which is what the checked-in lists render.
func TestKindComponents_GeneratedComparisonBites(t *testing.T) {
	file, err := os.ReadFile(kindRequiredFile)
	if err != nil {
		t.Fatalf("read %s: %v", kindRequiredFile, err)
	}
	if !bytes.Equal(renderKindRequired(t, kindRequiredPaths, secretStoreUnappliedDefaults), file) {
		t.Fatalf("the checked-in lists do not render the checked-in file; regenerate with %s=1", kindRequiredUpdate)
	}
	for _, c := range []struct{ component, dropped, added string }{
		{"secretstore", "provider.vault.server", "provider.vault.nothing"},
		{"issuer", "acme.solvers[].http01.ingress.podTemplate.spec.affinity.podAffinity.requiredDuringSchedulingIgnoredDuringExecution[].topologyKey", "acme.nothing"},
	} {
		listed := kindRequiredPaths[c.component]
		if !slices.Contains(listed, c.dropped) {
			t.Fatalf("the generated list of %s does not hold %s", c.component, c.dropped)
		}
		shorter := slices.DeleteFunc(slices.Clone(listed), func(path string) bool { return path == c.dropped })
		if missing, extra := pathsDiffer(shorter, listed); !slices.Equal(missing, []string{c.dropped}) || extra != nil {
			t.Errorf("%s: a list without %s: missing %v, extra %v; want that one path missing", c.component, c.dropped, missing, extra)
		}
		longer := append(slices.Clone(listed), c.added)
		if missing, extra := pathsDiffer(longer, listed); missing != nil || !slices.Equal(extra, []string{c.added}) {
			t.Errorf("%s: a list with %s: missing %v, extra %v; want that one path extra", c.component, c.added, missing, extra)
		}
		for name, paths := range map[string][]string{"without " + c.dropped: shorter, "with " + c.added: longer} {
			lists := maps.Clone(kindRequiredPaths)
			lists[c.component] = paths
			if bytes.Equal(renderKindRequired(t, lists, secretStoreUnappliedDefaults), file) {
				t.Errorf("the file of a list of %s %s equals the checked-in one", c.component, name)
			}
		}
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
		if bytes.Equal(renderKindRequired(t, kindRequiredPaths, c.defaults), file) {
			t.Errorf("the file of defaults with %s equals the checked-in one", name)
		}
	}
}

// TestKindComponents_GeneratedRequiredHeld holds the required list of every
// generated kind to its generated paths and to the classes the derivation does
// not produce. Each generated path is in the list, saying what requiredText
// holds for it or, without an entry there, its class's sentence. Every other
// entry of the list is in a held class: an unapplied default of a store
// (secretStoreUnappliedDefaults), saying its sentence, or a field under a path
// the kind refuses whatever is authored there (requiredWrittenUnauthorable),
// which the kind asks for first. An entry in neither fails, and so does a text
// of requiredText for a path that is not generated.
//
// The class sentences of the external-secrets API are the ones its kinds said
// before the lists were generated, byte for byte.
func TestKindComponents_GeneratedRequiredHeld(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{fmt.Sprintf(generatedRequiresField, "external-secrets"), "the external-secrets API requires this field"},
		{fmt.Sprintf(generatedRequiresName, "external-secrets"), "the external-secrets API requires the name of what this refers to"},
	} {
		if c.got != c.want {
			t.Errorf("a generated path of the external-secrets API says %q, and said %q", c.got, c.want)
		}
	}
	if got, want := slices.Sorted(maps.Keys(generatedKindRequiredLists)), slices.Sorted(maps.Keys(kindRequiredAPI)); !slices.Equal(got, want) {
		t.Fatalf("the generated kinds' lists are of %v, and kindRequiredAPI lists %v", got, want)
	}
	if got, want := slices.Sorted(maps.Keys(requiredText)), slices.Sorted(maps.Keys(kindRequiredAPI)); !slices.Equal(got, want) {
		t.Errorf("requiredText holds texts of %v, and kindRequiredAPI lists %v", got, want)
	}
	for _, component := range slices.Sorted(maps.Keys(kindRequiredAPI)) {
		t.Run(component, func(t *testing.T) {
			api, list, generated := kindRequiredAPI[component], generatedKindRequiredLists[component], kindRequiredPaths[component]
			for _, path := range generated {
				want, ok := requiredText[component][path]
				switch {
				case ok:
				case path == "name" || strings.HasSuffix(path, ".name"):
					want = fmt.Sprintf(generatedRequiresName, api)
				default:
					want = fmt.Sprintf(generatedRequiresField, api)
				}
				if says, listed := list[path]; !listed || says != want {
					t.Errorf("%s is generated, and the list says %q of it (listed: %v), want %q", path, says, listed, want)
				}
			}
			held := 0
			for _, path := range slices.Sorted(maps.Keys(list)) {
				says := list[path]
				if slices.Contains(generated, path) {
					continue
				}
				held++
				if def, ok := secretStoreUnappliedDefaults[path]; ok && (component == "secretstore" || component == "clustersecretstore") {
					if want := fmt.Sprintf(externalSecretsUnappliedDefault, def); says != want {
						t.Errorf("%s is an unapplied default, and the list says %q of it, want %q", path, says, want)
					}
					continue
				}
				if _, ok := unauthorableAncestor(component, path); ok {
					if strings.TrimSpace(says) == "" {
						t.Errorf("%s says nothing of itself", path)
					}
					continue
				}
				t.Errorf("%s is in the required list, and it is neither generated nor in a held class (an unapplied default, a field under a path requiredWrittenUnauthorable lists)", path)
			}
			for _, path := range slices.Sorted(maps.Keys(requiredText[component])) {
				if !slices.Contains(generated, path) {
					t.Errorf("requiredText says %q of %s, which is not generated", requiredText[component][path], path)
				}
			}
			t.Logf("%d generated paths, %d held, %d with their own text", len(generated), held, len(requiredText[component]))
		})
	}
}

// certManagerCRDOf reads the CRD of a cert-manager kind, nil for another
// component.
func certManagerCRDOf(t *testing.T, component string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	var file string
	for _, kind := range certManagerKinds {
		if kind.component == component {
			file = kind.crd
		}
	}
	if file == "" {
		return nil
	}
	data, err := os.ReadFile(certManagerCRD(t, file))
	if err != nil {
		t.Fatalf("read the CRD of %s: %v", component, err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD of %s: %v", component, err)
	}
	return &crd
}

// generatedHarnessSamples are what a harness control of one generated path
// authors beside what pinControl fills, by kind and path: an expression rule
// of the CRD the filled control does not meet.
var generatedHarnessSamples = map[string]map[string]any{
	// A Venafi issuer configures exactly one of tpp, cloud and ngts.
	"issuer venafi.zone":        {"venafi.cloud.apiTokenSecretRef.name": "venafi-token"},
	"clusterissuer venafi.zone": {"venafi.cloud.apiTokenSecretRef.name": "venafi-token"},
}

// TestKindComponents_GeneratedRequiredBothWays holds every generated path of
// every generated kind both ways, as the pin measures it: a control that
// authors the path (its base, the path and the required fields around it)
// builds, and the same properties without it are refused, naming the path
// with an index for a list element and saying what the list says of it.
//
// Where the linked module ships the kind's CRD, the API server answers too,
// on one path of each type that declares one: the control's object is
// accepted, and the same object without the field is refused as missing a
// required one. The External Secrets Operator's module ships no CRD, and its
// kinds are answered by the kind only. Each answer kindRequiredForced records
// is shown on the CRD as well: the control without the parent builds, and the
// API server accepts the object.
func TestKindComponents_GeneratedRequiredBothWays(t *testing.T) {
	for _, component := range slices.Sorted(maps.Keys(kindRequiredAPI)) {
		t.Run(component, func(t *testing.T) {
			k, ok := requiredWrittenKind(component)
			if !ok {
				t.Fatalf("%s is generated, and requiredWrittenKinds does not measure it", component)
			}
			if k.altBase != nil || k.prefixes != nil {
				t.Fatalf("%s has an alternative base or prefixes, which this test does not measure", component)
			}
			schema := k.schema(t)
			base := pinCopy(k.base)
			pinFill(base, schema, "")
			var create *crdCreate
			if crd := certManagerCRDOf(t, component); crd != nil {
				create = crdCreateOf(t, crd, "v1")
			}
			list := generatedKindRequiredLists[component]
			answered, rows := map[reflect.Type]bool{}, 0
			for _, d := range generatedRequiredPaths(t, k) {
				path := d.path
				with, err := pinControl(k, base, path, schema)
				if err != nil {
					t.Errorf("%s: no control builds with the field authored: %v", path, err)
					continue
				}
				for at, value := range generatedHarnessSamples[component+" "+path] {
					parent, name, _ := pinParent(with, at, true)
					parent[name] = value
				}
				obj, err := pinBuild(k, with)
				if err != nil {
					t.Errorf("%s: the control is refused: %v", path, err)
					continue
				}
				without := pinCopy(with)
				parent, name, _ := pinParent(without, path, false)
				delete(parent, name)
				_, err = pinBuild(k, without)
				want := strings.ReplaceAll(path, "[]", "[0]") + ": required (" + list[path] + ")"
				switch {
				case err == nil:
					t.Errorf("%s: the omission builds", path)
				case !strings.Contains(err.Error(), want):
					t.Errorf("%s: the omission is refused with %q, want one saying %q", path, err, want)
				}
				if create == nil || answered[d.field.owner] {
					continue
				}
				answered[d.field.owner] = true
				rows++
				what := fmt.Sprintf("%s %s (%s)", component, path, d.field.owner)
				create.create(t, obj).accepted(t, what+", authored")
				objWithout := pinCopy(obj)
				parent, name, ok := pinParent(objWithout, "spec."+path, false)
				if !ok {
					t.Errorf("%s: the object does not hold the field", what)
					continue
				}
				delete(parent, name)
				answer := create.create(t, objWithout)
				if !slices.ContainsFunc(answer.schema, func(e *field.Error) bool {
					return e.Type == field.ErrorTypeRequired && strings.HasSuffix(e.Field, "."+pinLeaf(path))
				}) {
					t.Errorf("%s, left out of the object: the API server answers %q, want the field refused as required", what, answer)
				}
			}
			for _, key := range slices.Sorted(maps.Keys(kindRequiredForced)) {
				c, path, _ := strings.Cut(key, " ")
				if c != component {
					continue
				}
				f := kindRequiredForced[key]
				with, err := pinControl(k, base, path, schema)
				if err != nil {
					t.Errorf("%s: no control builds with the field authored: %v", path, err)
					continue
				}
				holder, name, ok := pinParent(with, f.parent, false)
				if !ok {
					t.Errorf("%s: the control does not author %s", path, f.parent)
					continue
				}
				delete(holder, name)
				obj, err := pinBuild(k, with)
				if err != nil {
					t.Errorf("%s: the control without %s is refused: %v; kindRequiredForced says the API server accepts it", path, f.parent, err)
					continue
				}
				if create == nil {
					t.Errorf("%s: kindRequiredForced answers for a kind no CRD is read for", key)
					continue
				}
				create.create(t, obj).accepted(t, fmt.Sprintf("%s %s, %s left out", component, path, f.parent))
			}
			if create != nil && rows == 0 {
				t.Errorf("no path of %s was answered by the API server", component)
			}
			t.Logf("%d API server rows", rows)
		})
	}
}

// TestUnderPath: a path is under itself, a field of it, an element and a value
// of it, and not under a path that only shares its prefix.
func TestUnderPath(t *testing.T) {
	for _, c := range []struct {
		path, at string
		want     bool
	}{
		{"data[].sourceRef.generatorRef", "data[].sourceRef.generatorRef", true},
		{"data[].sourceRef.generatorRef.name", "data[].sourceRef.generatorRef", true},
		{"specs[].nodeSelector", "specs", true},
		{"tablespaceStorage{}.kind", "tablespaceStorage", true},
		{"data[].sourceRef.generatorRefs", "data[].sourceRef.generatorRef", false},
		{"secretName", "secret", false},
	} {
		if got := underPath(c.path, c.at); got != c.want {
			t.Errorf("underPath(%q, %q) = %v, want %v", c.path, c.at, got, c.want)
		}
	}
}
