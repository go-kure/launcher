package kurel

import (
	"strings"
	"testing"
)

// TestBuild_PostgresqlDefaultsTraitIsEngineOnly: the trait the postgresql rule
// attaches to its Cluster cannot be authored, on postgresql or on the
// cnpg-cluster kind it lowers onto. kurel's parse already refuses it as a trait
// type a document may not name; the engine's own refusals behind that are pinned
// in pkg/oam (engine_trait_test.go).
func TestBuild_PostgresqlDefaultsTraitIsEngineOnly(t *testing.T) {
	for _, typ := range []string{"postgresql", "cnpg-cluster"} {
		t.Run(typ, func(t *testing.T) {
			app := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: db
      type: ` + typ + `
      properties: {}
      traits:
        - type: cnpg-postgresql-defaults
          properties: {}
`
			_, out, err := buildDocs(t, app)
			const want = `invalid value "cnpg-postgresql-defaults" for "type" in "db"`
			if err == nil {
				t.Fatalf("build accepted an authored cnpg-postgresql-defaults trait on %s:\n%s", typ, out)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want one naming %s", err, want)
			}
		})
	}
}

// TestBuild_PostgresqlGeneratedNameCollision: a database whose generated
// component name is already a component of the document is refused, naming the
// postgresql component, the database and the authored component.
func TestBuild_PostgresqlGeneratedNameCollision(t *testing.T) {
	app := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: db
      type: postgresql
      properties:
        databases:
          - name: orders
            owner: app
    - name: db-orders
      type: webservice
      properties:
        image: ghcr.io/example/orders:v1.0.0
        port: 8080
`
	_, out, err := buildDocs(t, app)
	if err == nil {
		t.Fatalf("build accepted a generated name collision:\n%s", out)
	}
	for _, want := range []string{
		`component "db" (type "postgresql")`,
		`databases[0] "orders": generates component "db-orders", which is already the name of component "db-orders" (type "webservice") in the document; rename one of them`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nwant it to contain %q", err, want)
		}
	}
}
