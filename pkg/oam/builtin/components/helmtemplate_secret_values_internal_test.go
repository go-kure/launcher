package components

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack/helm"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// helmtemplate secretValues (go-kure/launcher#786): merged over values for the
// render, refused when a path is shared or a policy forbids explicit secrets,
// and kept out of every error the config returns.

// sensitiveValue is the value no error may carry.
const sensitiveValue = "s3cr3t-sentinel-value"

// noExplicitSecrets is NoopPolicy forbidding explicit secrets.
type noExplicitSecrets struct{ *oam.NoopPolicy }

func (noExplicitSecrets) AllowExplicitSecrets() bool { return false }

// secretTemplateFixture parses a helmtemplate component with values and
// secretValues through the handler and swaps its renderer for render.
func secretTemplateFixture(t *testing.T, values, secretValues map[string]any, render renderChartFunc) *HelmTemplateConfig {
	t.Helper()
	cfg, err := secretTemplateConfig(values, secretValues)
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	cfg.renderChart = render
	return cfg
}

func secretTemplateConfig(values, secretValues any) (*HelmTemplateConfig, error) {
	props := map[string]any{
		"chart":  "myapp",
		"source": map[string]any{"url": "https://charts.example.com"},
	}
	if values != nil {
		props["values"] = values
	}
	if secretValues != nil {
		props["secretValues"] = secretValues
	}
	cfg, err := (&HelmTemplateHandler{}).ToApplicationConfig(&oam.Component{Name: "myapp", Type: "helmtemplate", Properties: props}, "default")
	if err != nil {
		return nil, err
	}
	return cfg.(*HelmTemplateConfig), nil
}

// assertWithholdsSensitive fails unless err mentions wants and does not carry
// the sensitive value.
func assertWithholdsSensitive(t *testing.T, err error, wants ...string) {
	t.Helper()
	assertErrorMentions(t, err, wants...)
	if strings.Contains(err.Error(), sensitiveValue) {
		t.Errorf("error carries the sensitive value: %v", err)
	}
}

// carriesSensitive reports whether the sensitive value is anywhere in values.
func carriesSensitive(values map[string]any) bool {
	for _, v := range values {
		switch v := v.(type) {
		case string:
			if v == sensitiveValue {
				return true
			}
		case map[string]any:
			if carriesSensitive(v) {
				return true
			}
		}
	}
	return false
}

// TestHelmTemplateConfig_SecretValuesReachTheRender: the renderer receives the
// values with the secretValues merged in, objects merged key by key, every
// value with its authored type, and neither authored map is changed.
func TestHelmTemplateConfig_SecretValuesReachTheRender(t *testing.T) {
	values := map[string]any{"replicaCount": 3, "auth": map[string]any{"user": "admin"}}
	secret := map[string]any{"auth": map[string]any{"password": sensitiveValue}, "pin": 1234}
	var got map[string]any
	cfg := secretTemplateFixture(t, values, secret, func(_, _ string, v map[string]any, _ ...helm.RenderOption) ([]byte, error) {
		got = v
		return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), nil
	})
	if _, err := cfg.Generate(nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := map[string]any{
		"replicaCount": 3,
		"auth":         map[string]any{"user": "admin", "password": sensitiveValue},
		"pin":          1234,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rendered with %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(values, map[string]any{"replicaCount": 3, "auth": map[string]any{"user": "admin"}}) {
		t.Errorf("the merge changed the authored values: %#v", values)
	}
	if !reflect.DeepEqual(secret, map[string]any{"auth": map[string]any{"password": sensitiveValue}, "pin": 1234}) {
		t.Errorf("the merge changed the authored secretValues")
	}
}

// TestHelmTemplateConfig_SecretValuesOnlyInWhatTheChartRenders: the component
// emits what the chart renders and nothing of its own, so a sensitive value is
// in the output exactly when the chart put it there.
func TestHelmTemplateConfig_SecretValuesOnlyInWhatTheChartRenders(t *testing.T) {
	const quiet = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  user: admin\n"
	const leaky = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  password: " + sensitiveValue + "\n"
	for raw, wantCarried := range map[string]bool{quiet: false, leaky: true} {
		cfg := secretTemplateFixture(t, map[string]any{"user": "admin"}, map[string]any{"password": sensitiveValue}, stubRender(raw))
		objects, err := cfg.Generate(nil)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if len(objects) != 1 {
			t.Fatalf("Generate returned %d objects, want the chart's one", len(objects))
		}
		if carried := strings.Contains(writtenYAML(t, *objects[0]), sensitiveValue); carried != wantCarried {
			t.Errorf("output carries the sensitive value: %v, want %v", carried, wantCarried)
		}
	}
}

// TestHelmTemplateHandler_SecretValuesRefusals: secretValues that is not an
// object, is given twice, shares a path with values or does not encode as JSON
// is refused, naming the key, the type or the path and never a value.
func TestHelmTemplateHandler_SecretValuesRefusals(t *testing.T) {
	cases := []struct {
		name    string
		values  any
		secret  any
		extra   map[string]any
		wantErr string
	}{
		{"a string", nil, sensitiveValue, nil, "helmtemplate: secretValues: must be an object, got string"},
		{"a list", nil, []any{sensitiveValue}, nil, "helmtemplate: secretValues: must be an object, got []interface {}"},
		{"two spellings", nil, map[string]any{"a": sensitiveValue}, map[string]any{"SecretValues": map[string]any{"b": sensitiveValue}},
			"helmtemplate: secretValues is given more than once"},
		{"shared path", map[string]any{"auth": map[string]any{"password": "plain"}}, map[string]any{"auth": map[string]any{"password": sensitiveValue}}, nil,
			"helmtemplate: auth.password is set in both values and secretValues"},
		{"not JSON", nil, map[string]any{"password": sensitiveValue, "f": func() {}}, nil, "helmtemplate: secretValues is not representable as JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"chart": "myapp", "source": map[string]any{"url": "https://charts.example.com"}, "secretValues": tc.secret}
			if tc.values != nil {
				props["values"] = tc.values
			}
			for k, v := range tc.extra {
				props[k] = v
			}
			_, err := (&HelmTemplateHandler{}).ToApplicationConfig(&oam.Component{Name: "myapp", Type: "helmtemplate", Properties: props}, "default")
			assertWithholdsSensitive(t, err, tc.wantErr)
		})
	}
}

// TestHelmTemplateHandler_SecretValuesNull: a null or empty secretValues is no
// secretValues, and the values reach the render as the very map authored.
func TestHelmTemplateHandler_SecretValuesNull(t *testing.T) {
	var typedNil map[string]any
	for _, secret := range []any{typedNil, map[string]any{}} {
		values := map[string]any{"replicaCount": 3}
		cfg, err := secretTemplateConfig(values, secret)
		if err != nil {
			t.Fatalf("secretValues %#v: %v", secret, err)
		}
		var got map[string]any
		cfg.renderChart = func(_, _ string, v map[string]any, _ ...helm.RenderOption) ([]byte, error) {
			got = v
			return nil, nil
		}
		if _, err := cfg.Generate(nil); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if !reflect.DeepEqual(got, values) {
			t.Errorf("secretValues %#v: rendered with %#v, want the authored values", secret, got)
		}
	}
}

// TestHelmTemplateConfig_DirectConfigSharedPath: a config built without the
// handler is held to the same shared-path refusal when it renders.
func TestHelmTemplateConfig_DirectConfigSharedPath(t *testing.T) {
	cfg := &HelmTemplateConfig{
		Name: "myapp", SourceURL: "https://charts.example.com", Chart: "myapp",
		Values:       map[string]any{"token": "plain"},
		SecretValues: map[string]any{"token": sensitiveValue},
		renderChart:  stubRender(""),
	}
	_, err := cfg.Generate(nil)
	assertWithholdsSensitive(t, err, "helmtemplate: token is set in both values and secretValues")
}

// TestHelmTemplateConfig_SecretRenderFailureWithholdsTheCause: a render that
// fails with secretValues in play never reports that failure's own text, which
// a template error fills with the value it failed on. The chart is rendered
// once more without them: when that works, the cause is withheld; when it fails
// too, its error, which no sensitive value was part of, is the one reported.
func TestHelmTemplateConfig_SecretRenderFailureWithholdsTheCause(t *testing.T) {
	secret := map[string]any{"password": sensitiveValue}
	leaking := errors.Errorf("render templates: template: x.yaml:3: bad value %q", sensitiveValue)

	t.Run("renders without them", func(t *testing.T) {
		calls := 0
		cfg := secretTemplateFixture(t, map[string]any{"user": "admin"}, secret, func(_, _ string, v map[string]any, _ ...helm.RenderOption) ([]byte, error) {
			calls++
			if carriesSensitive(v) {
				return nil, leaking
			}
			return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), nil
		})
		_, err := cfg.Generate(nil)
		assertWithholdsSensitive(t, err, `helmtemplate "myapp": rendering chart with secretValues failed, and without them it renders`, "the cause is withheld")
		if calls != 2 {
			t.Errorf("renderChart called %d times, want 2 (with secretValues, then without)", calls)
		}
	})

	t.Run("fails without them too", func(t *testing.T) {
		cfg := secretTemplateFixture(t, nil, secret, func(_, _ string, v map[string]any, _ ...helm.RenderOption) ([]byte, error) {
			if carriesSensitive(v) {
				return nil, leaking
			}
			return nil, errors.New("pull chart: not found")
		})
		_, err := cfg.Generate(nil)
		assertWithholdsSensitive(t, err, `helmtemplate "myapp": rendering chart with secretValues failed`, "Without them it fails with",
			`helmtemplate "myapp": rendering chart: pull chart: not found`)
	})

	t.Run("rendered output does not parse", func(t *testing.T) {
		cfg := secretTemplateFixture(t, nil, secret, func(_, _ string, v map[string]any, _ ...helm.RenderOption) ([]byte, error) {
			if carriesSensitive(v) {
				return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  " + sensitiveValue + ": [unclosed\n"), nil
			}
			return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), nil
		})
		_, err := cfg.Generate(nil)
		assertWithholdsSensitive(t, err, "rendering chart with secretValues failed, and without them it renders")
	})

	t.Run("without secretValues the cause is reported", func(t *testing.T) {
		cfg := secretTemplateFixture(t, map[string]any{"user": "admin"}, nil, func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
			return nil, errors.New("render templates: boom")
		})
		_, err := cfg.Generate(nil)
		assertErrorMentions(t, err, `helmtemplate "myapp": rendering chart: render templates: boom`)
	})
}

// TestHelmTemplateConfig_ApplyPolicy_ExplicitSecrets: a policy that forbids
// explicit secrets refuses a config that sets secretValues, before anything is
// fetched; one that does not implement the optional interface allows it, and so
// does the same policy on a config without secretValues.
func TestHelmTemplateConfig_ApplyPolicy_ExplicitSecrets(t *testing.T) {
	render := func(calls *int) renderChartFunc {
		return func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
			*calls++
			return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), nil
		}
	}
	secret := map[string]any{"password": sensitiveValue}
	forbidding := noExplicitSecrets{&oam.NoopPolicy{}}

	calls := 0
	cfg := secretTemplateFixture(t, nil, secret, render(&calls))
	err := cfg.ApplyPolicy(forbidding)
	assertWithholdsSensitive(t, err, "helmtemplate: secretValues is set and the environment policy forbids explicit secrets")
	if calls != 0 {
		t.Errorf("the chart was rendered %d time(s) under a policy that refuses the component", calls)
	}

	calls = 0
	cfg = secretTemplateFixture(t, nil, secret, render(&calls))
	if err := cfg.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
		t.Errorf("a policy without the optional interface refused secretValues: %v", err)
	}

	cfg = secretTemplateFixture(t, map[string]any{"user": "admin"}, nil, render(&calls))
	if err := cfg.ApplyPolicy(forbidding); err != nil {
		t.Errorf("a config without secretValues was refused: %v", err)
	}
}

// TestSharedValuePath pins the walk both deliveries share: two objects at a key
// are compared key by key, anything else at a shared key is the shared path,
// and the first one in sorted key order is reported.
func TestSharedValuePath(t *testing.T) {
	obj := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		name   string
		a, b   map[string]any
		want   string
		shared bool
	}{
		{"nil trees", nil, nil, "", false},
		{"disjoint", obj("a", 1), obj("b", 2), "", false},
		{"disjoint under one object", obj("o", obj("a", 1)), obj("o", obj("b", 2)), "", false},
		{"scalar", obj("a", 1), obj("a", 2), "a", true},
		{"nested", obj("o", obj("p", obj("q", 1))), obj("o", obj("p", obj("q", 2))), "o.p.q", true},
		{"null against object", obj("o", nil), obj("o", obj("a", 1)), "o", true},
		{"empty objects", obj("o", obj()), obj("o", obj()), "", false},
		{"sorted", obj("z", 1, "b", 1), obj("z", 2, "b", 2), "b", true},
		// An empty key is a key: it sorts first, is a shared path, and does not
		// end the walk when the two sides only share an object under it.
		{"empty key", obj("", 1, "z", 1), obj("", 2, "z", 2), `""`, true},
		{"nested empty key", obj("o", obj("", 1)), obj("o", obj("", 2)), `o.""`, true},
		{"empty key over a later one", obj("", obj("a", 1), "z", 1), obj("", obj("b", 2), "z", 2), "z", true},
	}
	for _, tc := range cases {
		if got, shared := sharedValuePath(tc.a, tc.b); got != tc.want || shared != tc.shared {
			t.Errorf("%s: sharedValuePath = %q, %v, want %q, %v", tc.name, got, shared, tc.want, tc.shared)
		}
	}
	// The refusal reads the flag, not the path.
	for _, owner := range []string{helmType, helmTemplateType} {
		err := refuseSharedValuePath(owner, obj("", "plain"), obj("", sensitiveValue))
		if err == nil || !strings.Contains(err.Error(), owner+`: "" is set in both values and secretValues`) {
			t.Errorf("%s: err = %v, want the shared empty key refused", owner, err)
		}
		assertWithholdsSensitive(t, err)
	}
}
