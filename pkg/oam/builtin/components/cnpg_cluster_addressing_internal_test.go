package components

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestCnpgClusterHandler_AddressingNeedsCredentials shows the kind's answer to
// the rule of the Cluster CRD of cloudnative-pg v1.30.2, "s3AddressingStyle
// requires s3Credentials": an object store that sets the style without
// credentials is refused by its path, under the backup and under each external
// cluster, by the handler and by Generate; with credentials, or with the style
// left out, null or empty, the same properties build.
func TestCnpgClusterHandler_AddressingNeedsCredentials(t *testing.T) {
	store := func(extra map[string]any) map[string]any {
		s := map[string]any{"destinationPath": "s3://bucket/db/"}
		for k, v := range extra {
			s[k] = v
		}
		return s
	}
	creds := map[string]any{"inheritFromIAMRole": true}
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
				map[string]any{"name": "first", "barmanObjectStore": store(map[string]any{"s3Credentials": creds})},
				map[string]any{"name": "origin", "barmanObjectStore": s},
			}}
		}},
	}
	build := func(props map[string]any) error {
		_, err := (&CnpgClusterHandler{}).ToApplicationConfig(&oam.Component{Name: "db", Type: "cnpg-cluster", Properties: props}, "data")
		return err
	}
	for _, site := range sites {
		t.Run(site.name, func(t *testing.T) {
			err := build(site.props(store(map[string]any{"s3AddressingStyle": "path"})))
			want := site.path + ".s3AddressingStyle: requires s3Credentials"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("style without credentials: got %v, want a refusal naming %q", err, want)
			}
			for name, s := range map[string]map[string]any{
				"style with credentials":         store(map[string]any{"s3AddressingStyle": "virtual", "s3Credentials": creds}),
				"left out":                       store(nil),
				"style null":                     store(map[string]any{"s3AddressingStyle": nil}),
				"style authored empty":           store(map[string]any{"s3AddressingStyle": ""}),
				"credentials without any style":  store(map[string]any{"s3Credentials": creds}),
				"sseCustomerKey with its fields": store(map[string]any{"s3Credentials": map[string]any{"inheritFromIAMRole": true, "sseCustomerKey": map[string]any{"name": "sse", "key": "key"}}}),
			} {
				if err := build(site.props(s)); err != nil {
					t.Errorf("%s: the same properties must build, got %v", name, err)
				}
			}
		})
	}

	t.Run("Generate", func(t *testing.T) {
		spec := cnpgv1.ClusterSpec{Instances: 1, ExternalClusters: []cnpgv1.ExternalCluster{
			{Name: "origin", BarmanObjectStore: &cnpgv1.BarmanObjectStoreConfiguration{DestinationPath: "s3://bucket/db/", S3AddressingStyle: "path"}},
		}}
		c := &CnpgClusterConfig{Name: "db", Namespace: "data", Spec: spec}
		_, err := c.Generate(stack.NewApplication("db", "data", c))
		want := "externalClusters[0].barmanObjectStore.s3AddressingStyle: requires s3Credentials"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Generate: got %v, want a refusal naming %q", err, want)
		}
	})
}
