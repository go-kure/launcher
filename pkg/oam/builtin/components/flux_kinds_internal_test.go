package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
)

// fluxMarkerModules are the modules whose Go source the tests below read: the
// API modules of the Flux controllers hold the Go types and ship no CRD, so
// the markers the CRDs are generated from are the source, as for the kinds of
// the Prometheus operator's API. Nothing here is held to the API server's own
// validator (crdCreate), which answers from a CRD.
var fluxMarkerModules = []string{
	"github.com/fluxcd/notification-controller/api",
	"github.com/fluxcd/pkg/apis/meta",
}

// fluxKindRows lists the kind components of those APIs with the spec type each
// decodes into and its required list. validated names the required fields the
// list cannot hold, because a parent of theirs is written whether or not it
// was authored; the kind's validate refuses those.
var fluxKindRows = []struct {
	component string
	typ       reflect.Type
	required  map[string]string
	validated []string
}{
	{fluxcdAlertType, reflect.TypeFor[notificationv1beta3.AlertSpec](), fluxcdAlertKind.required, nil},
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
	for _, at := range []string{"AlertSpec: suspend"} {
		if !walked[at] {
			t.Errorf("the walk did not reach %s; it found %v", at, slices.Sorted(maps.Keys(walked)))
		}
	}
}
