package components

import (
	"encoding/json"
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
	"time"

	fluxoperatorv1 "github.com/controlplaneio-fluxcd/flux-operator/api/v1"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	notificationv1 "github.com/fluxcd/notification-controller/api/v1"
	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	"github.com/go-kure/kure/pkg/stack"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/internal/fluxduration"
)

// fluxMarkerModules are the modules whose Go source the tests below read: the
// API modules of the Flux controllers hold the Go types and ship no CRD, so
// the markers the CRDs are generated from are the source, as for the kinds of
// the Prometheus operator's API. The Flux Operator's module ships its CRDs
// beside its types; its markers are read the same way, so that one rule holds
// every Flux kind. Nothing here is held to the API server's own validator
// (crdCreate), which answers from a CRD. The shared type modules are meta, the
// kustomize one a Kustomization's patches and images are declared in, and the
// access-control one a HelmRepository's accessFrom is declared in.
var fluxMarkerModules = []string{
	"github.com/controlplaneio-fluxcd/flux-operator",
	"github.com/fluxcd/notification-controller/api",
	"github.com/fluxcd/image-reflector-controller/api",
	"github.com/fluxcd/image-automation-controller/api",
	"github.com/fluxcd/source-controller/api",
	"github.com/fluxcd/source-watcher/api/v2",
	"github.com/fluxcd/helm-controller/api",
	"github.com/fluxcd/kustomize-controller/api",
	"github.com/fluxcd/pkg/apis/meta",
	"github.com/fluxcd/pkg/apis/kustomize",
	"github.com/fluxcd/pkg/apis/acl",
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
	// defaulted is the kind's defaulted-zero list (policyFreeKind.defaultedZeros).
	defaulted map[string]string
}{
	{fluxcdAlertType, reflect.TypeFor[notificationv1beta3.AlertSpec](), fluxcdAlertKind.required, nil, durationForms(fluxcdAlertKind.durations), fluxcdAlertKind.defaultedZeros.fields},
	{fluxcdProviderType, reflect.TypeFor[notificationv1beta3.ProviderSpec](), fluxcdProviderKind.required, nil, durationForms(fluxcdProviderKind.durations), fluxcdProviderKind.defaultedZeros.fields},
	{fluxcdReceiverType, reflect.TypeFor[notificationv1.ReceiverSpec](), fluxcdReceiverKind.required, nil, durationForms(fluxcdReceiverKind.durations), fluxcdReceiverKind.defaultedZeros.fields},
	{imagePolicyType, reflect.TypeFor[imagev1.ImagePolicySpec](), imagePolicyKind.required, nil, durationForms(imagePolicyKind.durations), imagePolicyKind.defaultedZeros.fields},
	{imageRepositoryType, reflect.TypeFor[imagev1.ImageRepositorySpec](), imageRepositoryKind.required, nil, durationForms(imageRepositoryKind.durations), imageRepositoryKind.defaultedZeros.fields},
	{imageUpdateAutomationType, reflect.TypeFor[autov1.ImageUpdateAutomationSpec](), imageUpdateAutomationKind.required, nil, durationForms(imageUpdateAutomationKind.durations), imageUpdateAutomationKind.defaultedZeros.fields},
	{artifactGeneratorType, reflect.TypeFor[swv1beta1.ArtifactGeneratorSpec](), artifactGeneratorKind.required, nil, durationForms(artifactGeneratorKind.durations), artifactGeneratorKind.defaultedZeros.fields},
	{resourceSetInputProviderType, reflect.TypeFor[fluxoperatorv1.ResourceSetInputProviderSpec](), resourceSetInputProviderKind.required, nil, durationForms(resourceSetInputProviderKind.durations), resourceSetInputProviderKind.defaultedZeros.fields},
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

// fluxOperatorUnchecked is why the kind of the Flux Operator's API checks no
// expression rule although its module ships the CRD: it is held to the rule of
// the other Flux kinds, which leave every rule to the API server.
// TestFluxOperatorKinds_ExpressionRulesMatchCRD holds its list to that CRD.
const fluxOperatorUnchecked = "the kind leaves every expression rule to the API server on purpose, as the other Flux kinds do; the module ships the CRD, which the list is held to, and no check is made against it"

// fluxOperatorCRDs are the CRDs of the Flux Operator's kinds, by component: a
// path under the directory of the linked module.
var fluxOperatorCRDs = map[string]string{
	resourceSetInputProviderType: "config/crd/bases/fluxcd.controlplane.io_resourcesetinputproviders.yaml",
}

// fluxRulesLeft lists, per kind, every expression rule the API declares on
// what the kind decodes, as "<path>: <rule>", with what the kind leaves to the
// API server and why. A kind with no entry decodes a type that declares none.
// TestFluxKinds_ExpressionRules holds the table to the markers of the linked
// source, in both directions.
var fluxRulesLeft = map[string]map[string]string{
	fluxcdProviderType: {
		"spec: self.type == 'github' || self.type == 'gitlab' || self.type == 'gitea' || self.type == 'bitbucketserver' || self.type == 'bitbucket' || self.type == 'azuredevops' || !has(self.commitStatusExpr)": "a `commitStatusExpr` on a provider of another type than those six builds and is refused at apply: " + fluxNoCRD,
	},
	fluxcdReceiverType: {
		"spec: self.type != 'generic-oidc' || (has(self.oidcProviders) && size(self.oidcProviders) > 0)": "a `generic-oidc` receiver without an OIDC provider builds and is refused at apply: " + fluxNoCRD,
		"spec: self.type == 'generic-oidc' || !has(self.oidcProviders) || size(self.oidcProviders) == 0": "`oidcProviders` on a receiver of another type than `generic-oidc` builds and is refused at apply: " + fluxNoCRD,
		"spec: self.type != 'generic-oidc' || !has(self.secretRef)":                                      "a `secretRef` on a `generic-oidc` receiver builds and is refused at apply: " + fluxNoCRD,
		"spec: self.type == 'generic-oidc' || has(self.secretRef)":                                       "a receiver of another type than `generic-oidc` without a `secretRef` builds and is refused at apply: " + fluxNoCRD,
	},
	imagePolicyType: {
		"spec: !has(self.interval) || (has(self.digestReflectionPolicy) && self.digestReflectionPolicy == 'Always')": "an `interval` without `digestReflectionPolicy: Always` builds and is refused at apply: " + fluxNoCRD,
		"spec: has(self.interval) || !has(self.digestReflectionPolicy) || self.digestReflectionPolicy != 'Always'":   "`digestReflectionPolicy: Always` without an `interval` builds and is refused at apply: " + fluxNoCRD,
	},
	artifactGeneratorType: {
		`spec: has(self.pathPattern) && size(self.pathPattern) > 0 || self.artifacts.all(a, a.name.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$'))`: "without a `pathPattern`, an artifact whose `name` is no Kubernetes object name builds and is refused at apply: " + fluxNoCRD,
	},
	resourceSetInputProviderType: {
		"spec: self.type != 'Static' || !has(self.url)":                                                                                                                                               "a `url` on a `Static` provider builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: self.type != 'ExternalArtifact' || !has(self.url)":                                                                                                                                     "a `url` on an `ExternalArtifact` provider builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: self.type == 'Static' || self.type == 'ExternalArtifact' || has(self.url)":                                                                                                             "a provider of another type than `Static` or `ExternalArtifact` without a `url` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !self.type.startsWith('Git') || self.url.startsWith('http')":                                                                                                                           "a Git provider whose `url` is not `http(s)://` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !self.type.startsWith('AzureDevOps') || self.url.startsWith('http://') || self.url.startsWith('https://')":                                                                             "an AzureDevOps provider whose `url` is not `http(s)://` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !self.type.startsWith('AWSCodeCommit') || self.url.startsWith('https://')":                                                                                                             "an AWSCodeCommit provider whose `url` is not `https://` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !self.type.endsWith('ArtifactTag') || self.url.startsWith('oci')":                                                                                                                      "an OCI provider whose `url` is not `oci://` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !self.type.endsWith('ArtifactTag') || !self.url.startsWith('oci://') || self.url.substring(6).contains('/')":                                                                           "an OCI provider whose `url` names no repository after its host builds and is refused at apply, unless an allowed-registries policy refuses it first (enforceResourceSetInputProviderURL): " + fluxOperatorUnchecked,
		"spec: self.type != 'ExternalService' || self.url.startsWith('http')":                                                                                                                         "an `ExternalService` provider whose `url` is not `http(s)://` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !has(self.insecure) || !self.insecure || self.type == 'ExternalService' || self.type == 'OCIArtifactTag'":                                                                              "`insecure: true` on a provider of another type than `ExternalService` or `OCIArtifactTag` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: self.type != 'ExternalService' || !self.url.startsWith('http://') || (has(self.insecure) && self.insecure)":                                                                            "an `ExternalService` provider with an `http://` url and without `insecure: true` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !has(self.serviceAccountName) || self.type.startsWith('AzureDevOps') || self.type.startsWith('AWSCodeCommit') || self.type.endsWith('ArtifactTag') || self.type == 'ExternalArtifact'": "a `serviceAccountName` on a provider of another type than those builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !has(self.certSecretRef) || !(self.type == 'Static' || self.type == 'ExternalArtifact' || self.type.startsWith('AzureDevOps') || self.type.startsWith('AWSCodeCommit') || (self.type.endsWith('ArtifactTag') && self.type != 'OCIArtifactTag'))": "a `certSecretRef` on a provider of one of those types builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: !has(self.secretRef) || !(self.type == 'Static' || self.type == 'ExternalArtifact' || self.type.startsWith('AWSCodeCommit') || (self.type.endsWith('ArtifactTag') && self.type != 'OCIArtifactTag'))":                                            "a `secretRef` on a provider of one of those types builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: self.type != 'ExternalArtifact' || (has(self.selectors) && size(self.selectors) > 0)":                                                                                                                                                            "an `ExternalArtifact` provider without a selector builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec: self.type == 'ExternalArtifact' || !has(self.selectors)":                                "`selectors` on a provider of another type than `ExternalArtifact` builds and is refused at apply: " + fluxOperatorUnchecked,
		"spec.selectors[]: !has(self.name) || (!has(self.matchLabels) && !has(self.matchExpressions))": "a selector with a `name` and labels or expressions builds and is refused at apply: " + fluxOperatorUnchecked,
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
// types. The key and the operator of a match expression are fields of a
// Kubernetes type, which carries no +required marker: they are read from the
// source of metav1.LabelSelectorRequirement by the schema generators' rule
// (markerAPISource), as TestMonitoringKinds_RequiredMatchMarkers reads them.
// No other field of a Kubernetes type these specs embed is derived.
func TestFluxKinds_RequiredMatchMarkers(t *testing.T) {
	flux := linkedFieldMarkers(t, fluxMarkerModules)
	fluxMarkersRead(t, flux)
	src := markerAPISource(t)
	expression := reflect.TypeFor[metav1.LabelSelectorRequirement]()
	markers := func(f kindField) (fieldMarkers, bool) {
		if f.owner == expression {
			required := src.required(f)
			return fieldMarkers{required: required, optional: !required}, src.known(f)
		}
		return flux(f)
	}
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

// TestFluxKinds_DefaultedZeros is TestMonitoringKinds_DefaultedZeros for the
// kinds of the Flux APIs: a number, a boolean or a string these kinds decode
// that is omitted when zero and that the API defaults to something else must
// be in the kind's defaulted-zero list (policyFreeKind.defaultedZeros) with
// that default, and the list must hold nothing else. The default is the
// field's marker, read from the linked modules' source, the Kubernetes types
// these specs embed included. A field of that shape on a type whose source is
// not read fails too.
func TestFluxKinds_DefaultedZeros(t *testing.T) {
	markers := linkedFieldMarkers(t, markerModules)
	fluxMarkersRead(t, markers)
	walked := map[string]bool{}
	for _, kind := range fluxKindRows {
		derived := map[string]string{}
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
			case m.hasDefault && !f.defaultIsZero(m.def):
				derived[f.path] = f.defaultLiteral(m.def)
			}
		})
		compareDefaultedZeros(t, kind.component, derived, kind.defaulted)
	}
	// The list refuses: an authored "" on a listed field does not reach the
	// object.
	_, err := fluxcdAlertKind.config(&oam.Component{Name: "alert", Properties: map[string]any{"eventSeverity": ""}})
	const want = `eventSeverity: "" cannot be carried by the Flux API types (the field is omitted when zero, so the API server would apply its default "info")`
	if err == nil || err.Error() != want {
		t.Errorf("an authored empty string on a defaulted field: got %v, want %s", err, want)
	}
	// A string default of another API, and a field under a list: the authored
	// spelling of the key does not matter, and an omitted or set value passes.
	repo := func(extra map[string]any) map[string]any {
		props := map[string]any{"image": "ghcr.io/org/app", "interval": "5m"}
		maps.Copy(props, extra)
		return props
	}
	schedules := func(extra map[string]any) map[string]any {
		second := map[string]any{"cron": "0 0 * * *"}
		maps.Copy(second, extra)
		return map[string]any{"type": "Static", "schedule": []any{map[string]any{"cron": "0 * * * *"}, second}}
	}
	type configurer interface {
		config(*oam.Component) (stack.ApplicationConfig, error)
	}
	for _, c := range []struct {
		kind  configurer
		props map[string]any
		want  string
	}{
		{imageRepositoryKind, repo(map[string]any{"provider": ""}), `provider: "" cannot be carried by the Flux API types (the field is omitted when zero, so the API server would apply its default "generic")`},
		{imageRepositoryKind, repo(map[string]any{"Provider": ""}), `Provider: "" cannot be carried by the Flux API types (the field is omitted when zero, so the API server would apply its default "generic")`},
		{imageRepositoryKind, repo(map[string]any{"provider": "aws"}), ""},
		{imageRepositoryKind, repo(nil), ""},
		{resourceSetInputProviderKind, schedules(map[string]any{"timeZone": ""}), `schedule[1].timeZone: "" cannot be carried by the Flux Operator API types (the field is omitted when zero, so the API server would apply its default "UTC")`},
		{resourceSetInputProviderKind, schedules(map[string]any{"TimeZone": ""}), `schedule[1].TimeZone: "" cannot be carried by the Flux Operator API types (the field is omitted when zero, so the API server would apply its default "UTC")`},
		{resourceSetInputProviderKind, schedules(map[string]any{"timeZone": "Europe/Brussels"}), ""},
		{resourceSetInputProviderKind, schedules(nil), ""},
	} {
		_, err := c.kind.config(&oam.Component{Name: "c", Properties: c.props})
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: unexpected error %v", c.props, err)
		case c.want != "" && (err == nil || err.Error() != c.want):
			t.Errorf("%v: got %v, want %s", c.props, err, c.want)
		}
	}
	for _, at := range []string{"AlertSpec: suspend", "ImagePolicySpec: suspend", "ImageUpdateAutomationSpec: suspend", "ResourceSetInputProviderSpec: filter.limit"} {
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
// validator: that takes a CRD (crdCreate, go-kure/launcher#874), and the
// modules of the Flux controllers ship none. The Flux Operator's module ships
// its CRDs; its kind leaves the rules on purpose, and
// TestFluxOperatorKinds_ExpressionRulesMatchCRD holds its list to the CRD
// too. The table is what the README's "not checked" lists are held to say.
func TestFluxKinds_ExpressionRules(t *testing.T) {
	// A ResourceSetInputProvider's defaultValues holds apiextensions JSON
	// values, whose source is read for the rules they could declare.
	lines := linkedMarkerLines(t, append(slices.Clone(markerModules), "k8s.io/apiextensions-apiserver"))
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
			defaulted map[string]string
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

// TestFluxOperatorKinds_ExpressionRulesMatchCRD holds fluxRulesLeft of each
// kind of the Flux Operator's API to the x-kubernetes-validations of the CRD
// its module ships, in both directions: a rule the CRD adds, drops or rewords
// fails here until the list says so. The kind checks none of them, on purpose
// (fluxOperatorUnchecked); the CRD is read only for the list. A rule on the
// root of the object could reach the spec and fails too; the status is the
// operator's and is not read.
func TestFluxOperatorKinds_ExpressionRulesMatchCRD(t *testing.T) {
	const module = "github.com/controlplaneio-fluxcd/flux-operator"
	total := 0
	for component, file := range fluxOperatorCRDs {
		t.Run(component, func(t *testing.T) {
			path := filepath.Join(linkedModuleDir(t, module), filepath.FromSlash(file))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the CRD: %v", err)
			}
			var crd apiextensionsv1.CustomResourceDefinition
			if err := yaml.Unmarshal(data, &crd); err != nil {
				t.Fatalf("decode the CRD %s: %v", file, err)
			}
			if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Name != fluxoperatorv1.GroupVersion.Version || crd.Spec.Versions[0].Schema == nil || crd.Spec.Versions[0].Schema.OpenAPIV3Schema == nil {
				t.Fatalf("%s: want the one version %s, with a schema; update this test", file, fluxoperatorv1.GroupVersion.Version)
			}
			var declared []string
			walkCiliumBGPSchema(*crd.Spec.Versions[0].Schema.OpenAPIV3Schema, "", func(at string, s apiextensionsv1.JSONSchemaProps) {
				if at != "" && at != "spec" && !strings.HasPrefix(at, "spec.") {
					return
				}
				for _, rule := range s.XValidations {
					if at == "" {
						t.Errorf("the CRD declares the rule %q on the root of the object; classify it", rule.Rule)
						continue
					}
					declared = append(declared, at+": "+rule.Rule)
				}
			})
			slices.Sort(declared)
			total += len(declared)
			left := fluxRulesLeft[component]
			if listed := slices.Sorted(maps.Keys(left)); !slices.Equal(declared, listed) {
				t.Errorf("the CRD declares the rules %q\nfluxRulesLeft lists     %q", declared, listed)
			}
			for rule, why := range left {
				if !strings.HasSuffix(why, fluxOperatorUnchecked) {
					t.Errorf("rule %q is listed without the reason the kind leaves it to the API server", rule)
				}
			}
		})
	}
	// Vacuity guard: the seventeen rules of a ResourceSetInputProvider are read.
	if total != 17 {
		t.Errorf("the CRDs declare %d expression rules on the specs, want 17", total)
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
// under a map, which the list cannot name, one under a list whose pattern
// takes no h, whose decoded values the object would carry in another form than
// the pattern's (fluxDurationField), and one whose pattern is neither form's
// fail, so a dependency bump that adds a duration field fails here until the
// kind checks it.
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
				if strings.Contains(f.path, "{") {
					t.Errorf("%s is a duration under a map, which the kind's duration list cannot name", f.path)
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
					if strings.Contains(f.path, "[") && form != fluxduration.Interval {
						t.Errorf("%s is a duration under a list whose pattern takes no h, which the object would carry in another form than the pattern's", f.path)
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

// TestEmitFluxKind holds the writer of a Flux kind's object to two things.
// Under a form with h the object goes out as the typed one, untouched, as it
// did before the writer: every duration of the four kinds written before it
// takes h, so their output is unchanged. Under a form without h a duration of
// an hour or more goes out in the form's text, as emitFluxSource writes a
// source's timeout.
func TestEmitFluxKind(t *testing.T) {
	const long = 90 * time.Minute
	t.Run("a form with h emits the typed object", func(t *testing.T) {
		durations := 0
		for component, forms := range map[string]map[string]fluxduration.Form{
			fluxcdAlertType:           durationForms(fluxcdAlertKind.durations),
			imagePolicyType:           durationForms(imagePolicyKind.durations),
			imageUpdateAutomationType: durationForms(imageUpdateAutomationKind.durations),
			artifactGeneratorType:     durationForms(artifactGeneratorKind.durations),
		} {
			for path, form := range forms {
				durations++
				if got := form.Format(long); got != long.String() {
					t.Errorf("%s: %s of %s is written as %q, not as the type writes it (%q): the kind's output changed", component, path, long, got, long)
				}
			}
		}
		// Vacuity guard: an ImagePolicy and an ImageUpdateAutomation each have an
		// interval.
		if durations < 2 {
			t.Fatalf("the four kinds list %d durations, want at least 2", durations)
		}

		policy := &imagev1.ImagePolicy{Spec: imagev1.ImagePolicySpec{Interval: &metav1.Duration{Duration: long}}}
		objs, err := emitFluxKind(imagePolicyType, policy, &policy.Spec, imagePolicyKind.durations, oam.ObjectMetadata{})
		if err != nil || len(objs) != 1 {
			t.Fatalf("emitFluxKind = %d objects, %v; want one", len(objs), err)
		}
		if got, ok := (*objs[0]).(*imagev1.ImagePolicy); !ok || got != policy {
			t.Errorf("an imagepolicy with an interval of %s goes out as a %T, want the typed object it was given", long, *objs[0])
		}

		automation := &autov1.ImageUpdateAutomation{Spec: autov1.ImageUpdateAutomationSpec{Interval: metav1.Duration{Duration: long}}}
		objs, err = emitFluxKind(imageUpdateAutomationType, automation, &automation.Spec, imageUpdateAutomationKind.durations, oam.ObjectMetadata{})
		if err != nil || len(objs) != 1 {
			t.Fatalf("emitFluxKind = %d objects, %v; want one", len(objs), err)
		}
		if got, ok := (*objs[0]).(*autov1.ImageUpdateAutomation); !ok || got != automation {
			t.Errorf("an imageupdateautomation with an interval of %s goes out as a %T, want the typed object it was given", long, *objs[0])
		}
	})

	t.Run("a form without h emits the form's text", func(t *testing.T) {
		for _, timeout := range []time.Duration{30 * time.Second, long} {
			repo := func() *sourcev1.OCIRepository {
				return &sourcev1.OCIRepository{Spec: sourcev1.OCIRepositorySpec{
					Interval: metav1.Duration{Duration: 10 * time.Minute},
					Timeout:  &metav1.Duration{Duration: timeout},
				}}
			}
			encode := func(objs []*client.Object, err error) string {
				t.Helper()
				if err != nil || len(objs) != 1 {
					t.Fatalf("timeout %s: %d objects, %v; want one", timeout, len(objs), err)
				}
				data, err := json.Marshal(*objs[0])
				if err != nil {
					t.Fatalf("timeout %s: encode: %v", timeout, err)
				}
				return string(data)
			}
			source, kind := repo(), repo()
			want := encode(emitFluxSource("ocirepository", source, source.Spec.Timeout, oam.ObjectMetadata{}))
			got := encode(emitFluxKind("ocirepository", kind, &kind.Spec, ociRepositoryDurations, oam.ObjectMetadata{}))
			if got != want {
				t.Errorf("timeout %s:\n got %s\nwant %s (as emitFluxSource writes it)", timeout, got, want)
			}
			if text := fluxduration.SourceTimeout.Format(timeout); !strings.Contains(got, `"timeout":"`+text+`"`) {
				t.Errorf("timeout %s: the object does not carry spec.timeout %q: %s", timeout, text, got)
			}
		}
		// Vacuity guard: the long timeout is one the type would write with an h.
		if fluxduration.SourceTimeout.Format(long) == long.String() {
			t.Fatalf("%s is written the same under both forms; the case shows nothing", long)
		}
	})
}

// TestFluxDurations_ThroughAList holds a duration whose path names a list
// element: the authored text of every item is checked, under every spelling of
// the list's key, and the decoded values are passed over, by the check and by
// the writer, which emits the typed object.
func TestFluxDurations_ThroughAList(t *testing.T) {
	type window struct {
		Window metav1.Duration `json:"window"`
	}
	type spec struct {
		Schedule []window `json:"schedule"`
	}
	fields := []fluxDurationField[spec]{{path: []string{"schedule[]", "window"}, form: fluxduration.Interval}}
	for name, tc := range map[string]struct {
		props   map[string]any
		refused bool
	}{
		"every item valid":        {props: map[string]any{"schedule": []any{map[string]any{"window": "1h"}, map[string]any{"window": "30m"}}}},
		"no window":               {props: map[string]any{"schedule": []any{map[string]any{"cron": "* * * * *"}}}},
		"the second item invalid": {props: map[string]any{"schedule": []any{map[string]any{"window": "1h"}, map[string]any{"window": "1d"}}}, refused: true},
		"another spelling":        {props: map[string]any{"Schedule": []any{map[string]any{"Window": "1d"}}}, refused: true},
		"below a millisecond":     {props: map[string]any{"schedule": []any{map[string]any{"window": "500us"}}}, refused: true},
	} {
		err := checkAuthoredFluxDurations("kind", tc.props, fields)
		switch {
		case tc.refused && (err == nil || !strings.Contains(err.Error(), "schedule[].window")):
			t.Errorf("%s: err = %v, want a refusal naming schedule[].window", name, err)
		case !tc.refused && err != nil:
			t.Errorf("%s: err = %v, want none", name, err)
		}
	}

	decoded := &spec{Schedule: []window{{Window: metav1.Duration{Duration: -time.Hour}}}}
	if err := checkFluxDurations("kind", decoded, fields); err != nil {
		t.Errorf("checkFluxDurations = %v; a path through a list has no get and is passed over", err)
	}
	policy := &imagev1.ImagePolicy{}
	objs, err := emitFluxKind("kind", policy, decoded, fields, oam.ObjectMetadata{})
	if err != nil || len(objs) != 1 {
		t.Fatalf("emitFluxKind = %d objects, %v; want one", len(objs), err)
	}
	if got, ok := (*objs[0]).(*imagev1.ImagePolicy); !ok || got != policy {
		t.Errorf("emitFluxKind wrote a %T, want the typed object it was given", *objs[0])
	}
}

// TestFluxKinds_UnheldHaveNoEnforce: the kinds no dimension of the environment
// policy reaches have no enforce, so the ApplyPolicy of their config checks
// nothing (TestFluxKinds_UnheldPassEveryPolicy shows it on their fixtures). A
// kind that comes to name a host the policy holds leaves this list and says in
// its own tests what the policy refuses of it.
func TestFluxKinds_UnheldHaveNoEnforce(t *testing.T) {
	for component, held := range map[string]bool{
		fluxcdAlertType:           fluxcdAlertKind.enforce != nil,
		fluxcdReceiverType:        fluxcdReceiverKind.enforce != nil,
		imagePolicyType:           imagePolicyKind.enforce != nil,
		imageUpdateAutomationType: imageUpdateAutomationKind.enforce != nil,
		artifactGeneratorType:     artifactGeneratorKind.enforce != nil,
	} {
		if held {
			t.Errorf("%s has an enforce: the environment policy now refuses something of it", component)
		}
	}
}
