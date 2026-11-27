//go:build !race

package model

// raceEnabled is false when the test binary is built without the race detector (see race_on_test.go).
const raceEnabled = false
