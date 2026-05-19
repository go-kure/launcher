package traits

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// captureHandler captures slog records for assertion in tests.
type captureHandler struct {
	records *[]slog.Record
}

func (c *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (c *captureHandler) Handle(_ context.Context, r slog.Record) error {
	*c.records = append(*c.records, r)
	return nil
}
func (c *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return c }
func (c *captureHandler) WithGroup(_ string) slog.Handler      { return c }

func TestHTTPRouteHandler_CanHandle(t *testing.T) {
	h := &HTTPRouteHandler{}
	tests := []struct {
		traitType string
		want      bool
	}{
		{"httproute", true},
		{"ingress", false},
		{"expose", false},
		{"configmap", false},
		{"unknown", false},
	}
	for _, tc := range tests {
		t.Run(tc.traitType, func(t *testing.T) {
			got := h.CanHandle(tc.traitType)
			if got != tc.want {
				t.Errorf("CanHandle(%q) = %v, want %v", tc.traitType, got, tc.want)
			}
		})
	}
}

func TestHTTPRouteHandler_Apply_EmitsDeprecationWarning(t *testing.T) {
	var records []slog.Record
	capturingHandler := &captureHandler{records: &records}
	old := slog.Default()
	slog.SetDefault(slog.New(capturingHandler))
	t.Cleanup(func() { slog.SetDefault(old) })

	h := &HTTPRouteHandler{}
	app := stack.NewApplication("mycomp", "default", nil)
	trait := &oam.Trait{
		Type: "httproute",
		Properties: map[string]any{
			"parentRefs": []any{
				map[string]any{"name": "my-gateway"},
			},
			"rules": []any{
				map[string]any{},
			},
		},
	}
	bundle := &stack.Bundle{}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected slog.Warn to be called, but no records captured")
	}
	r := records[0]
	if r.Level != slog.LevelWarn {
		t.Errorf("expected Warn level, got %v", r.Level)
	}
	if !strings.Contains(r.Message, "httproute trait is deprecated") {
		t.Errorf("unexpected deprecation message: %q", r.Message)
	}
}

func TestHTTPRouteHandler_MissingParentRefs(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", nil)
	_, err := h.parseProperties(map[string]any{
		"rules": []any{map[string]any{}},
	}, app)
	if err == nil {
		t.Fatal("expected error for missing parentRefs")
	}
	if !strings.Contains(err.Error(), "parentRefs") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestHTTPRouteHandler_MissingRules(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", nil)
	_, err := h.parseProperties(map[string]any{
		"parentRefs": []any{map[string]any{"name": "gw"}},
	}, app)
	if err == nil {
		t.Fatal("expected error for missing rules")
	}
	if !strings.Contains(err.Error(), "rules") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestHTTPRouteHandler_ParentRefs_MissingName(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", nil)
	_, err := h.parseProperties(map[string]any{
		"parentRefs": []any{map[string]any{"namespace": "gw-system"}},
		"rules":      []any{map[string]any{}},
	}, app)
	if err == nil {
		t.Fatal("expected error for missing parentRef name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestHTTPRouteHandler_Apply_SubAppNaming(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", nil)
	bundle := &stack.Bundle{}

	trait := &oam.Trait{
		Type: "httproute",
		Properties: map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules":      []any{map[string]any{}},
		},
	}
	_ = slog.Default() // suppress deprecation log
	old := slog.Default()
	slog.SetDefault(slog.New(&captureHandler{records: new([]slog.Record)}))
	t.Cleanup(func() { slog.SetDefault(old) })

	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 sub-app, got %d", len(bundle.Applications))
	}
	if bundle.Applications[0].Name != "web-httproute" {
		t.Errorf("sub-app name = %q, want \"web-httproute\"", bundle.Applications[0].Name)
	}

	// With custom name override
	bundle2 := &stack.Bundle{}
	trait2 := &oam.Trait{
		Type: "httproute",
		Properties: map[string]any{
			"name":       "my-custom-route",
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules":      []any{map[string]any{}},
		},
	}
	if err := h.Apply(trait2, app, bundle2); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle2.Applications[0].Name != "my-custom-route" {
		t.Errorf("sub-app name = %q, want \"my-custom-route\"", bundle2.Applications[0].Name)
	}
}

func TestHTTPRouteConfig_Generate_Basic(t *testing.T) {
	cfg := &HTTPRouteConfig{
		ComponentName: "web",
		ParentRefs:    []ParentRef{{Name: "my-gateway"}},
		Rules: []HTTPRouteRule{
			{BackendRefs: []BackendRef{{Name: "web", Port: 80}}},
		},
	}
	app := stack.NewApplication("web-httproute", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	route := *objs[0]
	labels := route.GetLabels()
	if labels["app"] != "web" {
		t.Errorf("labels[app] = %q, want \"web\" (component name, not sub-app name)", labels["app"])
	}
}

// baseRule is a helper that wraps filters in a valid parentRefs+rules structure.
func baseRule(filters []any) map[string]any {
	return map[string]any{
		"parentRefs": []any{map[string]any{"name": "gw"}},
		"rules": []any{
			map[string]any{"filters": filters},
		},
	}
}

// TestHTTPRouteHandler_ParseFilters covers the discriminated-union filter parser.
func TestHTTPRouteHandler_ParseFilters(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", nil)

	cases := []struct {
		name         string
		filters      []any
		wantErr      string
		validateRule func(t *testing.T, rule HTTPRouteRule)
	}{
		{
			name: "RequestRedirect scheme+statusCode",
			filters: []any{
				map[string]any{
					"type": "RequestRedirect",
					"requestRedirect": map[string]any{
						"scheme":     "https",
						"statusCode": 308,
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				if len(rule.Filters) != 1 || rule.Filters[0].Type != "RequestRedirect" {
					t.Fatalf("filters = %+v", rule.Filters)
				}
				rr := rule.Filters[0].RequestRedirect
				if rr == nil || rr.Scheme != "https" || rr.StatusCode == nil || *rr.StatusCode != 308 {
					t.Errorf("RequestRedirect = %+v", rr)
				}
			},
		},
		{
			name: "RequestRedirect no fields → error",
			filters: []any{
				map[string]any{
					"type":            "RequestRedirect",
					"requestRedirect": map[string]any{},
				},
			},
			wantErr: "at least one of scheme/hostname/port/statusCode/path",
		},
		{
			name: "RequestRedirect invalid scheme",
			filters: []any{
				map[string]any{
					"type": "RequestRedirect",
					"requestRedirect": map[string]any{
						"scheme": "ftp",
					},
				},
			},
			wantErr: `scheme "ftp" must be "http" or "https"`,
		},
		{
			name: "RequestRedirect invalid statusCode",
			filters: []any{
				map[string]any{
					"type": "RequestRedirect",
					"requestRedirect": map[string]any{
						"statusCode": 404,
					},
				},
			},
			wantErr: "statusCode 404 must be one of [301,302,303,307,308]",
		},
		{
			name: "ResponseHeaderModifier set",
			filters: []any{
				map[string]any{
					"type": "ResponseHeaderModifier",
					"responseHeaderModifier": map[string]any{
						"set": []any{
							map[string]any{"name": "Strict-Transport-Security", "value": "max-age=31536000"},
						},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				hm := rule.Filters[0].ResponseHeaderModifier
				if hm == nil || len(hm.Set) != 1 {
					t.Fatalf("ResponseHeaderModifier = %+v", hm)
				}
				if hm.Set[0].Name != "Strict-Transport-Security" || hm.Set[0].Value != "max-age=31536000" {
					t.Errorf("Set[0] = %+v", hm.Set[0])
				}
			},
		},
		{
			name: "ResponseHeaderModifier remove",
			filters: []any{
				map[string]any{
					"type": "ResponseHeaderModifier",
					"responseHeaderModifier": map[string]any{
						"remove": []any{"X-Forwarded-For", "X-Forwarded-Host"},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				hm := rule.Filters[0].ResponseHeaderModifier
				if hm == nil || len(hm.Remove) != 2 {
					t.Fatalf("Remove = %+v", hm)
				}
			},
		},
		{
			name: "ResponseHeaderModifier all empty → error",
			filters: []any{
				map[string]any{
					"type":                   "ResponseHeaderModifier",
					"responseHeaderModifier": map[string]any{},
				},
			},
			wantErr: "at least one of set/add/remove",
		},
		{
			name: "RequestHeaderModifier add",
			filters: []any{
				map[string]any{
					"type": "RequestHeaderModifier",
					"requestHeaderModifier": map[string]any{
						"add": []any{
							map[string]any{"name": "X-Request-Id", "value": "abc"},
						},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				hm := rule.Filters[0].RequestHeaderModifier
				if hm == nil || len(hm.Add) != 1 {
					t.Fatalf("Add = %+v", hm)
				}
			},
		},
		{
			name: "URLRewrite ReplacePrefixMatch",
			filters: []any{
				map[string]any{
					"type": "URLRewrite",
					"urlRewrite": map[string]any{
						"path": map[string]any{
							"type":               "ReplacePrefixMatch",
							"replacePrefixMatch": "/v2",
						},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				rw := rule.Filters[0].URLRewrite
				if rw == nil || rw.Path == nil || rw.Path.ReplacePrefixMatch != "/v2" {
					t.Errorf("URLRewrite = %+v", rw)
				}
			},
		},
		{
			name: "URLRewrite ReplaceFullPath missing value",
			filters: []any{
				map[string]any{
					"type": "URLRewrite",
					"urlRewrite": map[string]any{
						"path": map[string]any{"type": "ReplaceFullPath"},
					},
				},
			},
			wantErr: "replaceFullPath is required",
		},
		{
			name: "URLRewrite no fields → error",
			filters: []any{
				map[string]any{
					"type":       "URLRewrite",
					"urlRewrite": map[string]any{},
				},
			},
			wantErr: "at least one of hostname or path",
		},
		{
			name: "RequestMirror basic",
			filters: []any{
				map[string]any{
					"type": "RequestMirror",
					"requestMirror": map[string]any{
						"backendRef": map[string]any{"name": "mirror-svc", "port": 8080},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				m := rule.Filters[0].RequestMirror
				if m == nil || m.BackendRef.Name != "mirror-svc" || m.BackendRef.Port != 8080 {
					t.Errorf("RequestMirror = %+v", m)
				}
				if m.Percent != nil || m.Fraction != nil {
					t.Errorf("expected no percent/fraction, got %+v", m)
				}
			},
		},
		{
			name: "RequestMirror with percent",
			filters: []any{
				map[string]any{
					"type": "RequestMirror",
					"requestMirror": map[string]any{
						"backendRef": map[string]any{"name": "mirror-svc", "port": 8080},
						"percent":    50,
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				m := rule.Filters[0].RequestMirror
				if m == nil || m.Percent == nil || *m.Percent != 50 {
					t.Errorf("percent = %+v", m)
				}
			},
		},
		{
			name: "RequestMirror with fraction",
			filters: []any{
				map[string]any{
					"type": "RequestMirror",
					"requestMirror": map[string]any{
						"backendRef": map[string]any{"name": "mirror-svc", "port": 8080},
						"fraction":   map[string]any{"numerator": 1, "denominator": 4},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				m := rule.Filters[0].RequestMirror
				if m == nil || m.Fraction == nil || m.Fraction.Numerator != 1 {
					t.Errorf("fraction = %+v", m)
				}
			},
		},
		{
			name: "RequestMirror missing backendRef → error",
			filters: []any{
				map[string]any{
					"type":          "RequestMirror",
					"requestMirror": map[string]any{},
				},
			},
			wantErr: "requestMirror.backendRef is required",
		},
		{
			name: "RequestMirror percent and fraction both set → error",
			filters: []any{
				map[string]any{
					"type": "RequestMirror",
					"requestMirror": map[string]any{
						"backendRef": map[string]any{"name": "svc", "port": 80},
						"percent":    50,
						"fraction":   map[string]any{"numerator": 1},
					},
				},
			},
			wantErr: "only one of percent or fraction",
		},
		{
			name: "RequestMirror percent out of range → error",
			filters: []any{
				map[string]any{
					"type": "RequestMirror",
					"requestMirror": map[string]any{
						"backendRef": map[string]any{"name": "svc", "port": 80},
						"percent":    101,
					},
				},
			},
			wantErr: "percent 101 must be in [0,100]",
		},
		{
			name: "CORS basic",
			filters: []any{
				map[string]any{
					"type": "CORS",
					"cors": map[string]any{
						"allowOrigins": []any{"https://example.com"},
						"allowMethods": []any{"GET", "POST"},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				c := rule.Filters[0].CORS
				if c == nil || len(c.AllowOrigins) != 1 || c.AllowOrigins[0] != "https://example.com" {
					t.Errorf("CORS = %+v", c)
				}
				if len(c.AllowMethods) != 2 {
					t.Errorf("AllowMethods = %+v", c.AllowMethods)
				}
			},
		},
		{
			name: "CORS with credentials and maxAge",
			filters: []any{
				map[string]any{
					"type": "CORS",
					"cors": map[string]any{
						"allowCredentials": true,
						"maxAge":           3600,
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				c := rule.Filters[0].CORS
				if c == nil || c.AllowCredentials == nil || !*c.AllowCredentials {
					t.Errorf("allowCredentials = %+v", c)
				}
				if c.MaxAge == nil || *c.MaxAge != 3600 {
					t.Errorf("maxAge = %+v", c.MaxAge)
				}
			},
		},
		{
			name: "CORS empty block → error",
			filters: []any{
				map[string]any{
					"type": "CORS",
					"cors": map[string]any{},
				},
			},
			wantErr: "cors block must set at least one field",
		},
		{
			name: "CORS missing block → error",
			filters: []any{
				map[string]any{"type": "CORS"},
			},
			wantErr: "cors block is required",
		},
		{
			name: "ExternalAuth HTTP",
			filters: []any{
				map[string]any{
					"type": "ExternalAuth",
					"externalAuth": map[string]any{
						"protocol":   "HTTP",
						"backendRef": map[string]any{"name": "authz-svc", "port": 9090},
						"http": map[string]any{
							"path":           "/check",
							"allowedHeaders": []any{"X-User-Id"},
						},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				ea := rule.Filters[0].ExternalAuth
				if ea == nil || ea.Protocol != "HTTP" || ea.BackendRef.Name != "authz-svc" {
					t.Errorf("ExternalAuth = %+v", ea)
				}
				if ea.HTTP == nil || ea.HTTP.Path != "/check" {
					t.Errorf("HTTP = %+v", ea.HTTP)
				}
			},
		},
		{
			name: "ExternalAuth GRPC",
			filters: []any{
				map[string]any{
					"type": "ExternalAuth",
					"externalAuth": map[string]any{
						"protocol":   "GRPC",
						"backendRef": map[string]any{"name": "authz-svc", "port": 9090},
						"grpc": map[string]any{
							"allowedHeaders": []any{"X-Request-Id"},
						},
					},
				},
			},
			validateRule: func(t *testing.T, rule HTTPRouteRule) {
				t.Helper()
				ea := rule.Filters[0].ExternalAuth
				if ea == nil || ea.Protocol != "GRPC" {
					t.Errorf("ExternalAuth = %+v", ea)
				}
				if ea.GRPC == nil || len(ea.GRPC.AllowedHeaders) != 1 {
					t.Errorf("GRPC = %+v", ea.GRPC)
				}
			},
		},
		{
			name: "ExternalAuth invalid protocol → error",
			filters: []any{
				map[string]any{
					"type": "ExternalAuth",
					"externalAuth": map[string]any{
						"protocol":   "UNKNOWN",
						"backendRef": map[string]any{"name": "svc", "port": 80},
					},
				},
			},
			wantErr: `protocol must be "HTTP" or "GRPC"`,
		},
		{
			name: "ExternalAuth missing backendRef → error",
			filters: []any{
				map[string]any{
					"type": "ExternalAuth",
					"externalAuth": map[string]any{
						"protocol": "HTTP",
					},
				},
			},
			wantErr: "externalAuth.backendRef is required",
		},
		{
			name: "missing type",
			filters: []any{
				map[string]any{"requestRedirect": map[string]any{"scheme": "https"}},
			},
			wantErr: "type is required",
		},
		{
			name: "ExtensionRef deferred",
			filters: []any{
				map[string]any{"type": "ExtensionRef"},
			},
			wantErr: `filter type "ExtensionRef" is not implemented`,
		},
		{
			name: "unknown type",
			filters: []any{
				map[string]any{"type": "Bogus"},
			},
			wantErr: `unknown filter type "Bogus"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := h.parseProperties(baseRule(tc.filters), app)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.validateRule != nil {
				tc.validateRule(t, cfg.Rules[0])
			}
		})
	}
}

// TestHTTPRouteHandler_ParseTimeouts covers the timeout parser.
func TestHTTPRouteHandler_ParseTimeouts(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", nil)

	baseTimeoutRule := func(timeouts map[string]any) map[string]any {
		return map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{
				map[string]any{"timeouts": timeouts},
			},
		}
	}

	t.Run("30s request", func(t *testing.T) {
		cfg, err := h.parseProperties(baseTimeoutRule(map[string]any{"request": "30s"}), app)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		to := cfg.Rules[0].Timeouts
		if to == nil || to.Request.String() != "30s" {
			t.Errorf("Timeouts.Request = %v, want 30s", to)
		}
	})

	t.Run("5m request", func(t *testing.T) {
		cfg, err := h.parseProperties(baseTimeoutRule(map[string]any{"request": "5m"}), app)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Rules[0].Timeouts.Request.String() != "5m0s" {
			t.Errorf("Request = %v, want 5m0s", cfg.Rules[0].Timeouts.Request)
		}
	})

	t.Run("invalid duration", func(t *testing.T) {
		_, err := h.parseProperties(baseTimeoutRule(map[string]any{"request": "not a duration"}), app)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "invalid duration") {
			t.Errorf("error = %q", err.Error())
		}
	})

	t.Run("negative duration", func(t *testing.T) {
		_, err := h.parseProperties(baseTimeoutRule(map[string]any{"request": "-1s"}), app)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "non-negative") {
			t.Errorf("error = %q", err.Error())
		}
	})

	t.Run("empty block", func(t *testing.T) {
		_, err := h.parseProperties(baseTimeoutRule(map[string]any{}), app)
		if err == nil {
			t.Fatal("expected error for empty timeouts block")
		}
		if !strings.Contains(err.Error(), "at least one of") {
			t.Errorf("error = %q", err.Error())
		}
	})

	t.Run("backendRequest only", func(t *testing.T) {
		cfg, err := h.parseProperties(baseTimeoutRule(map[string]any{"backendRequest": "10s"}), app)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		to := cfg.Rules[0].Timeouts
		if to == nil || to.BackendRequest.String() != "10s" {
			t.Errorf("BackendRequest = %v, want 10s", to)
		}
		if to.Request != 0 {
			t.Errorf("Request should be zero, got %v", to.Request)
		}
	})
}

// TestHTTPRouteConfig_Generate_Filters verifies that buildGateway* functions are
// exercised end-to-end through Generate(). One Apply() call per filter type suffices.
func TestHTTPRouteConfig_Generate_Filters(t *testing.T) {
	h := &HTTPRouteHandler{}
	app := stack.NewApplication("web", "default", nil)

	applyAndGenerate := func(t *testing.T, props map[string]any) {
		t.Helper()
		bundle := &stack.Bundle{}
		trait := &oam.Trait{Type: "httproute", Properties: props}
		if err := h.Apply(trait, app, bundle); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		objs, err := bundle.Applications[0].Config.(*HTTPRouteConfig).Generate(
			stack.NewApplication("web-httproute", "default", nil),
		)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if len(objs) != 1 {
			t.Fatalf("expected 1 object, got %d", len(objs))
		}
	}

	t.Run("RequestRedirect", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "RequestRedirect",
					"requestRedirect": map[string]any{
						"scheme": "https", "statusCode": 308,
					},
				}},
			}},
		})
	})

	t.Run("RequestHeaderModifier", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "RequestHeaderModifier",
					"requestHeaderModifier": map[string]any{
						"set": []any{map[string]any{"name": "X-Foo", "value": "bar"}},
					},
				}},
			}},
		})
	})

	t.Run("ResponseHeaderModifier", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "ResponseHeaderModifier",
					"responseHeaderModifier": map[string]any{
						"remove": []any{"X-Powered-By"},
					},
				}},
			}},
		})
	})

	t.Run("URLRewrite", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "URLRewrite",
					"urlRewrite": map[string]any{
						"hostname": "new.example.com",
					},
				}},
			}},
		})
	})

	t.Run("URLRewrite with path", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "URLRewrite",
					"urlRewrite": map[string]any{
						"path": map[string]any{
							"type":               "ReplacePrefixMatch",
							"replacePrefixMatch": "/v2",
						},
					},
				}},
			}},
		})
	})

	t.Run("RequestMirror", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "RequestMirror",
					"requestMirror": map[string]any{
						"backendRef": map[string]any{"name": "mirror-svc", "port": 8080},
						"percent":    50,
					},
				}},
			}},
		})
	})

	t.Run("CORS", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "CORS",
					"cors": map[string]any{
						"allowOrigins": []any{"https://example.com"},
					},
				}},
			}},
		})
	})

	t.Run("ExternalAuth HTTP", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "ExternalAuth",
					"externalAuth": map[string]any{
						"protocol":   "HTTP",
						"backendRef": map[string]any{"name": "authz-svc", "port": 9090},
					},
				}},
			}},
		})
	})

	t.Run("Timeouts", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules": []any{map[string]any{
				"timeouts": map[string]any{"request": "30s"},
			}},
		})
	})

	t.Run("Hostnames and header matches", func(t *testing.T) {
		applyAndGenerate(t, map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw", "namespace": "gw-ns"}},
			"hostnames":  []any{"api.example.com"},
			"rules": []any{map[string]any{
				"matches": []any{map[string]any{
					"path":    map[string]any{"type": "PathPrefix", "value": "/"},
					"headers": []any{map[string]any{"name": "x-version", "value": "v1"}},
				}},
			}},
		})
	})
}
