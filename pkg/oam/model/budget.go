package model

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

const (
	// MaxBuildParts is the number of components all the model renders of one build give
	// together. MaxParts bounds one render.
	MaxBuildParts = 500
	// MaxBuildCost is the actual cost, in CEL cost units, that all the expressions of one build
	// spend together. The limit of one expression is a lower constant of the expression engine.
	MaxBuildCost int64 = 10_000_000
	// MaxBuildPropertyBytes is the size, as JSON, of the properties that all the components of
	// one build emit together. It is a proxy: the build renders YAML later, and that size is not
	// checked here.
	MaxBuildPropertyBytes = 1 << 20
	// BuildTimeout is the time one build may spend on its model renders, counted from the first.
	BuildTimeout = time.Second

	// interruptCheckFrequency is the number of steps of a comprehension between two checks of the
	// context of the evaluation.
	interruptCheckFrequency = 100

	deadlineMessage = "the evaluation ran past the deadline of the build"
)

// buildClock is the time source of the build deadline, and buildCostLimit is the limit of the
// cost of a build. A test replaces them: the actual cost of 10,000,000 units takes longer than
// the deadline, so only a lower limit can show the sum at work.
var (
	buildClock     = time.Now
	buildCostLimit = MaxBuildCost
)

// buildBudget is what one build, one Transform, has spent. The run keeps it (see
// oam.LoweringContext.RunState), so every model rule of the run shares it and a Rule keeps none.
// A nil budget has no limit.
type buildBudget struct {
	mu       sync.Mutex
	parts    int
	cost     int64
	bytes    int
	deadline time.Time
}

type budgetKey struct{}

func newBuildBudget() *buildBudget {
	return &buildBudget{deadline: buildClock().Add(BuildTimeout)}
}

func (b *buildBudget) chargeParts(n int) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > MaxBuildParts-b.parts {
		return errors.Errorf("%w: the build gives more than %d parts", ErrLimit, MaxBuildParts)
	}
	b.parts += n
	return nil
}

func (b *buildBudget) chargeCost(n int64) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > buildCostLimit-b.cost {
		return errors.Errorf("%w: the build spends more than %d cost units", ErrLimit, buildCostLimit)
	}
	b.cost += n
	return nil
}

func (b *buildBudget) chargeBytes(n int) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > MaxBuildPropertyBytes-b.bytes {
		return errors.Errorf("%w: the build emits more than %d emitted properties bytes", ErrLimit, MaxBuildPropertyBytes)
	}
	b.bytes += n
	return nil
}

func (b *buildBudget) checkDeadline() error {
	if b == nil {
		return nil
	}
	if buildClock().After(b.deadline) {
		return errors.Errorf("%w: the build ran longer than %s", ErrLimit, BuildTimeout)
	}
	return nil
}

// remaining is the time left to the deadline.
func (b *buildBudget) remaining() time.Duration { return b.deadline.Sub(buildClock()) }

// propertyBytes is the size as JSON of the properties of comps and of their traits.
func propertyBytes(comps []oam.Component) (int, error) {
	n := 0
	for _, c := range comps {
		raw, err := json.Marshal(c.Properties)
		if err != nil {
			return 0, errors.Wrapf(err, "component %q", c.Name)
		}
		n += len(raw)
		for _, t := range c.Traits {
			raw, err := json.Marshal(t.Properties)
			if err != nil {
				return 0, errors.Wrapf(err, "component %q trait %q", c.Name, t.Type)
			}
			n += len(raw)
		}
	}
	return n, nil
}
