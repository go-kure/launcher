package components_test

import (
	"reflect"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The webservice and worker publish the `podMetadata` entry the deployment does,
// not a copy that may drift: a document moves between the three without a
// rename or a change of shape (go-kure/launcher#1216).
func TestRoleRules_PodMetadataSchemaIsTheDeploymentsEntry(t *testing.T) {
	want, ok := (&components.DeploymentHandler{}).PropertySchema()["podMetadata"]
	if !ok {
		t.Fatal("the deployment declares no podMetadata")
	}
	roles := map[string]map[string]oam.PropertySchema{
		"webservice": components.WebserviceRule{}.PropertySchema(),
		"worker":     components.WorkerRule{}.PropertySchema(),
	}
	for name, schema := range roles {
		t.Run(name, func(t *testing.T) {
			got, ok := schema["podMetadata"]
			if !ok {
				t.Fatalf("the %s declares no podMetadata", name)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the %s's podMetadata entry differs from the deployment's:\n got %+v\nwant %+v", name, got, want)
			}
		})
	}
}
