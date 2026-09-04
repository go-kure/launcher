package components_test

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// workloadKindCountRe matches a written-out count immediately in front of the
// phrase this package uses whenever it generalises over the workload family —
// "the six workload kinds", "the six direct workload kinds". Both spellings of
// the numeral are accepted because the package uses both.
var workloadKindCountRe = regexp.MustCompile(`(?i)\b(\d+|one|two|three|four|five|six|seven|eight|nine|ten)\s+(?:direct\s+)?workload kinds`)

var spelledNumbers = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

// TestDocs_WorkloadKindCountIsCurrent guards a defect class this package has
// now produced twice: adding a workload kind leaves prose and code comments
// elsewhere in the package asserting the old count, and each stale sentence is
// found one at a time by a reviewer rather than by the suite. Every such count
// is checked against the real number of workload kinds, in the README and in
// every Go file of the package, so the next kind fails the suite at each site
// it did not reach instead of shipping a document that contradicts itself.
func TestDocs_WorkloadKindCountIsCurrent(t *testing.T) {
	want := len(workloadKinds)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".md")) {
			continue
		}
		// This file carries the pattern itself and the counts in its own
		// documentation; scanning it would match the regexp source.
		if name == "docs_workload_count_test.go" {
			continue
		}
		checked++

		f, err := os.Open(name)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for line := 1; s.Scan(); line++ {
			for _, m := range workloadKindCountRe.FindAllStringSubmatch(s.Text(), -1) {
				got, ok := spelledNumbers[strings.ToLower(m[1])]
				if !ok {
					got, err = strconv.Atoi(m[1])
					if err != nil {
						t.Errorf("%s:%d: %q: unparsable count", name, line, m[0])
						continue
					}
				}
				if got != want {
					t.Errorf("%s:%d: %q says %d workload kinds, but the package has %d — update it",
						name, line, m[0], got, want)
				}
			}
		}
		if err := s.Err(); err != nil {
			f.Close()
			t.Fatalf("scan %s: %v", name, err)
		}
		f.Close()
	}

	if checked == 0 {
		t.Fatal("scanned no files — the guard would pass vacuously")
	}
}
