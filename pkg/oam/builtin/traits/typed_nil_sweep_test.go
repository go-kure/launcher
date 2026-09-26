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
