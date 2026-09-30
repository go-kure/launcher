package components_test

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// cnpgClusterExcludedFields lists the cnpgv1.ClusterSpec json fields the
// cnpg-cluster component deliberately does not publish, each with a one-line
// reason. It is empty: every field of the linked ClusterSpec is authorable. A
// field added here needs a reason a reviewer can check against the upstream
// type, and it must still exist upstream (the test refuses stale entries).
var cnpgClusterExcludedFields = map[string]string{}

// clusterSpecJSONFields returns the json name and Go type of every field of
// cnpgv1.ClusterSpec, as encoding/json sees them. An embedded field would
// promote its own fields and need walking; ClusterSpec has none, and the test
// fails rather than silently skipping one if that changes upstream.
func clusterSpecJSONFields(t *testing.T) map[string]reflect.Type {
	t.Helper()
	typ := reflect.TypeFor[cnpgv1.ClusterSpec]()
	fields := make(map[string]reflect.Type, typ.NumField())
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Anonymous {
			t.Fatalf("ClusterSpec embeds %s; walk its promoted fields before trusting this coverage test", f.Name)
		}
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = f.Type
	}
	if len(fields) == 0 {
		t.Fatal("found no json fields on cnpgv1.ClusterSpec; the reflection walk is broken")
	}
	return fields
}

// schemaTypeForGo maps a ClusterSpec field's Go type onto the PropertyType the
// schema must declare for it.
func schemaTypeForGo(typ reflect.Type) oam.PropertyType {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.String:
		return oam.PropertyTypeString
	case reflect.Bool:
		return oam.PropertyTypeBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return oam.PropertyTypeInteger
	case reflect.Slice, reflect.Array:
		return oam.PropertyTypeArray
	default:
		return oam.PropertyTypeObject
	}
}

// TestCnpgClusterSchema_CoversClusterSpec keeps the cnpg-cluster schema equal
// to the linked cnpgv1.ClusterSpec: every json field is published or excluded
// with a reason, no published key lacks a field, no exclusion is stale, and
// each published key declares the type its Go field decodes from. A dependency
// bump that adds, removes or retypes a top-level field fails here, naming it.
func TestCnpgClusterSchema_CoversClusterSpec(t *testing.T) {
	fields := clusterSpecJSONFields(t)
	schema := (&components.CnpgClusterHandler{}).PropertySchema()

	for _, name := range slices.Sorted(maps.Keys(fields)) {
		reason, excluded := cnpgClusterExcludedFields[name]
		prop, published := schema[name]
		switch {
		case published && excluded:
			t.Errorf("ClusterSpec field %q is both published and excluded (%q); pick one", name, reason)
		case !published && !excluded:
			t.Errorf("ClusterSpec field %q is neither published in the cnpg-cluster schema nor listed in cnpgClusterExcludedFields with a reason", name)
		case published:
			if want := schemaTypeForGo(fields[name]); prop.Type != want {
				t.Errorf("schema key %q declares type %q, but ClusterSpec's field is %s (want %q)", name, prop.Type, fields[name], want)
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(schema)) {
		if _, ok := fields[key]; !ok {
			t.Errorf("schema key %q has no ClusterSpec json field; the strict decode would refuse every value", key)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cnpgClusterExcludedFields)) {
		if _, ok := fields[name]; !ok {
			t.Errorf("cnpgClusterExcludedFields entry %q is stale: ClusterSpec has no such field", name)
		}
		if strings.TrimSpace(cnpgClusterExcludedFields[name]) == "" {
			t.Errorf("cnpgClusterExcludedFields entry %q has no reason", name)
		}
	}
}

// TestCnpgClusterSchema_EveryFieldReachable asserts that the strict decoder can
// set every ClusterSpec field under its published key (builtin's
// UnreachableJSONFields probes encoding/json itself). The component owns no key
// of its own, so there is nothing to shadow; a non-empty result means an
// upstream change made a field unreachable.
func TestCnpgClusterSchema_EveryFieldReachable(t *testing.T) {
	if got := builtin.UnreachableJSONFields(reflect.TypeFor[cnpgv1.ClusterSpec]()); len(got) != 0 {
		t.Errorf("ClusterSpec fields unreachable through the strict decode: %v", got)
	}
}
