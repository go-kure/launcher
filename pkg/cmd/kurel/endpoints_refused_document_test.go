package kurel

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// endpointAnswer is what the endpoint entry (Transformer.ComponentEndpointsNamed)
// does with a component of a document the build refuses.
type endpointAnswer int

const (
	// endpointRefused: no endpoint, and the refusal carries the build's words.
	endpointRefused endpointAnswer = iota
	// endpointRefusedOtherwise: no endpoint, in other words than the build's.
	endpointRefusedOtherwise
	// endpointAnswered: an endpoint for a component the build refuses.
	endpointAnswered
)

func (a endpointAnswer) String() string {
	return [...]string{"refused in the build's words", "refused in other words", "answered"}[a]
}

// buildRefusal runs what `kurel build` runs on appYAML and returns the stage
// that refuses it and the refusal. A document the build accepts fails the test.
func buildRefusal(t *testing.T, appYAML string, hook func(oam.NameRequest) (string, bool)) (string, error) {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		return "validation", err
	}
	cluster, err := transformer.Transform(app, oam.TransformContext{Domain: kurelDomain, Naming: hook})
	if err != nil {
		return "transform", err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return "generation", err
	}
	if err := oam.CheckInDocumentCollisions(apps); err != nil {
		return "collision check", err
	}
	t.Fatal("the build accepts the document")
	return "", nil
}

// TestEndpoints_DocumentTheBuildRefuses holds every endpoint implementation to
// one rule: a component of a document the build refuses gets no endpoint. Each
// row is a document `kurel build` refuses, with the stage that refuses it and
// what the endpoint entry does with its component db.
//
// A row that is not endpointRefused is a disagreement this table found. It
// states what the entry does today, so that settling one changes its row.
func TestEndpoints_DocumentTheBuildRefuses(t *testing.T) {
	const web = "    - name: web\n      type: webservice\n      properties:\n        image: ghcr.io/example/web:v1.0.0\n"
	component := func(typ, props string) string {
		return "    - name: db\n      type: " + typ + "\n      properties:\n" + props
	}
	const pooler = "        pooler:\n          enabled: true\n"
	for _, tc := range []struct {
		name, components string
		names            map[string]string
		// stage is the build stage that refuses the document, and want a part of
		// its refusal.
		stage, want string
		answer      endpointAnswer
		// otherwise is a part of the entry's own refusal, for endpointRefusedOtherwise.
		otherwise string
	}{
		{
			name:       "cnpg-cluster: a misspelt key below a declared one",
			components: component("cnpg-cluster", "        storage:\n          sise: 1Gi\n"),
			stage:      "transform", want: `properties do not decode into a postgresql.cnpg.io/v1 ClusterSpec: json: unknown field "sise"`,
			answer: endpointRefused,
		},
		{
			name:       "cnpg-cluster: no instance",
			components: component("cnpg-cluster", "        instances: 0\n"),
			stage:      "transform", want: `instances: must be >= 1, got 0`,
			answer: endpointRefused,
		},
		{
			name:       "cnpg-cluster: a required string left out below its parent",
			components: component("cnpg-cluster", "        replica:\n          enabled: true\n"),
			stage:      "transform", want: `replica.source: required`,
			answer: endpointRefused,
		},
		{
			name:       "cnpg-cluster: an image without a tag",
			components: component("cnpg-cluster", "        imageName: ghcr.io/example/postgres\n"),
			stage:      "transform", want: `imageName: image "ghcr.io/example/postgres" rejected: no tag or digest specified`,
			answer: endpointRefused,
		},
		{
			name:       "cnpg-cluster: an undeclared property",
			components: component("cnpg-cluster", "        storge:\n          size: 1Gi\n"),
			stage:      "validation", want: `properties: unsupported field "storge"`,
			answer: endpointRefusedOtherwise, otherwise: `properties do not decode into a postgresql.cnpg.io/v1 ClusterSpec: json: unknown field "storge"`,
		},
		{
			name:       "cnpg-cluster: a storage size of 0",
			components: component("cnpg-cluster", "        storage:\n          size: \"0\"\n"),
			stage:      "generation", want: `storage.size: quantity must be positive, got "0"`,
			answer: endpointAnswered,
		},
		{
			name:       "cnpg-pooler: no pgbouncer",
			components: component("cnpg-pooler", "        cluster:\n          name: main\n"),
			stage:      "transform", want: `pgbouncer: required`,
			answer: endpointAnswered,
		},
		{
			name:       "cnpg-pooler: a misspelt key below a declared one",
			components: component("cnpg-pooler", "        cluster:\n          name: main\n        pgbouncer:\n          poolMod: session\n"),
			stage:      "transform", want: `properties do not decode into a postgresql.cnpg.io/v1 PoolerSpec: json: unknown field "poolMod"`,
			answer: endpointAnswered,
		},
		{
			name:       "postgresql: a Pooler named like the component, the Cluster named apart",
			components: component("postgresql", pooler+"        clusterObjectName: pg\n        poolerName: db\n"),
			stage:      "transform", want: `pooler: generates component "db", which is already the name of component "db" (type "postgresql") in the document; rename one of them`,
			answer: endpointRefused,
		},
		{
			name:       "postgresql: a Pooler the hook names like the component, the Cluster named apart",
			components: component("postgresql", pooler+"        clusterObjectName: pg\n"),
			names:      map[string]string{"pooler db-pooler": "db"},
			stage:      "transform", want: `pooler: generates component "db", which is already the name of component "db" (type "postgresql") in the document; rename one of them`,
			answer: endpointRefused,
		},
		{
			name:       "postgresql: a Pooler named like the component and its Cluster",
			components: component("postgresql", pooler+"        poolerName: db\n"),
			stage:      "transform", want: `pooler: generates component "db", which is already the name of component "db" (type "postgresql") in the document; rename one of them`,
			answer: endpointRefused,
		},
		{
			name:       "postgresql: a Pooler named like another component",
			components: component("postgresql", pooler+"        poolerName: web\n") + web,
			stage:      "transform", want: `pooler: generates component "web", which is already the name of component "web" (type "webservice") in the document; rename one of them`,
			answer: endpointAnswered,
		},
		{
			name:       "postgresql: a Database named like the component",
			components: component("postgresql", "        databases:\n          - name: orders\n            owner: app\n            objectName: db\n"),
			stage:      "transform", want: `databases[0] "orders": generates component "db", which is already the name of component "db" (type "postgresql") in the document; rename one of them`,
			answer: endpointAnswered,
		},
		{
			name:       "postgresql: a backup value without its path",
			components: component("postgresql", "        backup:\n          endpointURL: https://s3.example.com\n"),
			stage:      "transform", want: `backup.destinationPath: required`,
			answer: endpointAnswered,
		},
		{
			name:       "webservice: no image",
			components: component("webservice", "        port: 8080\n"),
			stage:      "transform", want: `required property 'image' missing or not a string`,
			answer: endpointAnswered,
		},
		{
			name:       "webservice: an image tagged latest",
			components: component("webservice", "        image: ghcr.io/example/web:latest\n"),
			stage:      "transform", want: `image "ghcr.io/example/web:latest" rejected: :latest tag not allowed`,
			answer: endpointAnswered,
		},
		{
			name:       "service: a port out of range",
			components: component("service", "        ports:\n          - port: 70000\n"),
			stage:      "transform", want: `ports[0].port: must be between 1 and 65535, got 70000`,
			answer: endpointRefused,
		},
		{
			name:       "service: an undeclared property",
			components: component("service", "        selectr:\n          app: web\n        ports:\n          - port: 80\n"),
			stage:      "validation", want: `properties: unsupported field "selectr"`,
			answer: endpointAnswered,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := helmNamesApp(tc.components)
			hook := renameBy(tc.names)
			stage, err := buildRefusal(t, doc, hook)
			if stage != tc.stage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the build refuses at %s: %v\nwant at %s, containing %q", stage, err, tc.stage, tc.want)
			}

			transformer, comp := postgresqlComponent(t, doc)
			eps, err := transformer.ComponentEndpointsNamed("shop", comp, hook)
			got := endpointAnswered
			switch {
			case err != nil && strings.Contains(err.Error(), tc.want):
				got = endpointRefused
			case err != nil:
				got = endpointRefusedOtherwise
			case len(eps) == 0:
				t.Fatal("the entry answers no endpoint and no refusal")
			}
			if got != tc.answer {
				t.Fatalf("the endpoint entry: %s (%d endpoints, err = %v)\nwant: %s", got, len(eps), err, tc.answer)
			}
			if got == endpointRefusedOtherwise && !strings.Contains(err.Error(), tc.otherwise) {
				t.Errorf("the endpoint entry refuses with %v\nwant %q", err, tc.otherwise)
			}
		})
	}
}
