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
// cannot be shown refused, or an omission the API server accepts as the kind
// writes it and the operator handles, so refusing it would refuse a working
// document (go-kure/launcher#883). The generated list leaves them out, and the
// pin keeps them. An entry the derivation no longer lists, or the pin no
// longer holds, fails the test, and TestKindComponents_GeneratedRequiredBothWays
// shows each omission accepted on the kind's CRD where one is read.
var requiredWrittenExceptions = map[string]string{
	// The Go type writes the template's spec whether or not it was authored,
	// and the kind writes an unset container list as [] (CnpgPoolerConfig.Generate):
	// the Pooler CRD accepts both, and the operator adds its pgbouncer container
	// to an empty list as to an omitted spec.
	"cnpg-pooler template.spec.containers": "accepted: the kind writes the list as [], which the Pooler CRD accepts and to which the operator adds its pgbouncer container, with the template's spec authored or left out",
}

// requiredWrittenUndecided are the fields, by kind and path, that the
// derivation lists and a generated kind neither refuses unauthored nor keeps
// as an exception, because what the API server makes of them is not shown
// here: the fields of an owner reference in the metadata of an ephemeral
// volume's claim template, on the Prometheus operator's workload kinds. The Go
// source declares the whole ObjectMeta there; the operator's module ships no
// CRD to read, so whether the API server requires them or prunes them is not
// measured. The generated list leaves them out, the pin keeps them, and a
// follow-up of go-kure/launcher#883 that reads the operator's CRDs settles
// each. An entry the derivation no longer lists fails the test.
var requiredWrittenUndecided = promOpClaimOwnerReferences()

// promOpClaimOwnerReferences are the fields of an owner reference in the
// metadata of an ephemeral volume's claim template, on the Prometheus
// operator's workload kinds, each with why it is undecided.
func promOpClaimOwnerReferences() map[string]string {
	out := map[string]string{}
	for _, component := range []string{"alertmanager", "prometheus", "thanosruler"} {
		for _, at := range []string{"storage.ephemeral", "volumes[].ephemeral"} {
			for _, field := range []string{"apiVersion", "kind", "name", "uid"} {
				out[component+" "+at+".volumeClaimTemplate.metadata.ownerReferences[]."+field] = "the Go source requires it of an owner reference, and no CRD of the operator is read to show whether the API server requires it there or prunes the owner reference"
			}
		}
	}
	return out
}

// kindForcedParent is what the API server answers for a generated path under
// a parent the Go type writes whether or not it was authored and the API does
// not require: the object with that parent left out. The kind asks for the
// path only where the parent is authored; the object without it carries the
// parent as the type writes its zero value.
type kindForcedParent struct {
	// parent is the path of that parent.
	parent string
	// answer is the API server's answer for the object without it, "accepted:"
	// or "refused:", and why the kind refuses that object or does not.
	answer string
	// instead is what the properties without the parent author in its place,
	// by path: the alternative the API takes for it. Nil for none.
	instead map[string]any
	// refused says the kind refuses the properties without the parent:
	// through kindRequiredParents, or through a check of its own. An answer
	// the API server refuses must be refused by the kind.
	refused bool
}

// kindRequiredForced holds, by kind and path, the answer for every generated
// path under such a parent; TestKindComponents_RequiredIsGenerated fails on
// one without an answer and on an answer for no such path, and
// TestKindComponents_GeneratedRequiredBothWays shows what the kind does with
// each, and the answer on the kind's CRD where one is read. The Prometheus
// operator's module ships no CRD, so its kinds' answers are recorded from the
// operator's CRDs by hand (generatedKindsWithoutCRD), and a follow-up of
// go-kure/launcher#883 shows them on those CRDs.
//
// The cert-manager entries and the monitoring keySecret ones are optional
// alternatives: the API server accepts the object without the parent, which
// the operator handles, so refusing it would refuse a working document. Each
// is asked for only under an authored parent. A monitoring basic auth's
// username and password are accepted by the API server left out, and the
// operator refuses the basic auth without either; the kind refuses the parent
// left out (kindRequiredParents). A Probe's prober is refused by the API
// server without a url, and by the kind's own check.
var kindRequiredForced = map[string]kindForcedParent{
	"certificate keystores.jks.passwordSecretRef.name":                         {"keystores.jks.passwordSecretRef", "accepted: the keystore's password is written in `password` instead", nil, false},
	"certificate keystores.pkcs12.passwordSecretRef.name":                      {"keystores.pkcs12.passwordSecretRef", "accepted: the keystore's password is written in `password` instead", nil, false},
	"clusterissuer acme.solvers[].dns01.route53.secretAccessKeySecretRef.name": {"acme.solvers[].dns01.route53.secretAccessKeySecretRef", "accepted: Route 53 takes ambient credentials or `accessKeyID`", nil, false},
	"clusterissuer acme.solvers[].dns01.rfc2136.tsigSecretSecretRef.name":      {"acme.solvers[].dns01.rfc2136.tsigSecretSecretRef", "accepted: an RFC 2136 server is updated without TSIG", nil, false},
	"clusterissuer vault.auth.kubernetes.secretRef.name":                       {"vault.auth.kubernetes.secretRef", "accepted: Kubernetes auth takes `serviceAccountRef` instead", nil, false},
	"issuer acme.solvers[].dns01.route53.secretAccessKeySecretRef.name":        {"acme.solvers[].dns01.route53.secretAccessKeySecretRef", "accepted: Route 53 takes ambient credentials or `accessKeyID`", nil, false},
	"issuer acme.solvers[].dns01.rfc2136.tsigSecretSecretRef.name":             {"acme.solvers[].dns01.rfc2136.tsigSecretSecretRef", "accepted: an RFC 2136 server is updated without TSIG", nil, false},
	"issuer vault.auth.kubernetes.secretRef.name":                              {"vault.auth.kubernetes.secretRef", "accepted: Kubernetes auth takes `serviceAccountRef` instead", nil, false},
	"alertmanager clusterTLS.server.keySecret.key":                             webKeyFileInstead("clusterTLS.server"),
	"alertmanager web.tlsConfig.keySecret.key":                                 webKeyFileInstead("web.tlsConfig"),
	"prometheus web.tlsConfig.keySecret.key":                                   webKeyFileInstead("web.tlsConfig"),
	"thanosruler web.tlsConfig.keySecret.key":                                  webKeyFileInstead("web.tlsConfig"),
	"prometheus-probe prober.url":                                              {"prober", "refused: the API server refuses a prober without a url (minLength 1), and the type writes the prober whether or not it was authored; the kind's validate refuses a probe without one", nil, true},
}

// The basic auth blocks of the monitoring kinds: each one's username and
// password are answered for as basicAuthForced says.
func init() {
	for component, parents := range kindRequiredParents {
		for parent := range parents {
			kindRequiredForced[component+" "+parent+".key"] = kindForcedParent{parent, basicAuthForced, nil, true}
		}
	}
}

// basicAuthForced is the answer for a monitoring basic auth's username or
// password left out.
const basicAuthForced = "accepted: the API server takes the Secret key reference empty, and the Prometheus operator reads it and refuses the basic auth; the kind refuses it left out (kindRequiredParents)"

// webKeyFileInstead is the answer for the keySecret of the web server TLS
// configuration at at: an optional alternative to keyFile.
func webKeyFileInstead(at string) kindForcedParent {
	return kindForcedParent{at + ".keySecret", "accepted: the private key is read from `keyFile` instead", map[string]any{at + ".keyFile": "/etc/tls/tls.key"}, false}
}

// generatedKindRequiredLists are the required lists of the generated kinds,
// by component, as the kinds hold them.
var generatedKindRequiredLists = map[string]map[string]string{
	"certificate":              certificateKind.required,
	"clusterissuer":            clusterIssuerKind.required,
	"issuer":                   issuerKind.required,
	"cnpg-cluster":             cnpgClusterRequired,
	"cnpg-database":            cnpgDatabaseRequired,
	"cnpg-objectstore":         cnpgObjectStoreRequired,
	"cnpg-pooler":              cnpgPoolerRequired,
	"clusterexternalsecret":    clusterExternalSecretKind.required,
	"clustersecretstore":       clusterSecretStoreKind.required,
	"externalsecret":           externalSecretKind.required,
	"secretstore":              secretStoreKind.required,
	"alertmanager":             alertmanagerKind.generatedRequired,
	"podmonitor":               podMonitorKind.generatedRequired,
	"prometheus":               prometheusKind.generatedRequired,
	"prometheus-probe":         prometheusProbeKind.generatedRequired,
	"prometheusrule":           prometheusRuleKind.generatedRequired,
	"servicemonitor":           serviceMonitorKind.generatedRequired,
	"thanosruler":              thanosRulerKind.generatedRequired,
	"artifactgenerator":        artifactGeneratorKind.required,
	"bucket":                   bucketRequired,
	"fluxcd-alert":             fluxcdAlertKind.required,
	"fluxcd-kustomization":     fluxcdKustomizationRequired,
	"fluxcd-provider":          fluxcdProviderKind.required,
	"fluxcd-receiver":          fluxcdReceiverKind.required,
	"gitrepository":            gitRepositoryRequired,
	"helmchart":                helmChartRequired,
	"helmrelease":              helmReleaseRequired,
	"helmrepository":           helmRepositoryRequired,
	"imagepolicy":              imagePolicyKind.required,
	"imagerepository":          imageRepositoryKind.required,
	"imageupdateautomation":    imageUpdateAutomationKind.required,
	"ocirepository":            ociRepositoryRequired,
	"resourcesetinputprovider": resourceSetInputProviderKind.required,
	"backendtlspolicy":         backendTLSPolicyKind.required,
	"gateway":                  gatewayKind.required,
	"gatewayclass":             gatewayClassKind.required,
	"grpcroute":                grpcRouteRequired,
	"httproute":                httpRouteRequired,
	"listenerset":              listenerSetKind.required,
	"referencegrant":           referenceGrantKind.required,
	"tcproute":                 tcpRouteKind.required,
	"tlsroute":                 tlsRouteKind.required,
	"udproute":                 udpRouteKind.required,
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
// or requiredWrittenUndecided holds, and a field under a path
// requiredWrittenUnauthorable lists.
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
		if _, ok := requiredWrittenUndecided[key]; ok {
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
		f := kindRequiredForced[key]
		if f.parent == path || !underPath(path, f.parent) {
			t.Errorf("kindRequiredForced gives %s of %s the parent %s, which is not above it", path, component, f.parent)
		}
		switch {
		case strings.HasPrefix(f.answer, "refused: "):
			if !f.refused {
				t.Errorf("kindRequiredForced says the API server refuses %s of %s without %s, and the kind does not refuse it: the kind must", path, component, f.parent)
			}
		case !strings.HasPrefix(f.answer, "accepted: "):
			t.Errorf("kindRequiredForced answers %q for %s of %s; an answer starts with \"accepted: \" or \"refused: \"", f.answer, path, component)
		}
	}
	// A parent the kind refuses left out is one of those parents, and the
	// derivation does not list it.
	for _, component := range slices.Sorted(maps.Keys(kindRequiredParents)) {
		for _, parent := range slices.Sorted(maps.Keys(kindRequiredParents[component])) {
			if slices.Contains(derived[component], parent) {
				t.Errorf("kindRequiredParents lists %s of %s, which the derivation lists; drop it", parent, component)
			}
			if !slices.ContainsFunc(slices.Collect(maps.Keys(kindRequiredForced)), func(key string) bool {
				c, _, _ := strings.Cut(key, " ")
				return c == component && kindRequiredForced[key].parent == parent && kindRequiredForced[key].refused
			}) {
				t.Errorf("kindRequiredParents lists %s of %s, which no answer of kindRequiredForced refuses", parent, component)
			}
		}
	}
	for name, list := range map[string]map[string]string{"requiredWrittenExceptions": requiredWrittenExceptions, "requiredWrittenUndecided": requiredWrittenUndecided} {
		for _, key := range slices.Sorted(maps.Keys(list)) {
			component, path, _ := strings.Cut(key, " ")
			k, ok := requiredWrittenKind(component)
			if !ok || !slices.ContainsFunc(deriveRequiredWritten(t, k, k.schema(t)), func(d requiredWrittenField) bool { return d.path == path }) {
				t.Errorf("%s lists %s, which the derivation no longer lists; drop it", name, key)
			}
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
// (secretStoreUnappliedDefaults), saying its sentence, a parent the kind
// refuses left out (kindRequiredParents), saying its sentence, or a field
// under a path the kind refuses whatever is authored there
// (requiredWrittenUnauthorable). An entry in none fails, and so does a text
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
				if want, ok := kindRequiredParents[component][path]; ok {
					if says != want {
						t.Errorf("%s is a parent the kind refuses left out, and the list says %q of it, want %q", path, says, want)
					}
					continue
				}
				if _, ok := unauthorableAncestor(component, path); ok {
					if strings.TrimSpace(says) == "" {
						t.Errorf("%s says nothing of itself", path)
					}
					continue
				}
				t.Errorf("%s is in the required list, and it is neither generated nor in a held class (an unapplied default, a parent kindRequiredParents lists, a field under a path requiredWrittenUnauthorable lists)", path)
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

// generatedKindCRDFiles are the CRDs of the generated kinds whose linked module
// ships one and that certManagerKinds does not name, by component: the module
// and the file under its directory.
var generatedKindCRDFiles = map[string]struct{ modulePath, file string }{
	"cnpg-cluster":     {cnpgModulePath, cnpgCRDs + "clusters.yaml"},
	"cnpg-database":    {cnpgModulePath, cnpgCRDs + "databases.yaml"},
	"cnpg-objectstore": {barmanCloudModulePath, "config/crd/bases/barmancloud.cnpg.io_objectstores.yaml"},
	"cnpg-pooler":      {cnpgModulePath, cnpgCRDs + "poolers.yaml"},
	// The Gateway API's module ships the CRDs of both channels; the
	// experimental one holds every field the Go types do.
	"backendtlspolicy": {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "backendtlspolicies")},
	"gateway":          {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "gateways")},
	"gatewayclass":     {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "gatewayclasses")},
	"grpcroute":        {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "grpcroutes")},
	"httproute":        {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "httproutes")},
	"listenerset":      {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "listenersets")},
	"referencegrant":   {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "referencegrants")},
	"tcproute":         {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "tcproutes")},
	"tlsroute":         {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "tlsroutes")},
	"udproute":         {gatewayAPIModulePath, gatewayAPICRDFile("experimental", "udproutes")},
	// The Flux Operator's module ships its CRDs; the Flux controllers'
	// modules ship none.
	"resourcesetinputprovider": {"github.com/controlplaneio-fluxcd/flux-operator", fluxOperatorCRDs[resourceSetInputProviderType]},
}

// generatedKindsWithoutCRD are the generated kinds whose linked module ships no
// CRD and whose kindRequiredForced answers are recorded by hand from the
// operator's CRDs: the Prometheus operator's. A follow-up of
// go-kure/launcher#883 reads those CRDs and shows the answers on them.
var generatedKindsWithoutCRD = map[string]bool{
	"alertmanager":     true,
	"podmonitor":       true,
	"prometheus":       true,
	"prometheus-probe": true,
	"prometheusrule":   true,
	"servicemonitor":   true,
	"thanosruler":      true,
}

// generatedKindCRDOf reads the CRD of a generated kind whose linked module
// ships one, a cert-manager kind or one of generatedKindCRDFiles, nil for
// another component.
func generatedKindCRDOf(t *testing.T, component string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	var file string
	for _, kind := range certManagerKinds {
		if kind.component == component {
			file = certManagerCRD(t, kind.crd)
		}
	}
	if f, ok := generatedKindCRDFiles[component]; ok {
		file = linkedModuleDir(t, f.modulePath) + "/" + f.file
	}
	if file == "" {
		return nil
	}
	data, err := os.ReadFile(file)
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
	// An image catalog reference names the postgresql.cnpg.io group and a
	// catalog kind.
	"cnpg-cluster imageCatalogRef.major":         cnpgCatalogRefSample("imageCatalogRef"),
	"cnpg-cluster imageCatalogRef.name":          cnpgCatalogRefSample("imageCatalogRef"),
	"cnpg-pooler pgbouncer.imageCatalogRef.key":  cnpgCatalogRefSample("pgbouncer.imageCatalogRef"),
	"cnpg-pooler pgbouncer.imageCatalogRef.kind": cnpgCatalogRefSample("pgbouncer.imageCatalogRef"),
	"cnpg-pooler pgbouncer.imageCatalogRef.name": cnpgCatalogRefSample("pgbouncer.imageCatalogRef"),
	// A BackendTLSPolicy's validation names CA certificates or the well-known
	// ones, not both, and a subject alternative name of type Hostname holds a
	// hostname.
	"backendtlspolicy validation.caCertificateRefs[].group": {"validation.wellKnownCACertificates": nil},
	"backendtlspolicy validation.subjectAltNames[].type":    {"validation.subjectAltNames[].hostname": "payments.example.com"},
	// A listener with TLS speaks HTTPS.
	"gateway listeners[].tls.certificateRefs[].name":     {"listeners[].protocol": "HTTPS", "listeners[].port": 443},
	"listenerset listeners[].tls.certificateRefs[].name": {"listeners[].protocol": "HTTPS", "listeners[].port": 443},
	// A backend reference to a Service has a port, and a filter holds the
	// configuration of its type and of no other.
	"grpcroute rules[].backendRefs[].filters[].extensionRef.group":               gatewayFilterSample("ExtensionRef", nil),
	"grpcroute rules[].backendRefs[].filters[].requestHeaderModifier.add[].name": gatewayFilterSample("RequestHeaderModifier", nil),
	"grpcroute rules[].backendRefs[].filters[].requestMirror.backendRef":         gatewayFilterSample("RequestMirror", map[string]any{"requestMirror.backendRef.port": 8080}),
	"grpcroute rules[].backendRefs[].filters[].requestMirror.backendRef.name":    gatewayFilterSample("RequestMirror", map[string]any{"requestMirror.backendRef.port": 8080}),
	"grpcroute rules[].backendRefs[].filters[].requestMirror.fraction.numerator": gatewayFilterSample("RequestMirror", map[string]any{"requestMirror.backendRef.port": 8080}),
	"grpcroute rules[].backendRefs[].filters[].type":                             gatewayFilterSample("", map[string]any{"responseHeaderModifier.remove": []any{"x-debug"}}),
	"httproute rules[].backendRefs[].filters[].extensionRef.group":               gatewayFilterSample("ExtensionRef", nil),
	"httproute rules[].backendRefs[].filters[].externalAuth.backendRef":          gatewayFilterSample("ExternalAuth", map[string]any{"externalAuth.backendRef.port": 8080, "externalAuth.http": map[string]any{}}),
	"httproute rules[].backendRefs[].filters[].externalAuth.backendRef.name":     gatewayFilterSample("ExternalAuth", map[string]any{"externalAuth.backendRef.port": 8080, "externalAuth.http": map[string]any{}}),
	"httproute rules[].backendRefs[].filters[].requestHeaderModifier.add[].name": gatewayFilterSample("", nil),
	"httproute rules[].backendRefs[].filters[].requestMirror.backendRef":         gatewayFilterSample("RequestMirror", map[string]any{"requestMirror.backendRef.port": 8080}),
	"httproute rules[].backendRefs[].filters[].requestMirror.backendRef.name":    gatewayFilterSample("RequestMirror", map[string]any{"requestMirror.backendRef.port": 8080}),
	"httproute rules[].backendRefs[].filters[].requestMirror.fraction.numerator": gatewayFilterSample("RequestMirror", map[string]any{"requestMirror.backendRef.port": 8080}),
	"httproute rules[].backendRefs[].filters[].requestRedirect.path.type":        gatewayFilterSample("RequestRedirect", map[string]any{"requestRedirect.path.replaceFullPath": "/next"}),
	"httproute rules[].backendRefs[].filters[].type":                             gatewayFilterSample("", map[string]any{"requestHeaderModifier.remove": []any{"x-debug"}}),
	// A ResourceSetInputProvider takes a CA certificate on a Git provider, and
	// selectors on an ExternalArtifact one only.
	"resourcesetinputprovider certSecretRef.name":                 {"type": "GitHubBranch", "url": "https://github.com/example/app"},
	"resourcesetinputprovider selectors[].matchExpressions[].key": {"type": "ExternalArtifact"},
}

// gatewayFilterSample is a route rule's backend reference to a Service, with
// its port, and a filter of the given type (the filled one where it is
// empty) with the given fields under the filter.
func gatewayFilterSample(filterType string, fields map[string]any) map[string]any {
	const filter = "rules[].backendRefs[].filters[]."
	out := map[string]any{"rules[].backendRefs[].port": 8080}
	if filterType != "" {
		out[filter+"type"] = filterType
	}
	for at, value := range fields {
		out[filter+at] = value
	}
	return out
}

// generatedRefusedFirst are the generated paths whose omission a kind's own
// check refuses, by kind and path, each with the check: it runs before the
// generated refusal (the Flux kinds that decode their properties themselves
// read their list after validate), so the document is refused in the words it
// was refused with before the list was generated.
// TestKindComponents_GeneratedRequiredBothWays holds each to a refusal that
// names the path, and fails on an entry the generated refusal answers instead.
var generatedRefusedFirst = map[string]string{
	"bucket bucketName":                   "BucketConfig.validate",
	"bucket endpoint":                     "BucketConfig.validate",
	"fluxcd-kustomization sourceRef":      "FluxcdKustomizationConfig.validate",
	"fluxcd-kustomization sourceRef.kind": "FluxcdKustomizationConfig.validate",
	"fluxcd-kustomization sourceRef.name": "FluxcdKustomizationConfig.validate",
	"gitrepository url":                   "GitRepositoryConfig.validate",
	"helmchart chart":                     "HelmChartConfig.validate",
	"helmchart sourceRef":                 "HelmChartConfig.validate",
	"helmchart sourceRef.kind":            "HelmChartConfig.validate",
	"helmchart sourceRef.name":            "HelmChartConfig.validate",
	"helmrelease chart.spec":              "HelmReleaseConfig.validate",
	"helmrelease valuesFrom[].kind":       "HelmReleaseConfig.validate",
	"helmrelease valuesFrom[].name":       "HelmReleaseConfig.validate",
	"helmrepository url":                  "HelmRepositoryConfig.validate",
}

// cnpgCatalogRefSample is the group and the kind of an image catalog
// reference at the path at, which the CRDs' rules on the reference require.
func cnpgCatalogRefSample(at string) map[string]any {
	return map[string]any{at + ".apiGroup": "postgresql.cnpg.io", at + ".kind": "ImageCatalog"}
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
// required one. The External Secrets Operator's module, the Prometheus
// operator's and the Flux controllers' ship no CRD, and their kinds are
// answered by the kind only (a follow-up reads the Flux controllers' CRDs). Each
// answer kindRequiredForced records is shown by the kind (showForcedAnswer),
// and on the CRD where one is read: the control without the parent builds, and
// the API server accepts the object.
func TestKindComponents_GeneratedRequiredBothWays(t *testing.T) {
	for _, component := range slices.Sorted(maps.Keys(kindRequiredAPI)) {
		t.Run(component, func(t *testing.T) {
			k, ok := requiredWrittenKind(component)
			if !ok {
				t.Fatalf("%s is generated, and requiredWrittenKinds does not measure it", component)
			}
			if k.prefixes != nil {
				t.Fatalf("%s has prefixes, which this test does not measure", component)
			}
			schema := k.schema(t)
			// A path is measured on the base the pin measures it on
			// (pinKind.baseFor): altBase's under one of altPrefixes.
			var bases [2]map[string]any
			for i, b := range []map[string]any{k.base, k.altBase} {
				if i == 1 && b == nil {
					bases[1] = bases[0]
					break
				}
				bases[i] = pinCopy(b)
				pinFill(bases[i], schema, "")
			}
			base := bases[0]
			var create *crdCreate
			if crd := generatedKindCRDOf(t, component); crd != nil {
				create = crdCreateOf(t, crd, "v1")
			}
			list := generatedKindRequiredLists[component]
			generated := generatedRequiredPaths(t, k)
			for _, key := range slices.Sorted(maps.Keys(generatedRefusedFirst)) {
				c, path, _ := strings.Cut(key, " ")
				if c == component && !slices.ContainsFunc(generated, func(d requiredWrittenField) bool { return d.path == path }) {
					t.Errorf("generatedRefusedFirst names %s, which is not generated; drop the entry", path)
				}
			}
			answered, rows := map[reflect.Type]bool{}, 0
			for _, d := range generated {
				path := d.path
				with, err := pinControl(k, k.baseFor(path, bases), path, schema)
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
				// The control's first list element is [0], and the value of a map
				// it created is under the key pinParent gives it, k.
				at := strings.ReplaceAll(strings.ReplaceAll(path, "[]", "[0]"), "{}", "[k]")
				want := at + ": required (" + list[path] + ")"
				_, first := generatedRefusedFirst[component+" "+path]
				switch {
				case err == nil:
					t.Errorf("%s: the omission builds", path)
				case first && strings.Contains(err.Error(), want):
					t.Errorf("%s: the omission is refused by the generated list (%q), and generatedRefusedFirst says the kind's own check refuses it first; drop the entry", path, err)
				// A field the kind's own check requires is refused by that check,
				// which runs before the generated refusal and names the field in
				// the words it always did.
				case first && pinRefusalNames(at, err):
				case strings.Contains(err.Error(), want):
				// A struct the type writes whether or not it was authored is
				// refused by the kind's own check of its value, which runs before
				// the generated refusal and names the struct or the field of it
				// that check requires (a cluster reference's name, a TLS
				// configuration's certificate).
				case pinWrittenStruct(k, path) && pinRefusalNames(at, err):
				default:
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
				showForcedAnswer(t, k, base, schema, create, path, kindRequiredForced[key])
			}
			for _, key := range slices.Sorted(maps.Keys(requiredWrittenExceptions)) {
				c, path, _ := strings.Cut(key, " ")
				if c != component || create == nil {
					continue
				}
				showExceptionAccepted(t, k, base, schema, create, path)
			}
			if create != nil && rows == 0 {
				t.Errorf("no path of %s was answered by the API server", component)
			}
			t.Logf("%d API server rows", rows)
		})
	}
}

// showForcedAnswer shows the answer kindRequiredForced records for path, under
// the parent f names. The control that authors path builds; the same
// properties without the parent, and with what f authors instead, are refused
// by the kind naming the parent or a field under it where f says the kind
// refuses them, and build otherwise. Where the kind builds the object without
// the parent and its CRD is read, the API server accepts that object. A kind
// of generatedKindsWithoutCRD has its answers recorded by hand, and the kinds
// that refuse a parent are all of it, so no API server answer is shown for an
// object the kind refuses.
func showForcedAnswer(t *testing.T, k pinKind, base map[string]any, schema pinSchema, create *crdCreate, path string, f kindForcedParent) {
	t.Helper()
	what := fmt.Sprintf("%s %s, %s left out", k.component, path, f.parent)
	with, err := pinControl(k, base, path, schema)
	if err != nil {
		t.Errorf("%s: no control builds with the field authored: %v", path, err)
		return
	}
	if _, err := pinBuild(k, with); err != nil {
		t.Errorf("%s: the control is refused: %v", path, err)
		return
	}
	without := pinCopy(with)
	holder, name, ok := pinParent(without, f.parent, false)
	if !ok {
		t.Errorf("%s: the control does not author %s", path, f.parent)
		return
	}
	delete(holder, name)
	for _, at := range slices.Sorted(maps.Keys(f.instead)) {
		p, n, ok := pinParent(without, at, true)
		if !ok {
			t.Errorf("%s: %s cannot be placed in the properties", what, at)
			return
		}
		p[n] = f.instead[at]
	}
	obj, err := pinBuild(k, without)
	switch {
	case f.refused && err == nil:
		t.Errorf("%s: builds, and kindRequiredForced says the kind refuses it", what)
		return
	case f.refused && !pinRefusalNames(f.parent, err):
		t.Errorf("%s: refused with %q, want a refusal naming %s or a field under it", what, err, f.parent)
		return
	case !f.refused && err != nil:
		t.Errorf("%s: refused: %v; kindRequiredForced says the kind does not refuse it", what, err)
		return
	}
	switch {
	case generatedKindsWithoutCRD[k.component]:
		t.Logf("%s: the kind's answer is shown; the API server's, %q, is recorded by hand", what, f.answer)
	case create == nil:
		t.Errorf("%s %s: kindRequiredForced answers for a kind no CRD is read for", k.component, path)
	case f.refused:
		t.Errorf("%s: kindRequiredForced says the kind refuses it, and no API server answer is shown for a refused object of a kind whose CRD is read", what)
	default:
		create.create(t, obj).accepted(t, what)
	}
}

// showExceptionAccepted shows an omission requiredWrittenExceptions keeps on
// the kind's CRD: the control that authors the field builds and the API server
// accepts its object; the same properties without the field build, and the
// API server accepts that object too. Where an ancestor of the field is a
// struct the type writes whether or not it was authored, the properties
// without that ancestor are shown the same way, each ancestor in turn.
func showExceptionAccepted(t *testing.T, k pinKind, base map[string]any, schema pinSchema, create *crdCreate, path string) {
	t.Helper()
	with, err := pinControl(k, base, path, schema)
	if err != nil {
		t.Errorf("%s: no control builds with the field authored: %v", path, err)
		return
	}
	obj, err := pinBuild(k, with)
	if err != nil {
		t.Errorf("%s: the control is refused: %v", path, err)
		return
	}
	create.create(t, obj).accepted(t, k.component+" "+path+", authored")
	segments := strings.Split(path, ".")
	for depth := len(segments); depth > 1; depth-- {
		at := strings.Join(segments[:depth], ".")
		if depth < len(segments) && !pinWrittenStruct(k, at) {
			continue
		}
		without := pinCopy(with)
		parent, name, ok := pinParent(without, at, false)
		if !ok {
			t.Errorf("%s: the control does not author %s", path, at)
			continue
		}
		delete(parent, name)
		obj, err := pinBuild(k, without)
		if err != nil {
			t.Errorf("%s: the properties without %s are refused: %v; requiredWrittenExceptions keeps the omission", path, at, err)
			continue
		}
		answer := create.create(t, obj)
		answer.accepted(t, k.component+" "+path+", "+at+" left out")
		t.Logf("%s, %s left out: built %s; the API server accepts it", path, at, pinJSON(pinValueAt(obj, "spec."+path)))
	}
}

// pinWrittenStruct says the field of the kind's type at path is a struct that
// is no pointer: the type writes it whether or not it was authored.
func pinWrittenStruct(k pinKind, path string) bool {
	found := false
	walkKindFields(k.typ, func(kindField) bool { return false }, func(f kindField) {
		if f.path == path && f.field.Type.Kind() == reflect.Struct {
			found = true
		}
	})
	return found
}

// pinValueAt returns the value obj holds at path, through the first element
// of a list and the first value of a map; nil where it holds none.
func pinValueAt(obj map[string]any, path string) any {
	parent, name, ok := pinParent(obj, path, false)
	if !ok {
		return nil
	}
	return parent[name]
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
