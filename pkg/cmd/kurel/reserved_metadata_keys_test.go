package kurel

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// reservedKeyCarriers is one document per way a label or annotation key reaches
// the output today, through the built-in handlers and rules. %[1]s is the key
// under test, and %[2]s the chart repository's URL where the document has one.
// Each names the component, the object and the place the key lands in.
var reservedKeyCarriers = map[string]struct {
	component string // the document's one component, named "carrier"
	object    string
	what      string
}{
	"a passthrough object's label": {
		component: `    - name: carrier
      type: passthrough
      properties:
        object:
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: settings
            labels:
              %[1]s: a
`,
		object: `ConfigMap "settings"`, what: "label",
	},
	"a passthrough workload's pod template annotation": {
		component: `    - name: carrier
      type: passthrough
      properties:
        object:
          apiVersion: apps/v1
          kind: Deployment
          metadata:
            name: worker
          spec:
            selector:
              matchLabels:
                role: worker
            template:
              metadata:
                labels:
                  role: worker
                annotations:
                  %[1]s: a
              spec:
                containers:
                  - name: app
                    image: ghcr.io/example/worker:v1.0.0
`,
		object: `Deployment "worker"`, what: "pod template annotation",
	},
	"a manifests object's annotation": {
		component: `    - name: carrier
      type: manifests
      properties:
        inline: |
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: plain
            namespace: default
          ---
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: tagged
            namespace: default
            annotations:
              %[1]s: a
`,
		object: `ConfigMap "tagged"`, what: "annotation",
	},
	"a rendered chart's pod template label": {
		component: `    - name: carrier
      type: helm
      properties:
        delivery: template
        chart: testchart
        version: "0.1.0"
        source:
          url: %[2]s
        values:
          key: %[1]s
`,
		object: `Deployment "web"`, what: "pod template label",
	},
	"an ingress trait's annotation": {
		component: `    - name: carrier
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: ingress
          properties:
            rules:
              - host: shop.example.com
                paths:
                  - path: /
            annotations:
              %[1]s: a
`,
		object: `Ingress "`, what: "annotation",
	},
	"an httproute trait's annotation": {
		component: `    - name: carrier
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: httproute
          properties:
            parentRefs:
              - name: my-gateway
            rules:
              - matches:
                  - path:
                      type: PathPrefix
                      value: /
            annotations:
              %[1]s: a
`,
		object: `HTTPRoute "`, what: "annotation",
	},
	"an expose trait's annotation": {
		component: `    - name: carrier
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
        - type: expose
          properties:
            hostnames: [shop.example.com]
            annotations:
              %[1]s: a
`,
		object: `Ingress "`, what: "annotation",
	},
	"a postgresql component's inherited label": {
		component: `    - name: carrier
      type: postgresql
      properties:
        version: "16"
        storageSize: 10Gi
        inheritedMetadata:
          labels:
            %[1]s: a
`,
		object: `Cluster "carrier"`, what: "spec.inheritedMetadata label",
	},
	"a cnpg-cluster component's inherited annotation": {
		component: `    - name: carrier
      type: cnpg-cluster
      properties:
        instances: 1
        imageName: ghcr.io/cloudnative-pg/postgresql:17.2
        storage:
          size: 1Gi
        inheritedMetadata:
          annotations:
            %[1]s: a
`,
		object: `Cluster "carrier"`, what: "spec.inheritedMetadata annotation",
	},
}

// TestReservedMetadataKeys_EveryCarrier is the one rule of
// go-kure/launcher#790 held against each way a key reaches the output: a key
// the consumer reserved (TransformContext.ReservedMetadataKeys) is refused
// whatever component type or trait carries it, naming the component, the
// object, the key and the reserved entry. The same document with a key that is
// not reserved builds, so the refusal is the key's and not the fixture's.
func TestReservedMetadataKeys_EveryCarrier(t *testing.T) {
	// The chart puts the key its values name on the pod template of its
	// Deployment: a key no launcher code wrote and no property of the document
	// states as metadata.
	const pod = "      containers:\n        - name: app\n          image: ghcr.io/example/web:v1.0.0\n"
	chart := buildMinimalChartTar(t, "testchart", "0.1.0", map[string]string{
		"testchart/templates/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n" +
			"  selector:\n    matchLabels:\n      app: web\n  template:\n    metadata:\n      labels:\n        app: web\n" +
			"        {{ .Values.key }}: a\n    spec:\n" + pod,
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprint(w, helmIndexYAML("testchart", "0.1.0", "http://"+r.Host+"/testchart-0.1.0.tgz"))
		case "/testchart-0.1.0.tgz":
			_, _ = w.Write(chart)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	generate := func(t *testing.T, component, key string) error {
		t.Helper()
		doc := "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Application\nmetadata:\n  name: shop\n  namespace: default\nspec:\n  components:\n" +
			fmt.Sprintf(component, key, srv.URL)
		transformer := newBuiltinTransformer()
		app, err := oam.ParseWithExtraTypes([]byte(doc), nil, transformer.LowerableTypes())
		if err != nil {
			t.Fatalf("parsing: %v\n%s", err, doc)
		}
		if err := transformer.ValidateAuthoredProperties(app); err != nil {
			t.Fatalf("validating: %v", err)
		}
		cluster, err := transformer.Transform(app, oam.TransformContext{
			Domain:               kurelDomain,
			ReservedMetadataKeys: []string{"platform.example/", "example.org/tenant"},
			Capabilities: map[string]oam.CapabilityBinding{"expose": {Rendering: map[string]any{
				"controllerType": "ingress", "ingressClassName": "nginx",
			}}},
		})
		if err != nil {
			return err
		}
		_, err = oam.GenerateApplications(cluster)
		return err
	}

	for name, tc := range reservedKeyCarriers {
		t.Run(name, func(t *testing.T) {
			if err := generate(t, tc.component, "example.com/owner"); err != nil {
				t.Fatalf("with a key that is not reserved: %v", err)
			}
			for key, reason := range map[string]string{
				"platform.example/zone": `the prefix "platform.example/" is reserved`,
				"example.org/tenant":    "it is a reserved key",
			} {
				err := generate(t, tc.component, key)
				if !errors.Is(err, oam.ErrReservedMetadataKey) {
					t.Fatalf("with %s: %v, want ErrReservedMetadataKey", key, err)
				}
				for _, want := range []string{`component "carrier"`, tc.object, tc.what + ` "` + key + `"`, reason} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("with %s: refusal %q does not say %s", key, err, want)
					}
				}
			}
		})
	}
}
