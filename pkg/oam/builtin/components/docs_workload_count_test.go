package components_test

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// numeral matches either spelling of a small count; the package uses both.
const numeral = `(\d+|one|two|three|four|five|six|seven|eight|nine|ten)`

// workloadKindCountRes match a written-out count in the phrases this package
// uses when it generalises over the whole workload family. Three shapes, kept
// separate rather than merged into one alternation so each stays readable:
//
//   - "the six workload kinds", "the six direct workload kinds"
//   - "all six kinds", "all six kind handlers", "all six kind components"
//   - "the six kind components", "the six kind handlers"
//
// The bare "the N kinds" is deliberately NOT matched: the package legitimately
// says "the three kinds are not layered" (deployment, webservice, worker) and
// "those two kinds", which count a subset, not the family. The totalising
// qualifier — "all", or the "kind components"/"kind handlers" noun — is what
// distinguishes a family-wide claim from a subset one. A new phrasing that
// generalises over the family without one of these shapes escapes this guard;
// prefer an existing spelling over inventing a fourth.
var workloadKindCountRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b` + numeral + `\s+(?:direct\s+)?workload kinds`),
	regexp.MustCompile(`(?i)\ball\s+` + numeral + `\s+kinds?\b`),
	regexp.MustCompile(`(?i)\bthe\s+` + numeral + `\s+kind\s+(?:components|handlers)\b`),
}

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
			for _, re := range workloadKindCountRes {
				for _, m := range re.FindAllStringSubmatch(s.Text(), -1) {
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
