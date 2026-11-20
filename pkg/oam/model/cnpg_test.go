package model_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the done-when of go-kure/launcher#1643: a model of a CloudNativePG cluster
// with a Barman Cloud object store and a scheduled backup refers to the store and to the
// cluster only through the name of the part that makes each, so a reference follows the object
// it points at whatever the part is called.

const cnpgModelType = "pgbackup.acme.example@v1"

// cnpgModel is the model with the three parts under the given ids. Nothing in the document
// names an object: every reference is a read of `parts.<id>.name`.
func cnpgModel(store, cluster, backup string) string {
	return outputsDoc("pgbackup.acme.example", `  outputs:
    cluster: "${ parts.`+cluster+`.name }"
    store: "${ parts.`+store+`.name }"
`, `
    - id: `+store+`
      type: cnpg-objectstore.launcher.gokure.dev@v1
      properties:
        configuration:
          destinationPath: s3://backups/db
    - id: `+cluster+`
      type: cnpg-cluster.launcher.gokure.dev@v1
      properties:
        instances: 1
        storage:
          size: 1Gi
        plugins:
          - name: barman-cloud.cloudnative-pg.io
            isWALArchiver: true
            parameters:
              barmanObjectName: "${ parts.`+store+`.name }"
    - id: `+backup+`
      type: cnpg-scheduledbackup.launcher.gokure.dev@v1
      properties:
        schedule: "0 0 3 * * *"
        method: plugin
        pluginConfiguration:
          name: barman-cloud.cloudnative-pg.io
        cluster:
          name: "${ parts.`+cluster+`.name }"
`)
}

// TestCNPGModel_EveryReferenceEqualsTheNameOfTheObjectItPointsAt renders the model twice with
// the part ids renamed. The emitted names change with the ids, so the assertion is that in each
// render the plugin's store name is the ObjectStore the build made, the backup's cluster is the
// Cluster the build made, and the model publishes those two names.
func TestCNPGModel_EveryReferenceEqualsTheNameOfTheObjectItPointsAt(t *testing.T) {
	seen := map[string]bool{}
	for _, ids := range [][3]string{{"store", "cluster", "nightly"}, {"bucket", "pg", "daily"}} {
		tr, _ := registered(t, cnpgModel(ids[0], ids[1], ids[2]))
		comp := oam.Component{Name: "orders", Type: cnpgModelType}
		objs := objectsOK(t, tr, []oam.Component{comp}, nil)

		stores, clusters, backups := names(objs, "ObjectStore"), names(objs, "Cluster"), names(objs, "ScheduledBackup")
		if len(stores) != 1 || len(clusters) != 1 || len(backups) != 1 {
			t.Fatalf("ids %v: the build made ObjectStores %v, Clusters %v, ScheduledBackups %v, want one of each", ids, stores, clusters, backups)
		}
		if got := prop(t, objs[[2]string{"Cluster", clusters[0]}], "spec", "plugins", 0, "parameters", "barmanObjectName"); got != stores[0] {
			t.Errorf("ids %v: the plugin names the store %v, the build made the ObjectStore %s", ids, got, stores[0])
		}
		if got := prop(t, objs[[2]string{"ScheduledBackup", backups[0]}], "spec", "cluster", "name"); got != clusters[0] {
			t.Errorf("ids %v: the backup names the cluster %v, the build made the Cluster %s", ids, got, clusters[0])
		}

		got := outputKinds(t, tr, nil, comp)
		if got["cluster"] != "Cluster/"+clusters[0] || got["store"] != "ObjectStore/"+stores[0] {
			t.Errorf("ids %v: the model publishes %v, the build made the Cluster %s and the ObjectStore %s", ids, got, clusters[0], stores[0])
		}
		seen[clusters[0]] = true
	}
	// The control: the two renders made different objects, so a reference that held a literal
	// name would have failed one of them.
	if len(seen) != 2 {
		t.Fatalf("the two renders made the same Cluster name: %v", seen)
	}
}
