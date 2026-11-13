package model

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// These tests pin the limits of one build (go-kure/launcher#1261): the budget a build spends,
// and the stop of a long comprehension at the deadline.

func wantLimit(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want a limit error naming %q", substr)
	}
	if !errors.Is(err, ErrLimit) {
		t.Errorf("error %q does not wrap ErrLimit", err)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error %q does not name %q", err, substr)
	}
}

// The edge of each limit: the amount that equals the limit passes and one more fails, and a nil
// budget, which a direct Eval or Expand has, limits nothing.
func TestBuildBudget_LimitEdges(t *testing.T) {
	if err := newBuildBudget().chargeParts(MaxBuildParts); err != nil {
		t.Errorf("%d parts: %v", MaxBuildParts, err)
	}
	b := newBuildBudget()
	if err := b.chargeParts(MaxBuildParts); err != nil {
		t.Fatal(err)
	}
	wantLimit(t, b.chargeParts(1), "more than 500 parts")

	if err := newBuildBudget().chargeCost(MaxBuildCost); err != nil {
		t.Errorf("%d cost: %v", MaxBuildCost, err)
	}
	b = newBuildBudget()
	if err := b.chargeCost(MaxBuildCost - 1); err != nil {
		t.Fatal(err)
	}
	wantLimit(t, b.chargeCost(2), "more than 10000000 cost units")

	if err := newBuildBudget().chargeBytes(MaxBuildPropertyBytes); err != nil {
		t.Errorf("%d bytes: %v", MaxBuildPropertyBytes, err)
	}
	b = newBuildBudget()
	if err := b.chargeBytes(MaxBuildPropertyBytes); err != nil {
		t.Fatal(err)
	}
	wantLimit(t, b.chargeBytes(1), "emitted properties bytes")

	var none *buildBudget
	for name, err := range map[string]error{
		"parts": none.chargeParts(1 << 30), "cost": none.chargeCost(1 << 60),
		"bytes": none.chargeBytes(1 << 30), "deadline": none.checkDeadline(),
	} {
		if err != nil {
			t.Errorf("a nil budget limited %s: %v", name, err)
		}
	}
}

// The deadline is checked against the clock of the build: a clock not past it passes, and one
// past it fails.
func TestBuildBudget_Deadline(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	defer SetBuildClock(func() time.Time { return now })()
	b := newBuildBudget()
	now = now.Add(BuildTimeout)
	if err := b.checkDeadline(); err != nil {
		t.Errorf("at the deadline: %v", err)
	}
	now = now.Add(time.Nanosecond)
	wantLimit(t, b.checkDeadline(), "longer than 1s")
}

// A comprehension of 50,000 steps, under the cost limit of one expression, stops when its
// context is done, and the failure is a limit error. It stops at the first check, so the test is
// quick. The same kind of expression, small, under a live context and under a nil context returns
// its value: a large one would run for minutes under the race detector.
func TestEval_InterruptsALongComprehension(t *testing.T) {
	long := mustCompile(t, propModel(t, "${lists.range(50000).map(i, i).size()}"))
	short := mustCompile(t, propModel(t, "${lists.range(500).map(i, i).size()}"))
	sc := stdScope()

	done, cancel := context.WithCancel(context.Background())
	cancel()
	sc.Ctx = done
	_, _, err := long.Eval(propSite, sc)
	wantLimit(t, err, "past the deadline")
	if !strings.Contains(err.Error(), propSite) {
		t.Errorf("error %q does not name the site %q", err, propSite)
	}

	sc.Ctx = context.Background()
	if got, _ := mustEval(t, short, propSite, sc); got != int64(500) {
		t.Fatalf("under a live context gave %#v, want int64(500)", got)
	}
	// A nil context reads as context.Background().
	sc.Ctx = nil
	if got, _ := mustEval(t, short, propSite, sc); got != int64(500) {
		t.Fatalf("under a nil context gave %#v, want int64(500)", got)
	}
}
