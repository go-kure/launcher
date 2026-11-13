package model

import "time"

// SetBuildClock replaces the time source of the build deadline and returns the undo.
func SetBuildClock(f func() time.Time) func() {
	old := buildClock
	buildClock = f
	return func() { buildClock = old }
}

// SetBuildCostLimit replaces the cost limit of a build and returns the undo.
func SetBuildCostLimit(n int64) func() {
	old := buildCostLimit
	buildCostLimit = n
	return func() { buildCostLimit = old }
}
