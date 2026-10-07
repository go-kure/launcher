package components

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// volsyncModulePath is the module whose CRDs the tests below read: VolSync
// ships the CRDs its chart installs beside its API types.
const volsyncModulePath = "github.com/backube/volsync"

// volsyncNodeSelectorTerms is, under a mover's property, the one required
// field of an embedded Kubernetes type that the type writes as null: the terms
// of a required node affinity.
const volsyncNodeSelectorTerms = ".moverAffinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms"

// volsyncKinds lists the kind components of VolSync's API with the CRD of the
// object each emits, the spec type it decodes into, its required list and the
// movers of the type that hold an affinity (MoverConfig).
var volsyncKinds = []struct {
	component string
	handler   oam.ComponentHandler
	crd       string
	typ       reflect.Type
	required  map[string]string
	movers    []string
}{
	{
		"replicationsource", &ReplicationSourceHandler{}, "replicationsources.yaml", reflect.TypeFor[volsyncv1alpha1.ReplicationSourceSpec](),
		replicationSourceKind.required, []string{"rclone", "restic", "rsyncTLS", "syncthing"},
	},
	{
		"replicationdestination", &ReplicationDestinationHandler{}, "replicationdestinations.yaml", reflect.TypeFor[volsyncv1alpha1.ReplicationDestinationSpec](),
		replicationDestinationKind.required, []string{"rclone", "restic", "rsyncTLS"},
	},
}

// volsyncCRD is the path of one CRD of the linked module.
func volsyncCRD(t *testing.T, file string) string {
	t.Helper()
	return filepath.Join(linkedModuleDir(t, volsyncModulePath), filepath.FromSlash(volsyncCRDs+file))
}

// TestVolsyncKinds_RequiredMatchCRD is TestCertManagerKinds_RequiredMatchCRD
// for the kinds of VolSync's API. From the CRDs of the linked module it derives
// the fields of VolSync's own types that the API requires and the type would
// write unauthored, and holds each kind's required list to them, both ways: a
// dependency bump that adds, drops or moves one fails here, naming it. Every
// other field of those types that is written unauthored is held to its schema:
// none may be refused empty.
//
// Of the Kubernetes types these specs embed (a pod security context, resource
// requirements, an affinity, three volume sources), only a field the CRD
// refuses as the type writes it unauthored is derived, and the kinds answer
// those outside their required lists: the terms of a required node affinity,
// which refuseVolsyncNullNodeSelectorTerms refuses and
// TestKindComponents_NullRequired shows with the CRDs' validator. The set
// derived must be exactly those, one per mover that holds an affinity. One
// pair more is derived, as a field of VolSync's own types is: the key and the
// operator of a label selector's match expression, which the CRDs require and
// the type writes empty. TestLabelSelectorKinds_CoverEverySelector shows, path
// by path, what the API server answers for them.
func TestVolsyncKinds_RequiredMatchCRD(t *testing.T) {
	expression := reflect.TypeFor[metav1.LabelSelectorRequirement]()
	for _, kind := range volsyncKinds {
		t.Run(kind.component, func(t *testing.T) {
			props, required := crdSpecProperties(t, volsyncCRD(t, kind.crd), "v1alpha1")
			listed, empty, embedded := map[string]bool{}, map[string]bool{}, map[string]bool{}
			fields := 0
			walkKindFields(kind.typ, func(f kindField) bool { return required[f.path] }, func(f kindField) {
				if !strings.HasPrefix(f.owner.PkgPath(), volsyncModulePath+"/") {
					prop, ok := props[f.path]
					switch {
					case !ok || !required[f.path] || !f.writtenUnauthored():
					case f.owner == expression:
						listed[f.path] = true
					case f.encodesNull() || crdRefusesZero(prop, f.field.Type) != "":
						embedded[f.path] = true
					}
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
			t.Logf("walked %d fields of VolSync's types; required and written unauthored: %v; of an embedded type, refused as written: %v; written empty unauthored: %v",
				fields, slices.Sorted(maps.Keys(listed)), slices.Sorted(maps.Keys(embedded)), slices.Sorted(maps.Keys(empty)))
			if got, want := slices.Sorted(maps.Keys(kind.required)), slices.Sorted(maps.Keys(listed)); !slices.Equal(got, want) {
				t.Errorf("required list = %v\nthe CRD requires %v", got, want)
			}
			for path, says := range kind.required {
				if strings.TrimSpace(says) == "" {
					t.Errorf("required field %s says nothing of itself", path)
				}
			}
			var terms []string
			for _, mover := range kind.movers {
				terms = append(terms, mover+volsyncNodeSelectorTerms)
			}
			slices.Sort(terms)
			if got := slices.Sorted(maps.Keys(embedded)); !slices.Equal(got, terms) {
				t.Errorf("of the embedded types, the CRD refuses as written unauthored %v\nthe kind's validate answers %v", got, terms)
			}
		})
	}
}

// schemaDefaults returns, sorted, every default a schema declares, on itself
// or anywhere under it, as its json path, " = " and the default: a property
// under its name, the schema of a list's elements under [], of a map's values
// under {}. It also returns the number of schemas it visited.
func schemaDefaults(s apiextensionsv1.JSONSchemaProps) ([]string, int) {
	var found []string
	visited := 0
	walkCiliumBGPSchema(s, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
		visited++
		if s.Default != nil {
			found = append(found, path+" = "+strings.TrimSpace(string(s.Default.Raw)))
		}
	})
	slices.Sort(found)
	return found, visited
}

// TestVolsyncKinds_NoDefaults: the CRDs of the linked module default nothing
// under spec, on a property, on the elements of a list or on the values of a
// map. Neither kind sets a defaulted-zero list (policyFreeKind.defaultedZeros),
// so a number or a
// boolean that the type omits when zero must have no default an authored 0 or
// false would be replaced by; these CRDs have no default at all, and the test
// holds them to that, which is more than the kinds need and simpler to read. A
// dependency bump that adds a default fails here, naming the field: it is then
// answered where its kind is (a defaulted-zero list for a field omitted when
// zero, apiSetKinds for one the type writes).
func TestVolsyncKinds_NoDefaults(t *testing.T) {
	// Vacuity guards: a default is read where a schema has one, on a property,
	// on a list's elements and on a map's values; and where a linked CRD has
	// one.
	one := &apiextensionsv1.JSON{Raw: []byte("1")}
	made, _ := schemaDefaults(apiextensionsv1.JSONSchemaProps{Properties: map[string]apiextensionsv1.JSONSchemaProps{
		"count":  {Default: one},
		"groups": {Items: &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &apiextensionsv1.JSONSchemaProps{Default: one}}},
		"labels": {AdditionalProperties: &apiextensionsv1.JSONSchemaPropsOrBool{Schema: &apiextensionsv1.JSONSchemaProps{Default: one}}},
	}})
	if want := []string{"count = 1", "groups[] = 1", "labels{} = 1"}; !slices.Equal(made, want) {
		t.Fatalf("the defaults read of a schema that declares three = %v, want %v", made, want)
	}
	if peer, _ := schemaDefaults(ciliumBGPSpec(t, "ciliumbgppeerconfigs.yaml")); !slices.Contains(peer, "timers.holdTimeSeconds = 90") {
		t.Fatalf("the defaults read of Cilium's CiliumBGPPeerConfig CRD = %v, without its hold time; the CRDs' defaults are not being read", peer)
	}
	for _, kind := range volsyncKinds {
		t.Run(kind.component, func(t *testing.T) {
			crd := volsyncCRDObject(t, kind.crd)
			for _, version := range crd.Spec.Versions {
				if version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
					t.Fatalf("version %s has no schema", version.Name)
				}
				spec, ok := version.Schema.OpenAPIV3Schema.Properties["spec"]
				if !ok {
					t.Fatalf("version %s has no spec property", version.Name)
				}
				// Vacuity guard: the walk reaches depth, under a mover's
				// embedded Kubernetes type, and the elements of a list there.
				reached := map[string]bool{
					"restic.moverSecurityContext.windowsOptions.hostProcess": false,
					"restic.moverSecurityContext.supplementalGroups[]":       false,
				}
				walkCiliumBGPSchema(spec, "", func(path string, _ apiextensionsv1.JSONSchemaProps) {
					if _, asked := reached[path]; asked {
						reached[path] = true
					}
				})
				defaults, visited := schemaDefaults(spec)
				for path, ok := range reached {
					if !ok {
						t.Fatalf("the walk of version %s did not reach %s; it visited %d schemas", version.Name, path, visited)
					}
				}
				for _, found := range defaults {
					t.Errorf("version %s has the default %s; the kinds are written for an API that defaults nothing under spec", version.Name, found)
				}
			}
		})
	}
}

// volsyncAbsentFields lists, for the movers named, the fields the linked
// Kubernetes type holds under a mounted Secret that the CRDs do not: the
// kinds refuse each when it is authored (refuseVolsyncAbsentFields).
func volsyncAbsentFields(movers ...string) []string {
	var out []string
	for _, mover := range movers {
		secret := mover + ".moverVolumes[].volumeSource.secret."
		out = append(out, secret+"defaultUser", secret+"items[].user")
	}
	return out
}

// TestVolsyncKinds_AbsentFromCRD derives, from the CRDs of the linked module,
// the fields each kind's type reaches that its CRD has no property for, and
// holds the kind's refusals to them, both ways: the linked Kubernetes API is
// newer than the one VolSync's CRDs were generated from, and the strict decode
// reads the Go type. A bump of either module that closes the gap, or widens
// it, fails here with the field named.
//
// Each field is then shown on the kind's handler: authored, in the first
// volume or a later one, it is refused by its path, an authored 0 included;
// left out or null, the same properties build.
//
// The refusal is held to what the API server does with the field. crdCreate
// runs its create sequence on the linked CRD: the decode-time pass names the
// field as one the schema does not know, which refuses a request made with
// strict field validation, and prunes it from the document it goes on with,
// what held the field staying. Either way the object is not what the author
// wrote. Next to it, the same document without the field is accepted, and so
// is the object the kind emits for it.
func TestVolsyncKinds_AbsentFromCRD(t *testing.T) {
	for _, kind := range volsyncKinds {
		t.Run(kind.component, func(t *testing.T) {
			props, required := crdSpecProperties(t, volsyncCRD(t, kind.crd), "v1alpha1")
			var absent []string
			fields := 0
			walkKindFields(kind.typ, func(f kindField) bool { return required[f.path] }, func(f kindField) {
				fields++
				if _, ok := props[f.path]; !ok {
					absent = append(absent, f.path)
				}
			})
			slices.Sort(absent)
			want := volsyncAbsentFields(kind.movers...)
			slices.Sort(want)
			// Vacuity guard: the walk goes as deep as the fields refused.
			if fields == 0 || !slices.ContainsFunc(slices.Collect(maps.Keys(props)), func(path string) bool {
				return strings.HasSuffix(path, ".moverVolumes[].volumeSource.secret.items[].mode")
			}) {
				t.Fatalf("the walks did not reach the items of a mounted Secret: %d fields, %d properties", fields, len(props))
			}
			if !slices.Equal(absent, want) {
				t.Errorf("reached by the type and no property of the CRD:\n  derived %v\n  refused %v", absent, want)
			}

			build := handlerBuild(kind.handler, kind.component)
			// What the API server does with such a field: crdCreate runs its
			// create sequence on the linked CRD.
			crd := volsyncCRDObject(t, kind.crd)
			shown := checkedRules{
				create: crdCreateOf(t, crd, "v1alpha1"), component: kind.component, handler: kind.handler,
				document: func(props map[string]any) map[string]any {
					return crdDocument(crd, "v1alpha1", map[string]any{"spec": props})
				},
			}
			// volume is a mounted Secret with one item, and the field named,
			// when it is not "", set to value.
			volume := func(field string, value any) map[string]any {
				item := map[string]any{"key": "token", "path": "token"}
				secret := map[string]any{"secretName": "creds", "items": []any{item}}
				switch field {
				case "defaultUser":
					secret[field] = value
				case "items[].user":
					secret["items"] = []any{map[string]any{"key": "ca", "path": "ca"}, item}
					item["user"] = value
				}
				return map[string]any{"mountPath": "creds", "volumeSource": map[string]any{"secret": secret}}
			}
			for _, mover := range kind.movers {
				for field, at := range map[string]string{"defaultUser": "defaultUser", "items[].user": "items[1].user"} {
					with := func(volumes ...any) map[string]any {
						return map[string]any{mover: map[string]any{"moverVolumes": volumes}}
					}
					for name, tc := range map[string]struct {
						props map[string]any
						index int
					}{
						"authored":          {with(volume(field, 1000)), 0},
						"an authored 0":     {with(volume(field, 0)), 0},
						"in a later volume": {with(volume("", nil), volume(field, 1000)), 1},
					} {
						want := fmt.Sprintf("%s.moverVolumes[%d].volumeSource.secret.%s: %s", mover, tc.index, at, volsyncAbsent)
						if err := build(tc.props); err == nil || err.Error() != want {
							t.Errorf("%s, %s %s: err = %v, want %q", mover, field, name, err, want)
						}
						// The API server names the field as one its schema does
						// not know, and nothing else of the document; the document
						// it goes on with holds what held the field, without it.
						what := fmt.Sprintf("%s, %s %s", mover, field, name)
						answer := shown.create.create(t, shown.document(tc.props))
						unknown := fmt.Sprintf("spec.%s.moverVolumes[%d].volumeSource.secret.%s", mover, tc.index, at)
						if !slices.Equal(answer.unknown, []string{unknown}) {
							t.Errorf("%s: the API server answers %q, want the one unknown field %s", what, answer, unknown)
						}
						holder, key := []any{"spec", mover, "moverVolumes", tc.index, "volumeSource", "secret"}, "defaultUser"
						if field == "items[].user" {
							holder, key = append(holder, "items", 1), "user"
						}
						held, _ := volsyncDig(answer.object, holder...).(map[string]any)
						if held == nil {
							t.Errorf("%s: the document the API server goes on with holds nothing at %v", what, holder)
						} else if _, kept := held[key]; kept {
							t.Errorf("%s: the document the API server goes on with still holds %s", what, unknown)
						}
					}
					for name, props := range map[string]map[string]any{
						"left out": with(volume("", nil)),
						"null":     with(volume(field, nil)),
					} {
						what := fmt.Sprintf("%s, %s %s", mover, field, name)
						object, err := shown.build(t, props)
						if err != nil {
							t.Errorf("%s: %v, want it built", what, err)
							continue
						}
						shown.create.create(t, object).accepted(t, "the object the kind emits for "+what)
					}
					shown.create.create(t, shown.document(with(volume("", nil)))).accepted(t, mover+", "+field+" left out")
				}
			}
		})
	}
}

// volsyncCRDObject reads one CRD of the linked module.
func volsyncCRDObject(t *testing.T, file string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	data, err := os.ReadFile(volsyncCRD(t, file))
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", file, err)
	}
	return &crd
}

// volsyncDig returns what a document holds at path, a key of an object or an
// index of a list at each step, or nil where a step is not there.
func volsyncDig(v any, path ...any) any {
	for _, step := range path {
		switch at := step.(type) {
		case string:
			object, _ := v.(map[string]any)
			v = object[at]
		case int:
			list, _ := v.([]any)
			if at >= len(list) {
				return nil
			}
			v = list[at]
		}
	}
	return v
}

// TestVolsyncKinds_NoExpressionRules: the CRDs of the linked module declare no
// expression rule (x-kubernetes-validations) anywhere in the object, so the
// kinds have none to check or to leave to the API server. A dependency bump
// that adds one fails here, naming it: it is then classified as the rules of
// Cilium's BGP kinds are (TestCiliumBGPKinds_ExpressionRules) and, where a
// kind checks it, shown with checkedRules.
func TestVolsyncKinds_NoExpressionRules(t *testing.T) {
	// Vacuity guard: the walk reads a rule where a CRD declares one.
	_, peer := ciliumBGPCRD(t, "ciliumbgppeerconfigs.yaml")
	rules := 0
	walkCiliumBGPSchema(peer, "", func(_ string, s apiextensionsv1.JSONSchemaProps) { rules += len(s.XValidations) })
	if rules == 0 {
		t.Fatal("the walk reads no expression rule of Cilium's CiliumBGPPeerConfig CRD, which declares some; the rules are not being read")
	}
	for _, kind := range volsyncKinds {
		t.Run(kind.component, func(t *testing.T) {
			crd := volsyncCRDObject(t, kind.crd)
			// The CRD is one the API server serves, in the version the kind emits.
			crdCreateOf(t, crd, "v1alpha1")
			for _, version := range crd.Spec.Versions {
				if version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
					t.Fatalf("version %s has no schema", version.Name)
				}
				reached := false
				walkCiliumBGPSchema(*version.Schema.OpenAPIV3Schema, "", func(path string, s apiextensionsv1.JSONSchemaProps) {
					// Vacuity guard: the walk reaches depth.
					reached = reached || path == "spec.restic.moverSecurityContext.windowsOptions.hostProcess"
					for _, rule := range s.XValidations {
						t.Errorf("version %s declares the expression rule %q on %q; the kinds are written for a CRD that has none", version.Name, rule.Rule, path)
					}
				})
				if !reached {
					t.Fatalf("the walk of version %s did not reach a mover's security context", version.Name)
				}
			}
		})
	}
}

// volsyncNullRequiredRows is the rows of a VolSync kind in nullRequiredKinds:
// for each mover that holds an affinity, the terms of its required node
// affinity.
func volsyncNullRequiredRows(movers ...string) []nullRequiredRow {
	var rows []nullRequiredRow
	for _, mover := range movers {
		rows = append(rows, nullRequiredRow{
			path: mover + volsyncNodeSelectorTerms, at: mover + volsyncNodeSelectorTerms,
			omitted: func() map[string]any {
				return map[string]any{mover: map[string]any{"moverAffinity": requiredAffinityWithoutTerms()}}
			},
			reason: nodeSelectorTermsReason, authored: someNodeSelectorTerms(),
		})
	}
	return rows
}
