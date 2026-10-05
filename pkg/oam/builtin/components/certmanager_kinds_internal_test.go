package components

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// certManagerModulePath is the module whose CRDs the tests below read:
// cert-manager's API types publish no field descriptions, and the module ships
// the CRDs its chart installs.
const certManagerModulePath = "github.com/cert-manager/cert-manager"

// certManagerKinds lists the kind components of that API with the CRD of the
// object each emits, the spec type it decodes into and its required list.
var certManagerKinds = []struct {
	component string
	crd       string
	typ       reflect.Type
	required  map[string]string
}{
	{"issuer", "cert-manager.io_issuers.yaml", reflect.TypeFor[certv1.IssuerSpec](), issuerKind.required},
	{"clusterissuer", "cert-manager.io_clusterissuers.yaml", reflect.TypeFor[certv1.IssuerSpec](), clusterIssuerKind.required},
	{"certificate", "cert-manager.io_certificates.yaml", reflect.TypeFor[certv1.CertificateSpec](), certificateKind.required},
}

// certManagerCRD is the path of one CRD of the linked module.
func certManagerCRD(t *testing.T, file string) string {
	t.Helper()
	return filepath.Join(linkedModuleDir(t, certManagerModulePath), "deploy", "crds", file)
}

// crdSpecProperties reads a single-version CRD file, as crdSpecScalarDefaults
// does, and returns the schema of every property under spec, keyed by json
// path with [] for a list element and {} for a map value, and the paths the
// schema of their parent requires.
func crdSpecProperties(t *testing.T, file string) (map[string]apiextensionsv1.JSONSchemaProps, map[string]bool) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", file, err)
	}
	if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Name != "v1" || crd.Spec.Versions[0].Schema == nil {
		t.Fatalf("%s does not serve exactly one schema-bearing v1 version; update this test", file)
	}
	spec, ok := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		t.Fatalf("%s has no spec property", file)
	}
	props, required := map[string]apiextensionsv1.JSONSchemaProps{}, map[string]bool{}
	var walk func(s apiextensionsv1.JSONSchemaProps, path string)
	walk = func(s apiextensionsv1.JSONSchemaProps, path string) {
		for name, prop := range s.Properties {
			child := name
			if path != "" {
				child = path + "." + name
			}
			props[child] = prop
			if slices.Contains(s.Required, name) {
				required[child] = true
			}
			walk(prop, child)
		}
		if s.Items != nil && s.Items.Schema != nil {
			walk(*s.Items.Schema, path+"[]")
		}
		if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			walk(*s.AdditionalProperties.Schema, path+"{}")
		}
	}
	walk(spec, "")
	return props, required
}

// crdRefusesZero says why the schema of one property refuses the zero value of
// the Go field that is written into it, "" when it does not: an empty string
// against an enumeration, a minimum length or a pattern, a 0 against a minimum
// or an enumeration. A struct's zero holds only what its own fields write, and
// a pointer's is a null the API server drops.
func crdRefusesZero(s apiextensionsv1.JSONSchemaProps, typ reflect.Type) string {
	var zero string
	switch typ.Kind() {
	case reflect.String:
		zero = `""`
		if s.MinLength != nil && *s.MinLength > 0 {
			return fmt.Sprintf("its minimum length is %d", *s.MinLength)
		}
		if s.Pattern != "" && !regexp.MustCompile(s.Pattern).MatchString("") {
			return "its pattern " + s.Pattern + " does not match the empty string"
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		zero = "0"
		if s.Minimum != nil && (*s.Minimum > 0 || *s.Minimum == 0 && s.ExclusiveMinimum) {
			return fmt.Sprintf("its minimum is %v", *s.Minimum)
		}
	default:
		return ""
	}
	if len(s.Enum) == 0 {
		return ""
	}
	for _, value := range s.Enum {
		if strings.TrimSpace(string(value.Raw)) == zero {
			return ""
		}
	}
	return "its enumeration does not hold " + zero
}

// TestCertManagerKinds_RequiredMatchCRD derives, from the CRDs of the linked
// module, the fields of each kind that the API requires and the type would
// write unauthored, and holds the kind's required list to them. Such a field
// is in the `required` list of its parent's schema and is encoded when nothing
// was decoded into it: a struct that is no pointer, or any field without
// omitempty. A dependency bump that adds, drops or moves one fails here,
// naming it.
//
// The list follows what was authored, so a required field under a struct the
// author left out is not asked for. Where the type writes that struct anyway,
// the field reaches the API server as its zero value with nobody having
// authored it, and so does every field the type writes that the API does not
// require. The test holds each of those to its schema: none may be refused
// empty, or the kind would emit an object the API server refuses for a field
// the author never wrote.
//
// Only the fields of cert-manager's own types are derived. A field of a
// Kubernetes or Gateway API type these specs embed (the terms of an affinity,
// the name of a parent reference) is not, and no kind refuses its omission.
func TestCertManagerKinds_RequiredMatchCRD(t *testing.T) {
	for _, kind := range certManagerKinds {
		t.Run(kind.component, func(t *testing.T) {
			props, required := crdSpecProperties(t, certManagerCRD(t, kind.crd))
			listed, empty := map[string]bool{}, map[string]bool{}
			fields := 0
			walkKindFields(kind.typ, func(f kindField) bool { return required[f.path] }, func(f kindField) {
				if !strings.HasPrefix(f.owner.PkgPath(), certManagerModulePath+"/") {
					return
				}
				fields++
				prop, ok := props[f.path]
				if !ok {
					t.Errorf("%s (%s.%s) is no property of the CRD; the paths are keyed wrongly", f.path, f.owner, f.field.Name)
					return
				}
				if !f.writtenUnauthored() {
					return
				}
				if required[f.path] {
					if strings.Contains(f.path, "{}") {
						t.Errorf("%s is required under a map value, which a required list cannot name", f.path)
					}
					listed[f.path] = true
					if !f.forced {
						return
					}
				}
				empty[f.path] = true
				if why := crdRefusesZero(prop, f.field.Type); why != "" {
					t.Errorf("%s is written empty where the author wrote nothing, and the API refuses that: %s; the kind must refuse the omission or fill the field", f.path, why)
				}
			})
			if fields == 0 {
				t.Fatalf("the walk found no field of %s", kind.typ)
			}
			t.Logf("walked %d fields of cert-manager's types; required and written unauthored: %v; written empty unauthored: %v",
				fields, slices.Sorted(maps.Keys(listed)), slices.Sorted(maps.Keys(empty)))
			if got, want := slices.Sorted(maps.Keys(kind.required)), slices.Sorted(maps.Keys(listed)); !slices.Equal(got, want) {
				t.Errorf("required list = %v\nthe CRD requires %v", got, want)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}
		})
	}
}

// TestCrdRefusesZero: the zero of a string or a number is refused by a minimum
// length, a pattern, a minimum or an enumeration that excludes it, and by
// nothing else.
func TestCrdRefusesZero(t *testing.T) {
	one, oneF, zeroF := int64(1), float64(1), float64(0)
	str, num := reflect.TypeFor[string](), reflect.TypeFor[int32]()
	raw := func(values ...string) []apiextensionsv1.JSON {
		out := make([]apiextensionsv1.JSON, 0, len(values))
		for _, v := range values {
			out = append(out, apiextensionsv1.JSON{Raw: []byte(v)})
		}
		return out
	}
	for name, tc := range map[string]struct {
		schema  apiextensionsv1.JSONSchemaProps
		typ     reflect.Type
		refused bool
	}{
		"a free string":              {apiextensionsv1.JSONSchemaProps{}, str, false},
		"a minimum length":           {apiextensionsv1.JSONSchemaProps{MinLength: &one}, str, true},
		"a pattern that excludes it": {apiextensionsv1.JSONSchemaProps{Pattern: "^[a-z]+$"}, str, true},
		"a pattern that holds it":    {apiextensionsv1.JSONSchemaProps{Pattern: "^$|^[a-z]+$"}, str, false},
		"an enumeration without it":  {apiextensionsv1.JSONSchemaProps{Enum: raw(`"DER"`, `"CombinedPEM"`)}, str, true},
		"an enumeration with it":     {apiextensionsv1.JSONSchemaProps{Enum: raw(`"A"`, `""`)}, str, false},
		"a free number":              {apiextensionsv1.JSONSchemaProps{}, num, false},
		"a minimum above it":         {apiextensionsv1.JSONSchemaProps{Minimum: &oneF}, num, true},
		"a minimum at it":            {apiextensionsv1.JSONSchemaProps{Minimum: &zeroF}, num, false},
		"an exclusive minimum at it": {apiextensionsv1.JSONSchemaProps{Minimum: &zeroF, ExclusiveMinimum: true}, num, true},
		"a number's enumeration":     {apiextensionsv1.JSONSchemaProps{Enum: raw("1", "2")}, num, true},
		"a struct":                   {apiextensionsv1.JSONSchemaProps{MinLength: &one}, reflect.TypeFor[struct{}](), false},
	} {
		t.Run(name, func(t *testing.T) {
			if why := crdRefusesZero(tc.schema, tc.typ); (why != "") != tc.refused {
				t.Errorf("crdRefusesZero = %q, want refused = %v", why, tc.refused)
			}
		})
	}
}

// TestCertManagerKinds_NoDefaultedZeros is TestPolicyFreeKinds_NoDefaultedZeros
// for the kinds of cert-manager's API, whose types publish no field
// description to read a default from: no field these kinds decode may be a
// number or a boolean that is omitted when zero and that the CRD defaults to
// something else, since policyFreeKind.config carries no defaulted-zero list.
//
// Only a default of the CRD replaces an omitted field in the object. One the
// controller applies when it reads the object (a key size, a rotation policy)
// reads the same Go type, in which an authored 0 and none are one value, so
// nothing is lost between the document and what cert-manager sees.
//
// At v1.21.2 the Certificate CRD holds no default and the two issuer CRDs four,
// all strings.
func TestCertManagerKinds_NoDefaultedZeros(t *testing.T) {
	for _, kind := range certManagerKinds {
		t.Run(kind.component, func(t *testing.T) {
			file := certManagerCRD(t, kind.crd)
			// Vacuity guards: both walks reach depth.
			props, _ := crdSpecProperties(t, file)
			omitted := omitemptyScalarPaths(kind.typ)
			deep := map[string]string{
				"issuer":        "acme.solvers[].http01.ingress.podTemplate.spec.securityContext.runAsNonRoot",
				"clusterissuer": "acme.solvers[].http01.ingress.podTemplate.spec.securityContext.runAsNonRoot",
				"certificate":   "privateKey.size",
			}[kind.component]
			if _, ok := props[deep]; !ok {
				t.Fatalf("the CRD walk did not reach %s; it found %d properties", deep, len(props))
			}
			reached := map[string]string{
				"issuer":        "acme.skipTLSVerify",
				"clusterissuer": "acme.skipTLSVerify",
				"certificate":   "privateKey.size",
			}[kind.component]
			if !omitted[reached] {
				t.Fatalf("the reflection walk did not reach %s; it found %v", reached, slices.Sorted(maps.Keys(omitted)))
			}
			if kind.component != "certificate" {
				const group = "acme.solvers[].http01.gatewayHTTPRoute.parentRefs[].group"
				if def := crdSpecScalarDefaults(t, file, "string")[group]; def != `"gateway.networking.k8s.io"` {
					t.Fatalf("%s has the default %s, want \"gateway.networking.k8s.io\"; the CRD's defaults are not being read", group, def)
				}
			}
			for path, def := range crdSpecScalarDefaults(t, file, "integer", "number", "boolean") {
				if omitted[path] && !crdDefaultIsZero(def) {
					t.Errorf("%s is omitted when zero and defaults to %s: an authored zero would be replaced; the kind needs a defaulted-zero list, which policyFreeKind does not carry", path, def)
				}
			}
		})
	}
}
