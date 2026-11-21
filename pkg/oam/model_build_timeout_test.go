package oam

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestModelBuildTimeout_EveryEntryRefusesANegativeValue: a negative limit is refused by each public
// entry that takes a TransformContext and starts a build of its own, so it never reads as the default
// (go-kure/launcher#1663). A transformer with no rules makes the raw pass return early on any other
// input, so the refusal is shown to come before that return.
func TestModelBuildTimeout_EveryEntryRefusesANegativeValue(t *testing.T) {
	ctx := TransformContext{ModelBuildTimeout: -time.Second}
	tr := NewTransformer(nil, nil)

	_, err := tr.LowerRaws([]json.RawMessage{json.RawMessage(`{}`)}, ctx)
	wantNegativeRefusal(t, "LowerRaws", err)

	_, _, err = tr.LowerRawsWithSteps(nil, ctx)
	wantNegativeRefusal(t, "LowerRawsWithSteps", err)

	_, err = tr.Transform(&Application{APIVersion: SupportedAPIVersion, Kind: "Application", Metadata: Metadata{Name: "shop"}}, ctx)
	wantNegativeRefusal(t, "Transform", err)

	// Zero and a positive value are accepted: the raw pass has nothing to do, so it passes the input through.
	raws := []json.RawMessage{json.RawMessage(`{}`)}
	for _, ok := range []time.Duration{0, time.Hour} {
		if _, err := tr.LowerRaws(raws, TransformContext{ModelBuildTimeout: ok}); err != nil {
			t.Errorf("LowerRaws with a limit of %s: %v", ok, err)
		}
	}
}

func wantNegativeRefusal(t *testing.T, entry string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s accepted a negative ModelBuildTimeout", entry)
		return
	}
	if !strings.Contains(err.Error(), "TransformContext.ModelBuildTimeout") {
		t.Errorf("%s: error %q does not name TransformContext.ModelBuildTimeout", entry, err)
	}
}
