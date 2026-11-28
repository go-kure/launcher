package model_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin Registry.RegisterLazy (go-kure/launcher#1748): a model that ships with the
// program registers without compiling its expressions and compiles once, at its first use,
// while Register still compiles at registration.

const (
	lazyType   = "lazy.acme.example@v1"
	lazySource = "shipped model lazy.yaml"
)

// lazyDoc is a model of one ConfigMap whose data reads expr, so a bad expr is a model that does
// not compile.
func lazyDoc(expr string) string {
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: lazy.acme.example
  version: v1
spec:
  celEnv: "1"
  inputs:
    greeting: {type: string, default: hi}
  outputs: {}
  parts:
    - id: cm
      type: configmap@v1
      properties:
        data:
          greeting: "${` + expr + `}"
`
}

// lazyRegistered registers the model in doc on a fresh Transformer, eagerly or lazily.
func lazyRegistered(t *testing.T, doc string, lazy bool) (*oam.Transformer, error) {
	t.Helper()
	tr := expandTransformer(t)
	m, err := model.Load([]byte(doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	reg := model.NewRegistry(tr)
	if lazy {
		return tr, reg.RegisterLazy(m, lazySource)
	}
	return tr, reg.Register(m)
}

func lazyComponents() []oam.Component {
	return []oam.Component{{Name: "orders", Type: lazyType, Properties: map[string]any{"greeting": "hello"}}}
}

// A model registered lazily builds the objects the same model registered by Register builds.
func TestRegisterLazy_BuildsWhatRegisterBuilds(t *testing.T) {
	eager, err := lazyRegistered(t, lazyDoc("inputs.greeting"), false)
	if err != nil {
		t.Fatal(err)
	}
	lazy, err := lazyRegistered(t, lazyDoc("inputs.greeting"), true)
	if err != nil {
		t.Fatal(err)
	}
	want := objectsOK(t, eager, lazyComponents(), nil)
	got := objectsOK(t, lazy, lazyComponents(), nil)
	if len(got) == 0 {
		t.Fatal("the lazy model built nothing")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lazy objects = %v, want %v", got, want)
	}
}

// A model whose expression does not compile is refused by Register, and is registered by
// RegisterLazy, which fails the first use and every later one with the source named.
func TestRegisterLazy_CompilesAtFirstUse(t *testing.T) {
	if _, err := lazyRegistered(t, lazyDoc("inputs.nosuch"), false); err == nil || !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("Register of a model that does not compile: err = %v, want a refusal naming the expression", err)
	}
	tr, err := lazyRegistered(t, lazyDoc("inputs.nosuch"), true)
	if err != nil {
		t.Fatalf("RegisterLazy compiled at registration: %v", err)
	}
	for use := 1; use <= 2; use++ {
		err := transformWith(tr, lazyComponents(), nil)
		if err == nil {
			t.Fatalf("use %d: a model that does not compile built", use)
		}
		for _, want := range []string{lazySource, "lazy.acme.example@v1.0.0", "nosuch"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("use %d: the refusal does not hold %q: %v", use, want, err)
			}
		}
	}
}

// Many builds that use a lazy model first at once compile it once and all see the same answer;
// run under -race, it is the proof that the first use is safe.
func TestRegisterLazy_ConcurrentFirstUse(t *testing.T) {
	for name, tc := range map[string]struct {
		expr    string
		wantErr bool
	}{"compiles": {"inputs.greeting", false}, "does not compile": {"inputs.nosuch", true}} {
		t.Run(name, func(t *testing.T) {
			tr, err := lazyRegistered(t, lazyDoc(tc.expr), true)
			if err != nil {
				t.Fatal(err)
			}
			const n = 16
			errs := make([]error, n)
			var wg sync.WaitGroup
			for i := range errs {
				wg.Add(1)
				go func() { defer wg.Done(); errs[i] = transformWith(tr, lazyComponents(), nil) }()
			}
			wg.Wait()
			for i, err := range errs {
				if (err != nil) != tc.wantErr {
					t.Errorf("build %d: err = %v, want an error: %v", i, err, tc.wantErr)
				}
				if tc.wantErr && err != nil && !strings.Contains(err.Error(), lazySource) {
					t.Errorf("build %d: the refusal does not name %q: %v", i, lazySource, err)
				}
			}
		})
	}
}
