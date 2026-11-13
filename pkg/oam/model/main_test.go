package model_test

import (
	"os"
	"testing"
	"time"

	"github.com/go-kure/launcher/pkg/oam/model"
)

// TestMain stops the clock of the build deadline for every test of the package, so a host that
// stalls for over a second inside a build of a few milliseconds cannot fail a correct build
// (go-kure/launcher#1557). A test that needs another clock sets its own with model.SetBuildClock,
// and the two rows that test the real deadline put the real clock back for their own run.
// The clock is one package variable: a test that calls t.Parallel would share it with every other
// test, so add none to this package.
func TestMain(m *testing.M) {
	at := time.Now()
	model.SetBuildClock(func() time.Time { return at })
	os.Exit(m.Run())
}
