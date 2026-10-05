package kurel

import (
	"fmt"
	"strings"
	"testing"
)

// These tests pin the null contract for the string maps three traits read
// entry by entry (go-kure/launcher#790): an entry authored with a null value
// is absent, through the real `kurel build` entry point. Each was written into
// the object as the text "<nil>".

const nullMapValueExternalSecretProfile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    external-secret:
      rendering:
        secretStoreRef:
          name: vault-cluster-store
          kind: ClusterSecretStore
`

const nullMapValueNoCapabilityProfile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities: {}
`

// nullMapValueApp returns an Application whose one webservice component
// carries trait, the YAML of one trait entry indented for the traits list.
func nullMapValueApp(trait string) string {
	return fmt.Sprintf(`apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
  namespace: default
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
%s`, trait)
}

// nullMapValueAt walks obj along path, a list of map keys and "0" for the
// first element of a list, and returns the string map found there. A missing
// step returns nil: an object that leaves an empty map out says the same.
func nullMapValueAt(t *testing.T, obj map[string]any, path ...string) map[string]any {
	t.Helper()
	var cur any = obj
	for _, step := range path {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[step]
			if !ok {
				return nil
			}
			cur = next
		case []any:
			if step != "0" || len(v) == 0 {
				t.Fatalf("path %v: no element %s in a list of %d", path, step, len(v))
			}
			cur = v[0]
		default:
			t.Fatalf("path %v: cannot step %q into %T", path, step, cur)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok && cur != nil {
		t.Fatalf("path %v: holds %T, not a map", path, cur)
	}
	return m
}

func TestBuild_NullStringMapValueIsAbsent(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		trait   string
		kind    string
		path    []string
	}{
		{
			name:    "ingress annotations",
			profile: testClusterYAML,
			trait: `        - type: ingress
          properties:
            rules:
              - host: shop.example.com
                paths:
                  - path: /
            annotations:
              example.com/owner: null
              example.com/team: checkout
`,
			kind: "Ingress",
			path: []string{"metadata", "annotations"},
		},
		{
			name:    "httproute annotations",
			profile: nullMapValueNoCapabilityProfile,
			trait: `        - type: httproute
          properties:
            parentRefs:
              - name: my-gateway
            rules:
              - matches:
                  - path:
                      type: PathPrefix
                      value: /
            annotations:
              example.com/owner: null
              example.com/team: checkout
`,
			kind: "HTTPRoute",
			path: []string{"metadata", "annotations"},
		},
		{
			name:    "external-secret target template data",
			profile: nullMapValueExternalSecretProfile,
			trait: `        - type: external-secret
          properties:
            secretName: web-credentials
            target:
              template:
                data:
                  example.com/owner: null
                  example.com/team: checkout
            data:
              - secretKey: DB_PASSWORD
                remoteRef:
                  key: prod/web/db
                  property: password
`,
			kind: "ExternalSecret",
			path: []string{"spec", "target", "template", "data"},
		},
		{
			name:    "external-secret dataFrom find tags",
			profile: nullMapValueExternalSecretProfile,
			trait: `        - type: external-secret
          properties:
            secretName: web-credentials
            dataFrom:
              - find:
                  tags:
                    example.com/owner: null
                    example.com/team: checkout
`,
			kind: "ExternalSecret",
			path: []string{"spec", "dataFrom", "0", "find", "tags"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docs, stderr, err := buildPVCDocsWithProfile(t, nullMapValueApp(tc.trait), tc.profile)
			if err != nil {
				t.Fatalf("kurel build: %v\n%s", err, stderr)
			}
			var obj map[string]any
			for _, d := range docs {
				if d["kind"] == tc.kind {
					obj = d
				}
			}
			if obj == nil {
				t.Fatalf("no %s among %d documents", tc.kind, len(docs))
			}
			got := nullMapValueAt(t, obj, tc.path...)
			if v, written := got["example.com/owner"]; written {
				t.Errorf("%s %s holds example.com/owner: %#v; a null entry is absent", tc.kind, strings.Join(tc.path, "."), v)
			}
			if v := got["example.com/team"]; v != "checkout" {
				t.Errorf("%s %s holds example.com/team: %#v, want %q", tc.kind, strings.Join(tc.path, "."), v, "checkout")
			}
		})
	}
}

// TestBuild_OnlyNullTagsIsNoTags: tags holding nothing but null entries are an
// empty map, so a find with no name beside them names neither and is refused
// as one with no tags at all is.
func TestBuild_OnlyNullTagsIsNoTags(t *testing.T) {
	trait := `        - type: external-secret
          properties:
            secretName: web-credentials
            dataFrom:
              - find:
                  tags:
                    example.com/owner: null
`
	_, _, err := buildPVCDocsWithProfile(t, nullMapValueApp(trait), nullMapValueExternalSecretProfile)
	if err == nil {
		t.Fatal("kurel build succeeded; a find with only null tags and no name names nothing")
	}
	if want := "dataFrom[0].find: must have at least 'name' or 'tags'"; !strings.Contains(err.Error(), want) {
		t.Errorf("kurel build error = %q, want it to contain %q", err, want)
	}
}
