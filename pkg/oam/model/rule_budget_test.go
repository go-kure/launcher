package model_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin the limits of one build (go-kure/launcher#1261): 500 parts, 10,000,000 cost
// units, 1 MiB of emitted properties and a 1 s deadline are counted over every model render of
// one Transform, and the budget belongs to the Transform and not to the Rule.

const (
	// fiftyParts is a model of one part that forEach turns into the most one render may give.
	fiftyParts = `
    - id: cfg
      name: "cfg-${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ lists.range(50) }"
      properties:
        data:
          k: v
`
	// hundredSteps is a part whose one expression spends about a thousand and a half cost units.
	hundredSteps = `
    - id: cfg
      type: configmap@v1
      properties:
        data:
          k: "${ string(lists.range(100).map(i, i).size()) }"
`
	blobModel = "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Model\nmetadata:\n  name: webapp.acme.example\n  version: v1\nspec:\n  celEnv: \"1\"\n" +
		"  inputs:\n    blob:\n      type: string\n      required: true\n  parts:\n" + `
    - id: cfg
      type: configmap@v1
      properties:
        data:
          k: "${ inputs.blob }"
`
)

// copies is n components of the model, named c0 to c<n-1>.
func copies(n int) []oam.Component {
	out := make([]oam.Component, n)
	for i := range out {
		out[i] = oam.Component{Name: fmt.Sprintf("c%d", i), Type: webappType}
	}
	return out
}

func wantBuildLimit(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the build passed, want a limit error naming %q", substr)
	}
	if !errors.Is(err, model.ErrLimit) {
		t.Errorf("error %q does not wrap ErrLimit", err)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error %q does not name %q", err, substr)
	}
}

// Case C1: 500 parts over several components of one model build, and the 501st fails and names
// the limit. Each render stays under the 50 of one render.
func TestRule_BuildPartsLimit(t *testing.T) {
	tr, _ := registered(t, modelHeader+fiftyParts)
	if err := transformWith(tr, copies(10), nil); err != nil {
		t.Fatalf("500 parts: %v", err)
	}
	err := transformWith(tr, append(copies(10), oam.Component{Name: "one-more", Type: webappType}), nil)
	wantBuildLimit(t, err, "more than 500 parts")
}

// Case C2: the cost of every expression of the build is summed. The limit is lowered to 5,000 units
// (10,000,000 units run longer than the deadline), each component spends about 1,500, so three
// build and four do not, though each expression is far under the limit of one. A second build
// starts from zero.
func TestRule_BuildCostLimit(t *testing.T) {
	defer model.SetBuildCostLimit(5000)()
	tr, _ := registered(t, modelHeader+hundredSteps)
	if err := transformWith(tr, copies(3), nil); err != nil {
		t.Fatalf("three components: %v", err)
	}
	err := transformWith(tr, copies(4), nil)
	wantBuildLimit(t, err, "more than 5000 cost units")
	if err := transformWith(tr, copies(3), nil); err != nil {
		t.Fatalf("a second build of three components: %v", err)
	}
}

// The limits are the ones go-kure/launcher#1261 names.
func TestBuildLimitValues(t *testing.T) {
	if model.MaxBuildParts != 500 || model.MaxBuildCost != 10_000_000 || model.MaxBuildPropertyBytes != 1<<20 || model.BuildTimeout != time.Second {
		t.Errorf("limits are %d parts, %d cost, %d bytes, %s", model.MaxBuildParts, model.MaxBuildCost, model.MaxBuildPropertyBytes, model.BuildTimeout)
	}
}

// blobApp is n components that each set the blob input to a string of the given lengths.
func blobApp(lengths ...int) []oam.Component {
	out := make([]oam.Component, len(lengths))
	for i, n := range lengths {
		out[i] = oam.Component{
			Name: fmt.Sprintf("c%d", i), Type: webappType,
			Properties: map[string]any{"blob": strings.Repeat("x", n)},
		}
	}
	return out
}

// Case C3: the bytes of the emitted properties, as JSON, are summed over the build. Eight
// components that emit exactly 1 MiB build, and one more byte fails.
func TestRule_BuildPropertyBytesLimit(t *testing.T) {
	tr, _ := registered(t, blobModel)
	overhead, err := json.Marshal(map[string]any{"data": map[string]any{"k": ""}})
	if err != nil {
		t.Fatal(err)
	}
	each := model.MaxBuildPropertyBytes/8 - len(overhead)
	lengths := []int{each, each, each, each, each, each, each, each}
	if err := transformWith(tr, blobApp(lengths...), nil); err != nil {
		t.Fatalf("exactly %d bytes: %v", model.MaxBuildPropertyBytes, err)
	}
	lengths[7]++
	err = transformWith(tr, blobApp(lengths...), nil)
	wantBuildLimit(t, err, "emitted properties bytes")
	if strings.Contains(err.Error(), "rendered YAML") {
		t.Errorf("error %q names rendered YAML, which this check does not measure", err)
	}
}

// Case C4: a clock past the deadline between two expressions fails the build, and a clock that
// is not past it does not.
func TestRule_BuildDeadline(t *testing.T) {
	tr, _ := registered(t, modelHeader+emitModelParts)
	now := time.Now()
	defer model.SetBuildClock(func() time.Time { return now })()
	if err := transformWith(tr, orders(), func(oam.ModelEmit) error { return nil }); err != nil {
		t.Fatalf("a clock not past the deadline: %v", err)
	}
	// The clock moves past the deadline of this build while the hook runs, which is after the
	// render: the next component of the same build fails.
	comps := append(orders(), oam.Component{Name: "second", Type: webappType})
	err := transformWith(tr, comps, func(e oam.ModelEmit) error {
		now = now.Add(2 * model.BuildTimeout)
		return nil
	})
	wantBuildLimit(t, err, "longer than 1s")
}

// Case C4b: the deadline is also checked after the last expression of a render. A model whose
// only part has a `when` that reads false emits nothing, which is an invalid model and not a
// failure of the build, unless the clock has passed the deadline while that expression ran. The
// clock reads the start of the build, the time left for the context and the check before the
// expression as the first three reads, and is past the deadline from the fourth.
func TestRule_BuildDeadlineAfterTheLastExpression(t *testing.T) {
	tr, _ := registered(t, modelHeader+`
    - id: cfg
      type: configmap@v1
      when: "${ false }"
      properties:
        data:
          k: v
`)
	now := time.Now()
	reads := 0
	defer model.SetBuildClock(func() time.Time {
		reads++
		if reads > 3 {
			return now.Add(2 * model.BuildTimeout)
		}
		return now
	})()
	err := transformWith(tr, orders(), nil)
	wantBuildLimit(t, err, "longer than 1s")
}

// Case C5: the budget belongs to the build. Two builds on one rule each get the full budget,
// and builds that run at the same time share nothing (run with -race).
func TestRule_BudgetIsPerBuild(t *testing.T) {
	tr, _ := registered(t, modelHeader+fiftyParts)
	for i := 0; i < 2; i++ {
		if err := transformWith(tr, copies(10), nil); err != nil {
			t.Fatalf("build %d of 500 parts: %v", i+1, err)
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = transformWith(tr, copies(10), nil)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent build %d: %v", i, err)
		}
	}
}
