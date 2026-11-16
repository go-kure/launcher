package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// valueConfig is a config held by value, with a field of a type that does not
// compare: comparing two of them as interfaces panics.
type valueConfig struct{ extra any }

func (valueConfig) Generate(*stack.Application) ([]*client.Object, error) { return nil, nil }

// pointerConfig has a field so that two of them are two objects: pointers to a
// zero-size value may compare equal.
type pointerConfig struct{ _ byte }

func (*pointerConfig) Generate(*stack.Application) ([]*client.Object, error) { return nil, nil }

// sameConfig decides whether a trait put a config in place, so it must answer
// for any config a handler holds and never panic: the report changes nothing
// the transform produces.
func TestSameConfig(t *testing.T) {
	p, q := &pointerConfig{}, &pointerConfig{}
	withMap := valueConfig{extra: map[string]any{"k": 1}}
	cases := []struct {
		name string
		a, b stack.ApplicationConfig
		want bool
	}{
		{"one pointer", p, p, true},
		{"two pointers", p, q, false},
		{"another type", p, withMap, false},
		{"a value holding a map, unchanged", withMap, withMap, true},
		{"a value holding nothing", valueConfig{}, valueConfig{}, true},
		{"nil and a pointer", nil, p, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameConfig(tc.a, tc.b); got != tc.want {
				t.Errorf("sameConfig = %v, want %v", got, tc.want)
			}
		})
	}
}
