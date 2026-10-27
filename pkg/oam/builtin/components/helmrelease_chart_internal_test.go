package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
)

// TestHelmReleaseChartInertFields_CoverInlinedSpec holds the inert chart
// template fields to the types: every field of the OCIRepositorySpec a chart
// template inlines is inert but interval and verify, which helm-controller
// carries into the HelmChart, and every field of its verification is inert
// but provider and secretRef. Each field's check reports that field set, and
// no other.
func TestHelmReleaseChartInertFields_CoverInlinedSpec(t *testing.T) {
	t.Run("spec", func(t *testing.T) {
		typ := reflect.TypeFor[sourcev1.OCIRepositorySpec]()
		checkInertFields(t, typ, []string{"interval", "verify"}, helmReleaseChartInertFields, func(field string) *helmv2.HelmChartTemplateSpec {
			s := &helmv2.HelmChartTemplateSpec{}
			setNonZero(t, reflect.ValueOf(&s.OCIRepositorySpec).Elem(), field)
			return s
		})
	})
	t.Run("verify", func(t *testing.T) {
		typ := reflect.TypeFor[sourcev1.OCIRepositoryVerification]()
		checkInertFields(t, typ, []string{"provider", "secretRef"}, helmReleaseChartVerifyInertFields, func(field string) *sourcev1.OCIRepositoryVerification {
			v := &sourcev1.OCIRepositoryVerification{}
			setNonZero(t, reflect.ValueOf(v).Elem(), field)
			return v
		})
	})
}

// checkInertFields holds inert to the json fields of typ less read, and each
// of its checks to the value with only that field set.
func checkInertFields[T any](t *testing.T, typ reflect.Type, read []string, inert map[string]func(*T) bool, with func(field string) *T) {
	t.Helper()
	var want []string
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if !slices.Contains(read, name) {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(inert)); !slices.Equal(got, want) {
		t.Fatalf("inert fields = %v, want every field of %s but %v: %v", got, typ, read, want)
	}
	for _, field := range want {
		v := with(field)
		for _, other := range want {
			if got := inert[other](v); got != (other == field) {
				t.Errorf("with %s set, the check of %s reports %v", field, other, got)
			}
		}
	}
}

// setNonZero sets the field of the struct v whose json name is field to a
// non-zero value.
func setNonZero(t *testing.T, v reflect.Value, field string) {
	t.Helper()
	for i := range v.NumField() {
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		if name != field {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
		case reflect.Slice:
			f.Set(reflect.MakeSlice(f.Type(), 1, 1))
		case reflect.String:
			f.SetString("x")
		case reflect.Bool:
			f.SetBool(true)
		default:
			t.Fatalf("%s: no non-zero value for a %s", field, f.Kind())
		}
		return
	}
	t.Fatalf("%s has no field %s", v.Type(), field)
}
