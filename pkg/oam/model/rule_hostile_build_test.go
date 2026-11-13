package model_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests are the hostile corpus of one build (go-kure/launcher#1261): each row is a build that
// goes over one limit and a control one step under it, both through a real Transform and the model
// rule. A row passes only when the hostile build fails with model.ErrLimit and names the limit, and
// the control builds, so a rule that refuses everything fails the control.

const (
	innerType = "inner.acme.example@v1"

	// fiftyBlobs is one part that forEach turns into 50 copies of the blob input.
	fiftyBlobs = `
    - id: cfg
      name: "cfg-${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ lists.range(50) }"
      properties:
        data:
          k: "${ inputs.blob }"
`
	// hugeList is one part that forEach turns into 100,000 copies, far over the 50 of one render.
	hugeList = `
    - id: cfg
      name: "cfg-${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ lists.range(100000) }"
      properties:
        data:
          k: v
`
	// fiveThousandSteps is one expression of about 70,000 cost units, far under the 1,000,000 of one
	// expression, that takes a small part of the 1 s deadline.
	fiveThousandSteps = `
    - id: cfg
      type: configmap@v1
      properties:
        data:
          k: "${ string(lists.range(5000).map(i, i).size()) }"
`
	// slowOneExpression is one evaluation of an expression of about 700,000 cost units, under the
	// 1,000,000 of one expression, that runs far longer than the 2 ms the near-deadline clock leaves.
	slowOneExpression = `
    - id: cfg
      type: configmap@v1
      properties:
        data:
          k: "${ string(lists.range(50000).map(i, i).size()) }"
`
	// sizedSteps is one comprehension with as many steps as the blob input has bytes: 50,000 bytes
	// are about 700,000 cost units, 100 bytes a few hundred.
	sizedSteps = `
    - id: cfg
      type: configmap@v1
      properties:
        data:
          k: "${ string(lists.range(size(inputs.blob)).map(i, i).size()) }"
`
	// eightSlowExpressions is eight evaluations of an expression of about 700,000 cost units, which
	// is 5,600,000 for the build: under the 10,000,000 of one build and under the 1,000,000 of each
	// expression, so no cost limit stops it. It is the most one expression can be made to do, eight
	// times, because one expression cannot outrun the 1 s deadline once the host is fast enough.
	eightSlowExpressions = `
    - id: cfg
      name: "cfg-${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ lists.range(8) }"
      properties:
        data:
          k: "${ string(lists.range(50000).map(i, i).size()) }"
`
)

// modelNamed is the document of a model named name with the given parts and one blob input.
func modelNamed(name, parts string) string {
	return strings.Replace(modelHeader, "webapp.acme.example", name, 1) + parts
}

// withBlob is the model document of the header, a string input blob and the given parts.
func withBlob(parts string) string {
	return "apiVersion: launcher.gokure.dev/v1alpha1\nkind: Model\nmetadata:\n  name: webapp.acme.example\n  version: v1\nspec:\n  celEnv: \"1\"\n" +
		"  inputs:\n    blob:\n      type: string\n      required: true\n  parts:\n" + parts
}

// registerAll registers the given model documents on a new transformer and rule.
func registerAll(t *testing.T, docs ...string) *oam.Transformer {
	t.Helper()
	tr := expandTransformer(t)
	reg := model.NewRegistry(tr)
	for _, doc := range docs {
		m, err := model.Load([]byte(doc))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if err := reg.Register(m); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	return tr
}

// namesDeadline is true for the two texts of the deadline: the interrupt of an evaluation, and the
// check between two expressions.
func namesDeadline(err error) bool {
	return strings.Contains(err.Error(), "deadline of the build") || strings.Contains(err.Error(), "longer than 1s")
}

func wantDeadline(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("the build passed, want the deadline of the build")
	}
	if !errors.Is(err, model.ErrLimit) {
		t.Errorf("error %q does not wrap ErrLimit", err)
	}
	if !namesDeadline(err) {
		t.Errorf("error %q does not name the deadline", err)
	}
}

// wantInterrupt is wantDeadline for the text of the interrupt only: the evaluation was stopped
// while it ran, not found late by the check between two expressions.
func wantInterrupt(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("the build passed, want the interrupt of the evaluation")
	}
	if !errors.Is(err, model.ErrLimit) {
		t.Errorf("error %q does not wrap ErrLimit", err)
	}
	if !strings.Contains(err.Error(), "deadline of the build") {
		t.Errorf("error %q is not the interrupt of an evaluation", err)
	}
}

// nearDeadlineClock reads the start of the build, and every read after it 2 ms before the deadline.
// The check between two expressions never finds the deadline passed, and the context of an
// evaluation, which the build derives from the time left, expires 2 ms after it starts: only the
// interrupt of an evaluation can fail a build that runs under it.
func nearDeadlineClock() func() time.Time {
	base := time.Now()
	var reads atomic.Int64
	return func() time.Time {
		if reads.Add(1) == 1 {
			return base
		}
		return base.Add(model.BuildTimeout - 2*time.Millisecond)
	}
}

// afterBudgetClock reads the start of the build, and every read after it, ten deadlines later than
// the clock of a build that ran before it. A budget kept from that earlier build has its deadline
// in the past on this clock, and a fresh budget does not.
func afterBudgetClock() func() time.Time {
	at := time.Now().Add(10 * model.BuildTimeout)
	return func() time.Time { return at }
}

// slowBuild runs the build of eight 50,000-step comprehensions on the real clock and returns its
// error. A build that passes shows nothing about the deadline, however long it took: the time is
// measured around the whole Transform and the deadline counts from the first render, so a pause
// outside the budget cannot be told from a build that ignored it. The row is skipped with the time.
func slowBuild(t *testing.T, tr *oam.Transformer) error {
	t.Helper()
	start := time.Now()
	err := transformWith(tr, orders(), nil)
	if err == nil {
		t.Skipf("the build of eight 50,000-step comprehensions passed after %s: this host is too fast to show the deadline (race and coverage slow it)", time.Since(start))
	}
	return err
}

func buildOK(t *testing.T, tr *oam.Transformer, comps []oam.Component, what string) {
	t.Helper()
	if err := transformWith(tr, comps, nil); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// Row 1: the parts of every model render of one build are one count. Ten components of a model
// whose one part is a model of 50 parts give 10 + 500 parts and fail, and nine give 459 and build.
func TestHostileBuild_PartsAcrossNestedModels(t *testing.T) {
	tr := registerAll(t,
		modelNamed("inner.acme.example", fiftyParts),
		modelHeader+`
    - id: next
      name: next
      type: `+innerType+`
      properties: {}
`)
	buildOK(t, tr, copies(9), "nine components (459 parts)")
	err := transformWith(tr, copies(10), nil)
	wantBuildLimit(t, err, "more than 500 parts")
	// The same build is not over the limit of one render: each render is 50 parts at most.
	if strings.Contains(err.Error(), "in one render") {
		t.Errorf("error %q names the limit of one render, want the limit of the build", err)
	}
}

// Row 2: a forEach over 100,000 items fails at the limit of one render, which comes before the
// charge of the build, and a forEach over 50 items builds.
func TestHostileBuild_ForEachOverALargeList(t *testing.T) {
	tr, _ := registered(t, modelHeader+hugeList)
	err := transformWith(tr, orders(), nil)
	wantBuildLimit(t, err, "more than 50 parts in one render")
	if strings.Contains(err.Error(), "more than 500 parts") {
		t.Errorf("error %q names the limit of the build, want the limit of one render", err)
	}
	control, _ := registered(t, modelHeader+fiftyParts)
	buildOK(t, control, orders(), "a forEach over 50 items")
}

// Row 3: the cost of every expression of the build is summed, across parts and across models, and
// no expression is over the limit of one. The limit is lowered to 3,500 units because 10,000,000
// units cannot be spent inside the deadline on the host that measured this (row 3b).
func TestHostileBuild_CostSumAtALoweredLimit(t *testing.T) {
	defer model.SetBuildCostLimit(3500)()
	tr := registerAll(t, modelHeader+hundredSteps, modelNamed("other.acme.example", hundredSteps))
	other := func(name string) oam.Component { return oam.Component{Name: name, Type: "other.acme.example@v1"} }
	one := func(name string) oam.Component { return oam.Component{Name: name, Type: webappType} }
	// Each component spends about 1,500 units, so two build (about 3,000) and three do not (about
	// 4,500), over the 3,500 of the lowered limit, with every expression far under the limit of one.
	buildOK(t, tr, []oam.Component{one("a"), other("b")}, "one component of each model (about 3,000 units)")
	err := transformWith(tr, []oam.Component{one("a"), other("b"), one("c")}, nil)
	wantBuildLimit(t, err, "more than 3500 cost units")
}

// Row 3b: at the real limit the deadline stops a build of slow expressions before the cost can
// reach 10,000,000 units. This pins what happens, which is not an enforcement of the cost limit at
// 10,000,000: the build fails with ErrLimit, and names the deadline or the cost, whichever came first.
func TestHostileBuild_CostAtTheRealLimitIsStoppedByTheDeadline(t *testing.T) {
	tr, _ := registered(t, modelHeader+fiveThousandSteps)
	buildOK(t, tr, orders(), "one 5,000-step expression")
	var comps []oam.Component
	for i := 0; i < 200; i++ { // 200 expressions of about 70,000 units each are 14,000,000
		comps = append(comps, oam.Component{Name: fmt.Sprintf("c%d", i), Type: webappType})
	}
	start := time.Now()
	err := transformWith(tr, comps, nil)
	took := time.Since(start)
	if err == nil {
		t.Fatalf("200 expressions of about 70,000 units built in %s", took)
	}
	if !errors.Is(err, model.ErrLimit) {
		t.Errorf("error %q does not wrap ErrLimit", err)
	}
	if !namesDeadline(err) && !strings.Contains(err.Error(), "more than 10000000 cost units") {
		t.Errorf("error %q names neither the deadline nor the cost limit of the build", err)
	}
	t.Logf("the build stopped after %s: %v", took, err)
}

// Row 4: one component that emits more than 1 MiB of properties on its own, through a forEach of 50
// copies of an input, fails, and the same forEach under 1 MiB builds. The input is one byte longer
// in the failing build than in the building one, and the total is counted as JSON, so the
// overhead of the properties of a copy is measured and not guessed: 50 copies of the longest input
// that fits are 26 bytes under the limit, and of one byte more 24 bytes over it. The exact byte is
// pinned by TestRule_BuildPropertyBytesLimit.
func TestHostileBuild_OneComponentOverTheBytesLimit(t *testing.T) {
	tr, _ := registered(t, withBlob(fiftyBlobs))
	overhead, err := json.Marshal(map[string]any{"data": map[string]any{"k": ""}})
	if err != nil {
		t.Fatal(err)
	}
	fits := model.MaxBuildPropertyBytes/50 - len(overhead)
	app := func(n int) []oam.Component {
		return []oam.Component{{Name: "orders", Type: webappType, Properties: map[string]any{"blob": strings.Repeat("x", n)}}}
	}
	buildOK(t, tr, app(fits), fmt.Sprintf("50 copies of %d bytes (%d bytes)", fits, 50*(fits+len(overhead))))
	err = transformWith(tr, app(fits+1), nil)
	wantBuildLimit(t, err, "emitted properties bytes")
}

// Row 5a: the deadline interrupts an evaluation while it runs. The clock leaves the context of the
// evaluation 2 ms and never shows the check between two expressions a passed deadline, so the one
// comprehension of 50,000 steps, which takes far longer than 2 ms, can fail the build only by the
// interrupt, and the error must be the text of the interrupt. A 100-step comprehension on the real
// clock builds, so a rule that refuses everything fails the control.
func TestHostileBuild_DeadlineInterruptsALongComprehension(t *testing.T) {
	tr, _ := registered(t, modelHeader+slowOneExpression)
	undo := model.SetBuildClock(nearDeadlineClock())
	err := transformWith(tr, orders(), nil)
	undo()
	wantInterrupt(t, err)
	control, _ := registered(t, modelHeader+hundredSteps)
	buildOK(t, control, orders(), "a 100-step comprehension")
}

// Row 5b: a build of eight real comprehensions of 50,000 steps fails at the 1 s deadline with
// ErrLimit on the real clock, where unaided it runs for longer. A 100-step comprehension builds.
// The row shows the deadline only on a host that is slow enough, which is the case under -race and
// coverage, and it is skipped with its measured time where the build passes inside twice the
// deadline. It asserts no upper bound on the time: that is a property of the host, and the
// interrupt is pinned by row 5a. The cel-go version is logged beside the result.
func TestHostileBuild_DeadlineStopsARealLongBuild(t *testing.T) {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "cel.dev/cel-go" {
				t.Logf("cel.dev/cel-go %s", dep.Version)
			}
		}
	}
	tr, _ := registered(t, modelHeader+eightSlowExpressions)
	start := time.Now()
	err := slowBuild(t, tr)
	took := time.Since(start)
	wantDeadline(t, err)
	t.Logf("the build stopped after %s", took)
	control, _ := registered(t, modelHeader+hundredSteps)
	buildOK(t, control, orders(), "a 100-step comprehension")
}

// Row 6: a build that fails leaves nothing behind. After a failed build over the parts, the bytes
// and the deadline, the next build of the same rule starts from zero, and a hostile build that runs
// at the same time as a benign one does not change the benign one (run with -race).
func TestHostileBuild_FailedBuildPoisonsNothing(t *testing.T) {
	t.Run("parts", func(t *testing.T) {
		tr, _ := registered(t, modelHeader+fiftyParts)
		wantBuildLimit(t, transformWith(tr, copies(11), nil), "more than 500 parts")
		buildOK(t, tr, copies(10), "500 parts after a failed build")
	})
	t.Run("bytes", func(t *testing.T) {
		tr, _ := registered(t, withBlob(fiftyBlobs))
		app := func(n int) []oam.Component {
			return []oam.Component{{Name: "orders", Type: webappType, Properties: map[string]any{"blob": strings.Repeat("x", n)}}}
		}
		wantBuildLimit(t, transformWith(tr, app(25000), nil), "emitted properties bytes")
		buildOK(t, tr, app(20000), "under the limit after a failed build")
	})
	t.Run("deadline", func(t *testing.T) {
		// One model whose work follows the length of its input, so both builds go through the same
		// rule. The long input is stopped by the interrupt on the near-deadline clock, the same on
		// every host. The short one then builds on a clock ten deadlines later: a budget or a
		// cancelled context kept from the first build fails it, a fresh budget does not.
		tr, _ := registered(t, withBlob(sizedSteps))
		app := func(n int) []oam.Component {
			return []oam.Component{{Name: "orders", Type: webappType, Properties: map[string]any{"blob": strings.Repeat("x", n)}}}
		}
		undo := model.SetBuildClock(nearDeadlineClock())
		err := transformWith(tr, app(50000), nil)
		undo()
		wantInterrupt(t, err)
		defer model.SetBuildClock(afterBudgetClock())()
		buildOK(t, tr, app(100), "a short input after a failed build")
	})
	t.Run("concurrent", func(t *testing.T) {
		tr, _ := registered(t, modelHeader+fiftyParts)
		var wg sync.WaitGroup
		hostile := make([]error, 3)
		benign := make([]error, 3)
		for i := range hostile {
			wg.Add(2)
			go func() { defer wg.Done(); hostile[i] = transformWith(tr, copies(11), nil) }()
			go func() { defer wg.Done(); benign[i] = transformWith(tr, copies(10), nil) }()
		}
		wg.Wait()
		for i := range hostile {
			wantBuildLimit(t, hostile[i], "more than 500 parts")
			if benign[i] != nil {
				t.Errorf("benign build %d beside a hostile one: %v", i, benign[i])
			}
		}
	})
}
