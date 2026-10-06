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

// The three things an endpoint entry cannot know, each shown by one answered
// row of TestEndpoints_DocumentTheBuildRefuses.
const (
	limitSchemaCheck         = "the schema check of authored properties"
	limitPolicyAndGeneration = "what is refused once the policy is applied or at generation"
	limitDocument            = "what only the document shows"
	// Provisional, found by review: what the transform refuses of the component
	// alone after its type's own parse (a member's name, an evaluated value, the
	// parse of a kind it is lowered into).
	limitBeyondTheParse = "what the transform refuses of the component after its type's own parse"
)

// TestEndpoints_DocumentTheBuildRefuses holds every endpoint implementation to
// one rule: an endpoint entry answers only for a component its type's own
// parse accepts, in the parse's words. Each row is a document `kurel build`
// refuses, with the stage that refuses it and what the endpoint entry does
// with its component db.
//
// A row that is endpointAnswered is not a refusal of the type's parse: it names
// which of the three limits it shows. A new answered row needs one of them.
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
		// limit names which of the three things an endpoint entry cannot know
		// an answered row shows; every answered row names one.
		limit string
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
			answer: endpointAnswered, limit: limitPolicyAndGeneration,
		},
		{
			name:       "cnpg-pooler: no pgbouncer",
			components: component("cnpg-pooler", "        cluster:\n          name: main\n"),
			stage:      "transform", want: `pgbouncer: required`,
			answer: endpointRefused,
		},
		{
			name:       "cnpg-pooler: a misspelt key below a declared one",
			components: component("cnpg-pooler", "        cluster:\n          name: main\n        pgbouncer:\n          poolMod: session\n"),
			stage:      "transform", want: `properties do not decode into a postgresql.cnpg.io/v1 PoolerSpec: json: unknown field "poolMod"`,
			answer: endpointRefused,
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
			answer: endpointAnswered, limit: limitDocument,
		},
		{
			name:       "postgresql: a Database named like the component",
			components: component("postgresql", "        databases:\n          - name: orders\n            owner: app\n            objectName: db\n"),
			stage:      "transform", want: `databases[0] "orders": generates component "db", which is already the name of component "db" (type "postgresql") in the document; rename one of them`,
			answer: endpointRefused,
		},
		{
			name:       "postgresql: a backup value without its path",
			components: component("postgresql", "        backup:\n          endpointURL: https://s3.example.com\n"),
			stage:      "transform", want: `backup.destinationPath: required`,
			answer: endpointRefused,
		},
		{
			name:       "webservice: no image",
			components: component("webservice", "        port: 8080\n"),
			stage:      "transform", want: `required property 'image' missing or not a string`,
			answer: endpointRefused,
		},
		{
			name:       "webservice: an image tagged latest",
			components: component("webservice", "        image: ghcr.io/example/web:latest\n"),
			stage:      "transform", want: `image "ghcr.io/example/web:latest" rejected: :latest tag not allowed`,
			answer: endpointRefused,
		},
		{
			name:       "webservice: a Deployment name that is no name",
			components: component("webservice", "        image: ghcr.io/example/web:v1.0.0\n        deploymentObjectName: BAD_NAME\n"),
			stage:      "transform", want: `BAD_NAME`,
			answer: endpointAnswered, limit: limitBeyondTheParse,
		},
		{
			name:       "webservice: a node selector key that is no label key",
			components: component("webservice", "        image: ghcr.io/example/web:v1.0.0\n        affinity:\n          nodeSelector:\n            \"bad key!\": x\n"),
			stage:      "transform", want: `bad key!`,
			answer: endpointAnswered, limit: limitBeyondTheParse,
		},
		{
			name:       "postgresql: a database with a reserved name",
			components: component("postgresql", "        databases:\n          - name: postgres\n            owner: app\n"),
			stage:      "transform", want: `name "postgres": reserved by PostgreSQL`,
			answer: endpointAnswered, limit: limitBeyondTheParse,
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
			answer: endpointAnswered, limit: limitSchemaCheck,
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
			if (got == endpointAnswered) != (tc.limit != "") {
				t.Errorf("the endpoint entry: %s, with limit %q; an answered row names its limit and no other row has one", got, tc.limit)
			}
			if got == endpointRefusedOtherwise && !strings.Contains(err.Error(), tc.otherwise) {
				t.Errorf("the endpoint entry refuses with %v\nwant %q", err, tc.otherwise)
			}
		})
	}
}

// TestEndpoints_TypesWithAnEntry pins which component types have an endpoint
// entry: the five the table above holds to the rule. It reads the two
// registries `kurel build` registers its component types from, and asks each
// handler and rule whether it implements an endpoint interface, so a type that
// gains an entry fails here until the table has its rows.
func TestEndpoints_TypesWithAnEntry(t *testing.T) {
	withEntry := map[string]bool{"webservice": true, "service": true, "postgresql": true, "cnpg-cluster": true, "cnpg-pooler": true}
	registered := map[string]any{}
	for typ, h := range builtinComponentHandlers() {
		registered[typ] = h
	}
	for typ, r := range builtinComponentLoweringRules() {
		if _, both := registered[typ]; both {
			t.Errorf("type %q is registered as a handler and as a lowering rule", typ)
		}
		registered[typ] = r
	}
	// Not vacuous: the types checked include the workload types and the kinds
	// added last.
	for _, typ := range []string{"worker", "deployment", "endpointslice", "role", "rolebinding", "clusterrole", "clusterrolebinding"} {
		if _, ok := registered[typ]; !ok {
			t.Errorf("type %q is not among the registered types this checks", typ)
		}
	}
	found := 0
	for typ, v := range registered {
		_, plain := v.(oam.EndpointProvider)
		_, named := v.(oam.NamedEndpointProvider)
		if has := plain || named; has != withEntry[typ] {
			t.Errorf("type %q (%T): implements an endpoint interface: %t, want %t", typ, v, has, withEntry[typ])
		}
		if withEntry[typ] {
			found++
		}
	}
	if found != len(withEntry) {
		t.Errorf("%d of the %d types with an endpoint entry are registered", found, len(withEntry))
	}
}
