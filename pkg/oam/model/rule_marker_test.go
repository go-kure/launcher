package model_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// The rule of a model writes authored traits (go-kure/launcher#1562), so the engine leaves them unsealed.
func TestRule_IsAnAuthoredTraitEmitter(t *testing.T) {
	var r oam.ComponentLoweringRule = (*model.Rule)(nil)
	e, ok := r.(oam.AuthoredTraitEmitter)
	if !ok || !e.EmitsAuthoredTraits() {
		t.Fatal("*model.Rule must implement oam.AuthoredTraitEmitter and return true")
	}
}
