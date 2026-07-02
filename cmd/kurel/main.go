package main

// R3 warm-cache probe: trivial one-package change to measure fallback restore. Revert.

import (
	"github.com/go-kure/launcher/pkg/cmd/kurel"
)

func main() {
	kurel.Execute()
}
