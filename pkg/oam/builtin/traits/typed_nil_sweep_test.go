package traits

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// The sites below are the typed-nil holes the go-kure/launcher#465 sweep found in
// this package: a bare `v.(map[string]any)` / `v.([]any)` succeeds on a TYPED nil
// (map[string]any(nil), []any(nil) — what an uninitialized Go map or slice in a
// lowering rule or a Go-API caller produces) with ok=true, so the value read as an
// authored empty collection where an UNTYPED nil (a decoded `key:` with no value)
// took the absent or the refused path. Every case pins the two shapes to the same
// answer: the same error, or the same parsed result.
//
// Each site is its own subtest so a mutant that reopens one hole is reported by
// name rather than hidden in a shared row.

// sameNullAnswer runs run once with an untyped nil and once with typed, and
// fails unless both return the same error text and a deeply equal result. A panic
// is reported as a failure of the typed case rather than aborting the test binary.
func sameNullAnswer(t *testing.T, typed any, run func(v any) (any, error)) {
	t.Helper()
	call := func(v any) (res any, err error, panicked any) {
		defer func() { panicked = recover() }()
		res, err = run(v)
		return res, err, nil
	}
	wantRes, wantErr, p := call(nil)
	if p != nil {
		t.Fatalf("untyped nil panicked: %v", p)
	}
	gotRes, gotErr, p := call(typed)
	if p != nil {
		t.Fatalf("typed nil %T panicked: %v (untyped nil: res=%+v err=%v)", typed, p, wantRes, wantErr)
	}
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatalf("typed nil %T error = %v, untyped nil error = %v", typed, gotErr, wantErr)
	}
	if !reflect.DeepEqual(gotRes, wantRes) {
		t.Fatalf("typed nil %T result = %+v, untyped nil result = %+v", typed, gotRes, wantRes)
	}
}

func sweepApp() *stack.Application {
	return stack.NewApplication("web", "default", &mockServicePortConfig{port: 80})
}

func httpRouteProps(rules []any) map[string]any {
	return map[string]any{
		"parentRefs": []any{map[string]any{"name": "gw"}},
		"rules":      rules,
	}
}

func ingressProps(rule map[string]any, extra map[string]any) map[string]any {
	props := map[string]any{"rules": []any{rule}}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

func TestTypedNilSweep(t *testing.T) {
	nilMap := map[string]any(nil)
	nilList := []any(nil)

	t.Run("expose sslRedirect onto a null annotations map", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			props := map[string]any{"annotations": v, "sslRedirect": true}
			setSSLRedirectAnnotations(props)
			return props["annotations"], nil
		})
	})

	t.Run("fluxcd-patches patches", func(t *testing.T) {
		sameNullAnswer(t, nilList, func(v any) (any, error) {
			bundle := &stack.Bundle{}
			err := (&FluxCDPatchesHandler{}).Apply(&oam.Trait{Type: "fluxcd-patches",
				Properties: map[string]any{"patches": v}}, sweepApp(), bundle)
			return bundle.Patches, err
		})
	})

	t.Run("fluxcd-patches patches[0].target", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			bundle := &stack.Bundle{}
			err := (&FluxCDPatchesHandler{}).Apply(&oam.Trait{Type: "fluxcd-patches",
				Properties: map[string]any{"patches": []any{map[string]any{"patch": "p", "target": v}}}},
				sweepApp(), bundle)
			return bundle.Patches, err
		})
	})

	t.Run("external-secret target.template", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			return (&ExternalSecretHandler{}).parseProperties(map[string]any{
				"secretName":     "db",
				"secretStoreRef": map[string]any{"name": "vault"},
				"target":         map[string]any{"template": v},
			}, sweepApp())
		})
	})

	t.Run("httproute rules[0]", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			return (&HTTPRouteHandler{}).parseProperties(httpRouteProps([]any{v}), sweepApp())
		})
	})

	t.Run("httproute rules[0].matches[0]", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			return (&HTTPRouteHandler{}).parseProperties(
				httpRouteProps([]any{map[string]any{"matches": []any{v}}}), sweepApp())
		})
	})

	t.Run("httproute rules[0].backendRefs", func(t *testing.T) {
		sameNullAnswer(t, nilList, func(v any) (any, error) {
			return (&HTTPRouteHandler{}).parseProperties(
				httpRouteProps([]any{map[string]any{"backendRefs": v}}), sweepApp())
		})
	})

	t.Run("httproute rules[0].backendRefs[0]", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			return (&HTTPRouteHandler{}).parseProperties(
				httpRouteProps([]any{map[string]any{"backendRefs": []any{v}}}), sweepApp())
		})
	})

	for _, key := range []string{"grpc", "http", "forwardBody"} {
		t.Run("httproute externalAuth."+key, func(t *testing.T) {
			sameNullAnswer(t, nilMap, func(v any) (any, error) {
				return parseExternalAuth(map[string]any{"externalAuth": map[string]any{
					"protocol":   "HTTP",
					"backendRef": map[string]any{"name": "authz", "port": 9090},
					key:          v,
				}}, "rules[0].filters[0]")
			})
		})
	}

	t.Run("ingress rules[0].paths[0]", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			return (&IngressHandler{}).parseProperties(ingressProps(
				map[string]any{"host": "web.example.com", "paths": []any{v}}, nil), sweepApp())
		})
	})

	t.Run("ingress tls[0]", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			return (&IngressHandler{}).parseProperties(ingressProps(
				map[string]any{"host": "web.example.com", "paths": []any{map[string]any{}}},
				map[string]any{"tls": []any{v}}), sweepApp())
		})
	})

	t.Run("pvc accessModes", func(t *testing.T) {
		sameNullAnswer(t, nilList, func(v any) (any, error) {
			return (&PVCHandler{}).parseProperties(map[string]any{
				"name": "data", "size": "1Gi", "accessModes": v,
			}, sweepApp())
		})
	})
}

// externalSecretProps returns a valid external-secret property set with extra
// merged over it.
func externalSecretProps(extra map[string]any) map[string]any {
	props := map[string]any{
		"secretName":     "db",
		"secretStoreRef": map[string]any{"name": "vault"},
	}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

// TestTypedNilSweepFollowUp covers the sites the branch review found still
// diverging after the first sweep. An optional block beside a valid sibling is
// the shape that exposed each one: untyped nil skips the block and the sibling
// carries the document, while a typed nil entered the block and either failed a
// required-field check or produced a different parse.
func TestTypedNilSweepFollowUp(t *testing.T) {
	nilMap := map[string]any(nil)
	nilList := []any(nil)
	validData := []any{map[string]any{"secretKey": "k", "remoteRef": map[string]any{"key": "db/pass"}}}

	es := func(extra map[string]any) (any, error) {
		return (&ExternalSecretHandler{}).parseProperties(externalSecretProps(extra), sweepApp())
	}
	route := func(rules []any) (any, error) {
		return (&HTTPRouteHandler{}).parseProperties(httpRouteProps(rules), sweepApp())
	}
	cases := []struct {
		name  string
		typed any
		run   func(v any) (any, error)
	}{
		{"external-secret data[0]", nilMap, func(v any) (any, error) {
			return es(map[string]any{"data": []any{v}})
		}},
		{"external-secret data[0].remoteRef", nilMap, func(v any) (any, error) {
			return es(map[string]any{"data": []any{map[string]any{"secretKey": "k", "remoteRef": v}}})
		}},
		{"external-secret dataFrom[0]", nilMap, func(v any) (any, error) {
			return es(map[string]any{"dataFrom": []any{v}})
		}},
		{"external-secret dataFrom[0].extract beside find", nilMap, func(v any) (any, error) {
			return es(map[string]any{"dataFrom": []any{map[string]any{
				"extract": v, "find": map[string]any{"name": map[string]any{"regexp": "db-.*"}}}}})
		}},
		{"external-secret dataFrom[0].find beside extract", nilMap, func(v any) (any, error) {
			return es(map[string]any{"dataFrom": []any{map[string]any{
				"extract": map[string]any{"key": "db"}, "find": v}}})
		}},
		{"external-secret dataFrom[0].find.tags beside name", nilMap, func(v any) (any, error) {
			return es(map[string]any{"dataFrom": []any{map[string]any{"find": map[string]any{
				"name": map[string]any{"regexp": "db-.*"}, "tags": v}}}})
		}},
		{"external-secret remoteRef beside data", nilMap, func(v any) (any, error) {
			return es(map[string]any{"data": validData, "remoteRef": v})
		}},
		{"external-secret target.template.data", nilMap, func(v any) (any, error) {
			return es(map[string]any{"data": validData, "target": map[string]any{
				"template": map[string]any{"type": "Opaque", "data": v}}})
		}},
		{"httproute annotations", nilMap, func(v any) (any, error) {
			props := httpRouteProps([]any{map[string]any{}})
			props["annotations"] = v
			return (&HTTPRouteHandler{}).parseProperties(props, sweepApp())
		}},
		{"ingress rules[0]", nilMap, func(v any) (any, error) {
			return (&IngressHandler{}).parseProperties(map[string]any{"rules": []any{v}}, sweepApp())
		}},
		{"ingress annotations", nilMap, func(v any) (any, error) {
			return (&IngressHandler{}).parseProperties(ingressProps(map[string]any{
				"host": "web.example.com", "paths": []any{map[string]any{}}},
				map[string]any{"annotations": v}), sweepApp())
		}},
		{"httproute parentRefs[0]", nilMap, func(v any) (any, error) {
			return (&HTTPRouteHandler{}).parseProperties(map[string]any{
				"parentRefs": []any{v}, "rules": []any{map[string]any{}}}, sweepApp())
		}},
		{"httproute matches[0].path", nilMap, func(v any) (any, error) {
			return route([]any{map[string]any{"matches": []any{map[string]any{"path": v}}}})
		}},
		{"httproute matches[0].headers[0]", nilMap, func(v any) (any, error) {
			return route([]any{map[string]any{"matches": []any{map[string]any{"headers": []any{v}}}}})
		}},
		{"httproute backendRefs[0].backendSelector", nilMap, func(v any) (any, error) {
			return route([]any{map[string]any{"backendRefs": []any{map[string]any{
				"name": "ext", "port": 80, "backendSelector": v}}}})
		}},
		{"httproute filters[0]", nilMap, func(v any) (any, error) {
			return parseRuleFilters(map[string]any{"filters": []any{v}}, 0)
		}},
		{"httproute timeouts", nilMap, func(v any) (any, error) {
			return parseRuleTimeouts(map[string]any{"timeouts": v}, 0)
		}},
		{"httproute requestRedirect", nilMap, func(v any) (any, error) {
			return parseRequestRedirect(map[string]any{"requestRedirect": v}, "f")
		}},
		{"httproute requestRedirect.path beside scheme", nilMap, func(v any) (any, error) {
			return parseRequestRedirect(map[string]any{"requestRedirect": map[string]any{
				"scheme": "https", "path": v}}, "f")
		}},
		{"httproute urlRewrite", nilMap, func(v any) (any, error) {
			return parseURLRewrite(map[string]any{"urlRewrite": v}, "f")
		}},
		{"httproute urlRewrite.path beside hostname", nilMap, func(v any) (any, error) {
			return parseURLRewrite(map[string]any{"urlRewrite": map[string]any{
				"hostname": "example.com", "path": v}}, "f")
		}},
		{"httproute requestMirror", nilMap, func(v any) (any, error) {
			return parseRequestMirror(map[string]any{"requestMirror": v}, "f")
		}},
		{"httproute requestMirror.backendRef", nilMap, func(v any) (any, error) {
			return parseRequestMirror(map[string]any{"requestMirror": map[string]any{"backendRef": v}}, "f")
		}},
		{"httproute requestMirror.fraction", nilMap, func(v any) (any, error) {
			return parseRequestMirror(map[string]any{"requestMirror": map[string]any{
				"backendRef": map[string]any{"name": "mirror", "port": 80}, "fraction": v}}, "f")
		}},
		{"httproute cors", nilMap, func(v any) (any, error) {
			return parseCORS(map[string]any{"cors": v}, "f")
		}},
		{"httproute externalAuth", nilMap, func(v any) (any, error) {
			return parseExternalAuth(map[string]any{"externalAuth": v}, "f")
		}},
		{"httproute externalAuth.backendRef", nilMap, func(v any) (any, error) {
			return parseExternalAuth(map[string]any{"externalAuth": map[string]any{
				"protocol": "HTTP", "backendRef": v}}, "f")
		}},
		{"httproute requestHeaderModifier", nilMap, func(v any) (any, error) {
			return parseHeaderModifier(map[string]any{"requestHeaderModifier": v}, "requestHeaderModifier", "f")
		}},
		{"httproute header modifier set[0]", nilMap, func(v any) (any, error) {
			return parseHeaderKVList([]any{v}, "f.set")
		}},
		{"ingress paths[0].backendSelector", nilMap, func(v any) (any, error) {
			return (&IngressHandler{}).parseProperties(ingressProps(map[string]any{
				"host": "web.example.com", "paths": []any{map[string]any{"backendSelector": v}}}, nil), sweepApp())
		}},
		{"networkPolicy", nilMap, func(v any) (any, error) {
			return parseTrafficSources(map[string]any{"networkPolicy": v}, "web", "ingress")
		}},
		{"networkPolicy.trafficSources[0]", nilMap, func(v any) (any, error) {
			return parseTrafficSources(map[string]any{"networkPolicy": map[string]any{
				"trafficSources": []any{v}}}, "web", "ingress")
		}},
		{"networkPolicy.trafficSources[0].podSelector", nilMap, func(v any) (any, error) {
			return parseTrafficSources(map[string]any{"networkPolicy": map[string]any{
				"trafficSources": []any{map[string]any{"namespace": "edge", "podSelector": v}}}}, "web", "ingress")
		}},
		{"rbac rules[0]", nilMap, func(v any) (any, error) {
			return (&RBACHandler{}).parseProperties(map[string]any{"rules": []any{v}}, sweepApp())
		}},
		{"rbac rules[0].apiGroups", nilList, func(v any) (any, error) {
			return (&RBACHandler{}).parseProperties(map[string]any{"rules": []any{map[string]any{
				"apiGroups": v, "resources": []any{"pods"}, "verbs": []any{"get"}}}}, sweepApp())
		}},
		{"certificate issuerRef", nilMap, func(v any) (any, error) {
			return (&CertificateHandler{}).parseProperties(map[string]any{
				"secretName": "tls", "issuerRef": v, "dnsNames": []any{"web.example.com"}}, sweepApp())
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { sameNullAnswer(t, tc.typed, tc.run) })
	}

	postbuild := func(props map[string]any) (any, error) {
		bundle := &stack.Bundle{}
		err := (&PostBuildHandler{}).Apply(&oam.Trait{Type: "fluxcd-postbuild", Properties: props}, sweepApp(), bundle)
		return bundle.PostBuild, err
	}
	validFrom := []any{map[string]any{"kind": "ConfigMap", "name": "vars"}}
	t.Run("fluxcd-postbuild substitute beside substituteFrom", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			return postbuild(map[string]any{"substitute": v, "substituteFrom": validFrom})
		})
	})
	t.Run("fluxcd-postbuild substituteFrom beside substitute", func(t *testing.T) {
		sameNullAnswer(t, nilList, func(v any) (any, error) {
			return postbuild(map[string]any{"substitute": map[string]any{"A": "1"}, "substituteFrom": v})
		})
	})
	t.Run("fluxcd-postbuild substituteFrom[0]", func(t *testing.T) {
		sameNullAnswer(t, nilMap, func(v any) (any, error) {
			return postbuild(map[string]any{"substituteFrom": []any{v}})
		})
	})
}
