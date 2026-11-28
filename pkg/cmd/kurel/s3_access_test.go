package kurel

import (
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/shipped"
)

// These tests pin the s3-access model that launcher ships in the binary
// (go-kure/launcher#1738): one ConfigMap of settings and, by the credentials route the
// application picks, a Secret, an ExternalSecret or nothing, with the keys the format names.

const (
	s3Type     = "s3-access.launcher.gokure.dev@v1"
	s3Endpoint = "https://s3.example.internal:9000"
	// Fixtures, not credentials.
	s3KeyID  = "AKIDEXAMPLE"
	s3Secret = "not-a-real-secret"
	s3Pass   = "not-a-real-password"
)

// s3Props is the component's properties as a flow mapping: the settings of every test and
// the credentials the case brings.
func s3Props(credentials string) string {
	return `{endpoint: "` + s3Endpoint + `", bucket: backups, prefix: orders, credentials: ` + credentials + `}`
}

// s3Objects decodes a build's documents by "<kind>/<name>".
func s3Objects(t *testing.T, out string) map[string]map[string]any {
	t.Helper()
	objects := map[string]map[string]any{}
	for _, raw := range strings.Split(out, "\n---\n") {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding an output document: %v\n%s", err, raw)
		}
		if obj == nil {
			continue
		}
		meta, _ := obj["metadata"].(map[string]any)
		objects[fmt.Sprintf("%v/%v", obj["kind"], meta["name"])] = obj
	}
	return objects
}

// s3Build builds an application of one s3-access component and returns its objects.
func s3Build(t *testing.T, props string) map[string]map[string]any {
	t.Helper()
	dir := modelPackage(t, modelApp(s3Type, props), nil)
	out, err := buildModelPackage(t, dir)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return s3Objects(t, out)
}

// s3Strings reads obj[field] as a mapping of strings, base64-decoding the values of a Secret.
func s3Strings(t *testing.T, obj map[string]any, field string, decode bool) map[string]string {
	t.Helper()
	raw, _ := obj[field].(map[string]any)
	got := map[string]string{}
	for k, v := range raw {
		s, _ := v.(string)
		if decode {
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				t.Fatalf("%s[%s] is not base64: %v", field, k, err)
			}
			s = string(b)
		}
		got[k] = s
	}
	return got
}

func s3Want(t *testing.T, objects map[string]map[string]any, key string) map[string]any {
	t.Helper()
	obj, ok := objects[key]
	if !ok {
		names := make([]string, 0, len(objects))
		for k := range objects {
			names = append(names, k)
		}
		t.Fatalf("no %s among %v", key, names)
	}
	return obj
}

// s3Shapes is the keys each format writes, for the credentials the caller passes: the
// values themselves on the secret route and the template tokens on the external-secret one.
func s3Shapes(id, secret, password string) map[string]map[string]string {
	return map[string]map[string]string{
		"plain": {
			"access-key-id": id, "secret-access-key": secret,
			"endpoint": s3Endpoint, "bucket-name": "backups",
		},
		"aws-env": {
			"AWS_ACCESS_KEY_ID": id, "AWS_SECRET_ACCESS_KEY": secret,
			"AWS_REGION": "us-east-1", "AWS_ENDPOINT_URL_S3": s3Endpoint,
		},
		"credentials-file": {
			"credentials": "[default]\naws_access_key_id = " + id + "\naws_secret_access_key = " + secret + "\n",
		},
		"restic": {
			"RESTIC_REPOSITORY": "s3:" + s3Endpoint + "/backups/orders", "RESTIC_PASSWORD": password,
			"AWS_ACCESS_KEY_ID": id, "AWS_SECRET_ACCESS_KEY": secret,
		},
		"barman": {"ACCESS_KEY_ID": id, "ACCESS_SECRET_KEY": secret},
		"custom": {"MY_ID": id, "MY_SECRET": secret},
	}
}

var s3FormatNames = []string{"plain", "aws-env", "credentials-file", "restic", "barman", "custom"}

const s3CustomKeys = `keys: {accessKeyId: MY_ID, secretAccessKey: MY_SECRET}`

// s3ValuesCredentials is the secret route for one format.
func s3ValuesCredentials(format string) string {
	extra := ""
	switch format {
	case "restic":
		extra = ", resticPassword: " + s3Pass
	case "custom":
		extra = ", " + s3CustomKeys
	}
	return `{values: {accessKeyId: ` + s3KeyID + `, secretAccessKey: ` + s3Secret + `, format: ` + format + extra + `}}`
}

// s3ExternalCredentials is the external-secret route for one format.
func s3ExternalCredentials(format string) string {
	extra := ""
	switch format {
	case "restic":
		extra = ", resticPassword: {key: s3/restic, property: password}"
	case "custom":
		extra = ", " + s3CustomKeys
	}
	return `{externalSecret: {storeRef: {name: vault}, accessKeyId: {key: s3/backups, property: access}, ` +
		`secretAccessKey: {key: s3/backups, property: secret}, format: ` + format + extra + `}}`
}

// TestS3AccessKnownWithEmptyAppDir builds an application whose directory holds app.yaml
// only: the model is shipped, so no models/ directory is needed. An existing Secret gets
// the ConfigMap and nothing else.
func TestS3AccessKnownWithEmptyAppDir(t *testing.T) {
	objects := s3Build(t, s3Props(`{existingSecret: s3-creds}`))
	if len(objects) != 1 {
		t.Errorf("want the ConfigMap only, got %d objects", len(objects))
	}
	cm := s3Want(t, objects, "ConfigMap/orders-settings")
	want := map[string]string{
		"endpoint": s3Endpoint, "region": "us-east-1", "bucket-name": "backups", "prefix": "orders",
		"path-style": "true", "ca-secret-name": "", "ca-secret-key": "", "credentials-secret": "s3-creds",
	}
	if got := s3Strings(t, cm, "data", false); !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigMap data = %v, want %v", got, want)
	}
}

// TestS3AccessSettings: an authored region, path style, prefix and CA Secret reach the
// ConfigMap, and no prefix gives a restic repository without a trailing path.
func TestS3AccessSettings(t *testing.T) {
	props := `{endpoint: "` + s3Endpoint + `", region: garage, bucket: backups, pathStyle: false, ` +
		`caSecret: {name: s3-ca}, credentials: {values: {accessKeyId: ` + s3KeyID + `, secretAccessKey: ` + s3Secret + `, ` +
		`format: restic, resticPassword: ` + s3Pass + `}}}`
	objects := s3Build(t, props)
	want := map[string]string{
		"endpoint": s3Endpoint, "region": "garage", "bucket-name": "backups", "prefix": "",
		"path-style": "false", "ca-secret-name": "s3-ca", "ca-secret-key": "ca.crt", "credentials-secret": "orders-explicit",
	}
	if got := s3Strings(t, s3Want(t, objects, "ConfigMap/orders-settings"), "data", false); !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigMap data = %v, want %v", got, want)
	}
	secret := s3Strings(t, s3Want(t, objects, "Secret/orders-explicit"), "data", true)
	if got, want := secret["RESTIC_REPOSITORY"], "s3:"+s3Endpoint+"/backups"; got != want {
		t.Errorf("RESTIC_REPOSITORY = %q, want %q", got, want)
	}
}

// TestS3AccessValuesRouteFormats: each format writes its keys into the Secret.
func TestS3AccessValuesRouteFormats(t *testing.T) {
	for _, format := range s3FormatNames {
		t.Run(format, func(t *testing.T) {
			objects := s3Build(t, s3Props(s3ValuesCredentials(format)))
			secret := s3Want(t, objects, "Secret/orders-explicit")
			if got, want := s3Strings(t, secret, "data", true), s3Shapes(s3KeyID, s3Secret, s3Pass)[format]; !reflect.DeepEqual(got, want) {
				t.Errorf("Secret keys = %v, want %v", got, want)
			}
			cm := s3Want(t, objects, "ConfigMap/orders-settings")
			if got := s3Strings(t, cm, "data", false)["credentials-secret"]; got != "orders-explicit" {
				t.Errorf("credentials-secret = %q, want orders-explicit", got)
			}
			for key := range objects {
				if strings.HasPrefix(key, "ExternalSecret/") {
					t.Errorf("the secret route emitted %s", key)
				}
			}
		})
	}
}

// TestS3AccessValuesRouteDefaultFormatIsPlain: no format is the plain one.
func TestS3AccessValuesRouteDefaultFormatIsPlain(t *testing.T) {
	objects := s3Build(t, s3Props(`{values: {accessKeyId: `+s3KeyID+`, secretAccessKey: `+s3Secret+`}}`))
	secret := s3Want(t, objects, "Secret/orders-explicit")
	if got, want := s3Strings(t, secret, "data", true), s3Shapes(s3KeyID, s3Secret, s3Pass)["plain"]; !reflect.DeepEqual(got, want) {
		t.Errorf("Secret keys = %v, want %v", got, want)
	}
}

// TestS3AccessExternalSecretRouteFormats: each format writes its keys as the ExternalSecret's
// template, over the two remote references (and the restic password's third).
func TestS3AccessExternalSecretRouteFormats(t *testing.T) {
	for _, format := range s3FormatNames {
		t.Run(format, func(t *testing.T) {
			objects := s3Build(t, s3Props(s3ExternalCredentials(format)))
			es := s3Want(t, objects, "ExternalSecret/orders-external")
			spec, _ := es["spec"].(map[string]any)
			target, _ := spec["target"].(map[string]any)
			template, _ := target["template"].(map[string]any)
			want := s3Shapes("{{ .accessKeyId }}", "{{ .secretAccessKey }}", "{{ .resticPassword }}")[format]
			if got := s3Strings(t, template, "data", false); !reflect.DeepEqual(got, want) {
				t.Errorf("template data = %v, want %v", got, want)
			}
			data, _ := spec["data"].([]any)
			wantEntries := 2
			if format == "restic" {
				wantEntries = 3
			}
			if len(data) != wantEntries {
				t.Errorf("%d data entries, want %d: %v", len(data), wantEntries, data)
			}
			store, _ := spec["secretStoreRef"].(map[string]any)
			if store["name"] != "vault" || store["kind"] != "ClusterSecretStore" {
				t.Errorf("secretStoreRef = %v, want vault of kind ClusterSecretStore", store)
			}
			cm := s3Want(t, objects, "ConfigMap/orders-settings")
			if got := s3Strings(t, cm, "data", false)["credentials-secret"]; got != "orders-external" {
				t.Errorf("credentials-secret = %q, want orders-external", got)
			}
			for key := range objects {
				if strings.HasPrefix(key, "Secret/") {
					t.Errorf("the external-secret route emitted %s", key)
				}
			}
		})
	}
}

// TestS3AccessRefusals: what the model's schema refuses, with the refusal naming the input.
func TestS3AccessRefusals(t *testing.T) {
	values := `values: {accessKeyId: ` + s3KeyID + `, secretAccessKey: ` + s3Secret + `}`
	tests := []struct {
		name, credentials string
		want              []string
	}{
		// The wanted text is the model's own refusal, which a test's temporary directory name
		// (it holds the case name) cannot spell.
		{"two sources", `{existingSecret: s3-creds, ` + values + `}`, []string{`properties.credentials: "existingSecret" and "values" are mutually exclusive`}},
		{"no source", `{}`, []string{`properties.credentials: exactly one of "existingSecret", "externalSecret" and "values" is required`}},
		{"format over an existing Secret", `{existingSecret: s3-creds, format: aws-env}`, []string{`properties.credentials: unsupported field "format"`}},
		{"unknown format", `{values: {accessKeyId: a, secretAccessKey: b, format: yaml}}`, []string{"properties.credentials.values.format: value yaml not in allowed set"}},
		{"restic without a password", `{values: {accessKeyId: a, secretAccessKey: b, format: restic}}`, []string{`"resticPassword" is required when "format" is "restic"`}},
		{"custom without keys", `{values: {accessKeyId: a, secretAccessKey: b, format: custom}}`, []string{`"keys" is required when "format" is "custom"`}},
		{"restic over the external secret without a password", `{externalSecret: {storeRef: {name: v}, accessKeyId: {key: k}, secretAccessKey: {key: k}, format: restic}}`, []string{`"resticPassword" is required when "format" is "restic"`}},
		{"external secret without a store", `{externalSecret: {accessKeyId: {key: k}, secretAccessKey: {key: k}}}`, []string{`properties.credentials.externalSecret: "storeRef" is required`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := modelPackage(t, modelApp(s3Type, s3Props(tt.credentials)), nil)
			_, err := buildModelPackage(t, dir)
			requireRefused(t, err, tt.want...)
		})
	}
}

// s3Transform runs the shipped model through the built-in pipeline under a policy.
func s3Transform(t *testing.T, policy oam.Policy, credentials string) error {
	t.Helper()
	tr := newBuiltinTransformer()
	if _, err := shipped.Load(tr); err != nil {
		t.Fatal(err)
	}
	app, err := oam.ParseWithExtraTypes([]byte(modelApp(s3Type, s3Props(credentials))), nil, tr.LowerableTypes())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = tr.TransformWithPolicy(app, oam.TransformContext{Domain: kurelDomain, Policy: policy})
	return err
}

// TestS3AccessExplicitSecretPolicy: a policy that forbids explicit secrets refuses the route
// that writes a Secret, with the explicit-secret class, and leaves the two others open.
func TestS3AccessExplicitSecretPolicy(t *testing.T) {
	forbid := noExplicitSecrets{&oam.NoopPolicy{}}

	err := s3Transform(t, forbid, s3ValuesCredentials("plain"))
	var v *oam.ViolationError
	if !errors.As(err, &v) {
		t.Fatalf("the secret route under a policy that forbids explicit secrets: err = %v, want a violation", err)
	}
	if v.Class != oam.RefusalExplicitSecret {
		t.Errorf("violation class %q, want %q: %v", v.Class, oam.RefusalExplicitSecret, err)
	}

	for name, credentials := range map[string]string{
		"external-secret": s3ExternalCredentials("aws-env"),
		"existing Secret": `{existingSecret: s3-creds}`,
	} {
		if err := s3Transform(t, forbid, credentials); err != nil {
			t.Errorf("the %s route under the same policy: %v", name, err)
		}
	}

	// The control: the same document builds where the policy allows explicit secrets.
	if err := s3Transform(t, &oam.NoopPolicy{}, s3ValuesCredentials("plain")); err != nil {
		t.Errorf("the secret route under a policy that allows them: %v", err)
	}
}

// TestS3AccessShippedIdentityIsRefused: an application's model of the shipped identity is
// refused, naming both files, and a model of another version is another type.
func TestS3AccessShippedIdentityIsRefused(t *testing.T) {
	model := func(version string) string {
		return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: s3-access.launcher.gokure.dev
  version: ` + version + `
spec:
  celEnv: "1"
  outputs: {}
  parts:
    - id: cm
      type: configmap@v1
      properties:
        data: {k: v}
`
	}
	dir := modelPackage(t, modelApp(s3Type, "{}"), map[string]string{"models/mine.yaml": model("v1")})
	_, err := buildModelPackage(t, dir)
	requireRefused(t, err, "mine.yaml", "shipped", "s3-access.launcher.gokure.dev@v1.0.0", "model s3-access.yaml")

	dir = modelPackage(t, modelApp("s3-access.launcher.gokure.dev@v2", "{}"), map[string]string{"models/mine.yaml": model("v2")})
	out, err := buildModelPackage(t, dir)
	requireBuilt(t, out, err, "kind: ConfigMap")
}

// TestS3AccessExample builds the example application: the external-secret route.
func TestS3AccessExample(t *testing.T) {
	out, err := runKurel(t, "build", "../../../examples/s3-access",
		"--profile", "../../../examples/cluster-profiles/minimal.yaml")
	requireBuilt(t, out, err, "kind: ConfigMap", "kind: ExternalSecret")
}
