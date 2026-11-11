package components

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// cnpgClusterAbsentFields are the fields the linked barman-cloud type holds
// and the Cluster CRD of cloudnative-pg v1.30.1 (clusters.yaml) has no
// property for, under the backup's object store and under each external
// cluster's: sseCustomerKey with its name and key, and s3AddressingStyle.
// Remove them when cloudnative-pg ships a CRD that carries them: the kind's
// row in the API-set table then fails until its absent list is empty.
func cnpgClusterAbsentFields() []string {
	var out []string
	for _, store := range []string{"backup.barmanObjectStore", "externalClusters[].barmanObjectStore"} {
		key := store + ".s3Credentials.sseCustomerKey"
		out = append(out, key, key+".name", key+".key", store+".s3AddressingStyle")
	}
	return out
}

// TestCnpgClusterHandler_AbsentFromCRD shows the kind's answer for each of the
// fields cnpgClusterAbsentFields lists: authored, under the backup's store or
// under any external cluster's, the kind refuses it by its path; left out or
// null, the same properties build. TestKindComponents_OmittedRequiredAndWrittenDefaults derives the list
// from the CRD in both directions.
func TestCnpgClusterHandler_AbsentFromCRD(t *testing.T) {
	// The words are a literal, not the handler's constant: the test is then
	// shown to fail against a handler that lacks the refusal, and not to fail
	// to build.
	const absentSays = "no field of the postgresql.cnpg.io/v1 Cluster API: its CRD has no such property"
	// store is an object store that carries its credentials, with extra set on it.
	store := func(extra map[string]any) map[string]any {
		s := map[string]any{
			"destinationPath": "s3://bucket/db/",
			"s3Credentials":   map[string]any{"inheritFromIAMRole": true},
		}
		for k, v := range extra {
			s[k] = v
		}
		return s
	}
	withKey := func(key any) map[string]any {
		return store(map[string]any{"s3Credentials": map[string]any{"inheritFromIAMRole": true, "sseCustomerKey": key}})
	}
	sites := []struct {
		name  string
		path  string
		props func(store map[string]any) map[string]any
	}{
		{"backup", "backup.barmanObjectStore", func(s map[string]any) map[string]any {
			return map[string]any{"backup": map[string]any{"barmanObjectStore": s}}
		}},
		{"first external cluster", "externalClusters[0].barmanObjectStore", func(s map[string]any) map[string]any {
			return map[string]any{"externalClusters": []any{map[string]any{"name": "origin", "barmanObjectStore": s}}}
		}},
		{"second external cluster", "externalClusters[1].barmanObjectStore", func(s map[string]any) map[string]any {
			return map[string]any{"externalClusters": []any{
				map[string]any{"name": "first", "barmanObjectStore": store(nil)},
				map[string]any{"name": "origin", "barmanObjectStore": s},
			}}
		}},
	}
	authored := []struct {
		field string
		store map[string]any
	}{
		{"s3Credentials.sseCustomerKey", withKey(map[string]any{"name": "sse", "key": "key"})},
		{"s3AddressingStyle", store(map[string]any{"s3AddressingStyle": "path"})},
	}
	build := func(props map[string]any) error {
		_, err := (&CnpgClusterHandler{}).ToApplicationConfig(&oam.Component{Name: "db", Type: "cnpg-cluster", Properties: props}, "data")
		return err
	}
	for _, site := range sites {
		t.Run(site.name, func(t *testing.T) {
			for _, a := range authored {
				err := build(site.props(a.store))
				want := site.path + "." + a.field
				if err == nil || !strings.Contains(err.Error(), want+": "+absentSays) {
					t.Errorf("%s authored: got %v, want a refusal naming %s and saying %q", a.field, err, want, absentSays)
				}
			}
			for name, s := range map[string]map[string]any{
				"left out":                         store(nil),
				"sseCustomerKey null":              withKey(nil),
				"s3AddressingStyle null":           store(map[string]any{"s3AddressingStyle": nil}),
				"s3AddressingStyle authored empty": store(map[string]any{"s3AddressingStyle": ""}),
			} {
				if err := build(site.props(s)); err != nil {
					t.Errorf("%s: the same properties must build, got %v", name, err)
				}
			}
		})
	}
}
