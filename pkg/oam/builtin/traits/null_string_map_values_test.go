package traits

import (
	"reflect"
	"testing"
)

// TestNullStringMapValue_IsAbsence: an authored null as the value of one key of
// a string map states no value, so the key is left out. It used to be written
// as the text "<nil>" (go-kure/launcher#790). One case per site that turns an
// authored map into strings; the null is tried untyped and typed, beside a key
// that has a value and a non-string scalar, which still becomes its text.
func TestNullStringMapValue_IsAbsence(t *testing.T) {
	var typedNil map[string]any
	nulls := map[string]any{"untyped null": nil, "typed null": typedNil}
	want := map[string]string{"kept": "v", "number": "8080", "flag": "true"}
	authored := func(null any) map[string]any {
		return map[string]any{"kept": "v", "number": 8080, "flag": true, "dropped": null}
	}

	sites := []struct {
		name  string
		parse func(m map[string]any) (map[string]string, error)
	}{
		{"ingress annotations", func(m map[string]any) (map[string]string, error) {
			cfg, err := (&IngressHandler{}).parseProperties(ingressProps(map[string]any{
				"host": "web.example.com", "paths": []any{map[string]any{}}},
				map[string]any{"annotations": m}), sweepApp())
			if err != nil {
				return nil, err
			}
			return cfg.Annotations, nil
		}},
		{"httproute annotations", func(m map[string]any) (map[string]string, error) {
			props := httpRouteProps([]any{map[string]any{}})
			props["annotations"] = m
			cfg, err := (&HTTPRouteHandler{}).parseProperties(props, sweepApp())
			if err != nil {
				return nil, err
			}
			return cfg.Annotations, nil
		}},
		{"external-secret target.template.data", func(m map[string]any) (map[string]string, error) {
			cfg, err := (&ExternalSecretHandler{}).parseProperties(externalSecretProps(map[string]any{
				"data": []any{map[string]any{"secretKey": "k", "remoteRef": map[string]any{"key": "db/pass"}}},
				"target": map[string]any{
					"template": map[string]any{"type": "Opaque", "data": m}},
			}), sweepApp())
			if err != nil {
				return nil, err
			}
			return cfg.Template.Data, nil
		}},
		{"external-secret dataFrom[].find.tags", func(m map[string]any) (map[string]string, error) {
			cfg, err := (&ExternalSecretHandler{}).parseProperties(externalSecretProps(map[string]any{
				"dataFrom": []any{map[string]any{"find": map[string]any{
					"name": map[string]any{"regexp": "db-.*"}, "tags": m}}},
			}), sweepApp())
			if err != nil {
				return nil, err
			}
			return cfg.DataFrom[0].Find.Tags, nil
		}},
	}
	for _, site := range sites {
		for name, null := range nulls {
			t.Run(site.name+"/"+name, func(t *testing.T) {
				got, err := site.parse(authored(null))
				if err != nil {
					t.Fatalf("parseProperties: %v", err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("got %v, want %v: the null key is left out", got, want)
				}
			})
		}
		// A map of nulls alone reads as an authored empty map does.
		t.Run(site.name+"/only a null", func(t *testing.T) {
			got, err := site.parse(map[string]any{"dropped": nil})
			if err != nil {
				t.Fatalf("parseProperties: %v", err)
			}
			empty, err := site.parse(map[string]any{})
			if err != nil {
				t.Fatalf("parseProperties: %v", err)
			}
			if len(got) != 0 || !reflect.DeepEqual(got, empty) {
				t.Errorf("got %#v, want what an empty map gives, %#v", got, empty)
			}
		})
	}
}
