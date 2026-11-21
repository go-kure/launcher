package model_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin the time limit of one build, which a caller sets on the transform context
// (go-kure/launcher#1663). Each one replaces the clock of the build, so none depends on how fast the
// host is.

// transformWithTimeout is transformWith with the time limit of the build set on the transform context.
func transformWithTimeout(tr *oam.Transformer, comps []oam.Component, limit time.Duration) error {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion, Kind: "Application",
		Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
		Spec:     oam.ApplicationSpec{Components: comps},
	}
	_, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", ModelBuildTimeout: limit})
	return err
}

// secondPerReadClock reads the start of the build, and every read after it one second later than the
// one before: any limit under a few reads is passed, and a limit of an hour is not.
func secondPerReadClock() func() time.Time {
	at := time.Now()
	var reads atomic.Int64
	return func() time.Time {
		return at.Add(time.Duration(reads.Add(1)-1) * time.Second)
	}
}

// nearLimitClock reads the start of the build, and every read after it 2 ms before the limit: the
// check between two expressions never finds the limit passed, and the context of an evaluation, which
// the build derives from the time left, expires 2 ms after it starts. Only the interrupt of an
// evaluation can fail a build that runs under it.
func nearLimitClock(limit time.Duration) func() time.Time {
	base := time.Now()
	var reads atomic.Int64
	return func() time.Time {
		if reads.Add(1) == 1 {
			return base
		}
		return base.Add(limit - 2*time.Millisecond)
	}
}

// TestBuildTimeout_ASetLimitHoldsForAModelThatPublishesOutputs: the first render of a build makes the
// budget of the whole build, and for a model that publishes outputs the first render is the query for
// them, made before the component is lowered. A limit that only the lowering knew would be ignored.
func TestBuildTimeout_ASetLimitHoldsForAModelThatPublishesOutputs(t *testing.T) {
	tr, _ := registered(t, outputsDoc("webapp.acme.example", publishedOutputs, pgOnlyParts))
	t.Cleanup(model.SetBuildClock(secondPerReadClock()))
	if err := transformWithTimeout(tr, orders(), time.Hour); err != nil {
		t.Fatalf("a limit of an hour: %v", err)
	}
	t.Cleanup(model.SetBuildClock(secondPerReadClock()))
	err := transformWithTimeout(tr, orders(), 10*time.Millisecond)
	if err == nil {
		t.Fatal("a limit of 10 ms passed on a clock that moves a second per read")
	}
	if !errors.Is(err, model.ErrLimit) || !strings.Contains(err.Error(), "longer than 10ms") {
		t.Errorf("error %q, want ErrLimit naming the limit of 10ms", err)
	}
}

// TestBuildTimeout_OneLimitHoldsAcrossNestedModels: a model that asks the outputs of a model it
// emits spends from the one budget of the build, so the limit a caller set holds for the chain.
func TestBuildTimeout_OneLimitHoldsAcrossNestedModels(t *testing.T) {
	tr := publishingChain(t)
	t.Cleanup(model.SetBuildClock(secondPerReadClock()))
	if err := transformWithTimeout(tr, copies(1), time.Hour); err != nil {
		t.Fatalf("a chain under a limit of an hour: %v", err)
	}
}

// TestBuildTimeout_UnsetKeepsTheDefault: a zero limit is the one second of BuildTimeout, and the text
// of the refusal names it, so a caller that sets nothing sees what it saw before.
func TestBuildTimeout_UnsetKeepsTheDefault(t *testing.T) {
	tr, _ := registered(t, outputsDoc("webapp.acme.example", publishedOutputs, pgOnlyParts))
	t.Cleanup(model.SetBuildClock(secondPerReadClock()))
	err := transformWithTimeout(tr, orders(), 0)
	if err == nil {
		t.Fatal("no limit set passed on a clock that moves a second per read")
	}
	if !errors.Is(err, model.ErrLimit) || !strings.Contains(err.Error(), "longer than 1s") {
		t.Errorf("error %q, want ErrLimit naming the default of 1s", err)
	}
}

// TestBuildTimeout_TheInterruptOfAnEvaluationNamesTheLimit: a long comprehension stopped by the context
// of the evaluation fails with the usual text of the deadline of the build, and the limit in force is
// appended to it.
func TestBuildTimeout_TheInterruptOfAnEvaluationNamesTheLimit(t *testing.T) {
	const limit = 50 * time.Millisecond
	tr, _ := registered(t, modelHeader+slowOneExpression)
	t.Cleanup(model.SetBuildClock(nearLimitClock(limit)))
	err := transformWithTimeout(tr, orders(), limit)
	wantInterrupt(t, err)
	if !strings.Contains(err.Error(), "deadline of the build (limit 50ms)") {
		t.Errorf("error %q does not name the limit of 50ms after the deadline text", err)
	}
}

// TestBuildTimeout_ANegativeLimitIsRefused: a negative limit is a mistake of the caller, refused by
// the field it was set in.
func TestBuildTimeout_ANegativeLimitIsRefused(t *testing.T) {
	tr, _ := registered(t, modelHeader+fiveThousandSteps)
	err := transformWithTimeout(tr, orders(), -time.Second)
	if err == nil {
		t.Fatal("a negative limit was accepted")
	}
	if !strings.Contains(err.Error(), "TransformContext.ModelBuildTimeout") {
		t.Errorf("error %q does not name TransformContext.ModelBuildTimeout", err)
	}
}
