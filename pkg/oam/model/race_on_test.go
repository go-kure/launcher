//go:build race

package model

// raceEnabled is true when the test binary is built with the race detector, which raises the
// allocation of some evaluations (reading a string copies it), so the megabyte bounds of the budget
// tests are not asserted under it.
const raceEnabled = true
