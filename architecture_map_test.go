package launcher

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mapRow matches one row of the Map table in ARCHITECTURE.md: a path in backticks
// in the first cell.
var mapRow = regexp.MustCompile("^\\|\\s*`((?:pkg|cmd)/[^`]+)`\\s*\\|")

// TestArchitectureMapListsEveryPackage keeps the Map in ARCHITECTURE.md complete:
// every directory under pkg/ and cmd/ that holds Go source has a row, and every
// row names such a directory.
func TestArchitectureMapListsEveryPackage(t *testing.T) {
	data, err := os.ReadFile("ARCHITECTURE.md")
	if err != nil {
		t.Fatalf("read ARCHITECTURE.md: %v", err)
	}
	documented, dupes := parseMapRows(string(data))
	for _, d := range dupes {
		t.Errorf("the Map of ARCHITECTURE.md names %s twice", d)
	}
	if len(documented) == 0 {
		t.Fatal("no Map rows found in ARCHITECTURE.md: the table format changed, or the file moved")
	}

	actual := goDirs(t)
	for dir := range actual {
		if !documented[dir] {
			t.Errorf("%s holds Go source but has no row in the Map of ARCHITECTURE.md: add one", dir)
		}
	}
	for dir := range documented {
		if !actual[dir] {
			t.Errorf("the Map of ARCHITECTURE.md has a row for %s, which holds no Go source: remove or fix the row", dir)
		}
	}
}

// TestParseMapRows pins what counts as a row: only a table line in the Map section,
// never one inside an HTML comment or a fenced code block, and a path named twice
// is reported.
func TestParseMapRows(t *testing.T) {
	doc := strings.Join([]string{
		"## Elsewhere",
		"| `pkg/other` | not in the Map |",
		"## Map",
		"| `pkg/real` | a row |",
		"<!--",
		"| `pkg/commented` | hidden in a comment |",
		"-->",
		"<!-- | `pkg/inline` | hidden in one line | -->",
		"```",
		"| `pkg/fenced` | inside a code block |",
		"```",
		"~~~markdown",
		"| `pkg/tilde` | inside a tilde block |",
		"~~~",
		"|  `cmd/tool`  | a row with wider cell spacing |",
		"| `pkg/real` | the same path again |",
		"## After",
		"| `pkg/after` | not in the Map |",
	}, "\n")

	rows, dupes := parseMapRows(doc)

	for _, want := range []string{"pkg/real", "cmd/tool"} {
		if !rows[want] {
			t.Errorf("row %s was not found", want)
		}
	}
	for _, not := range []string{"pkg/other", "pkg/commented", "pkg/inline", "pkg/fenced", "pkg/tilde", "pkg/after"} {
		if rows[not] {
			t.Errorf("%s was counted as a Map row", not)
		}
	}
	if len(dupes) != 1 || dupes[0] != "pkg/real" {
		t.Errorf("duplicates = %v, want [pkg/real]", dupes)
	}
}

// parseMapRows returns the paths named in the Map section of a Markdown document,
// and the paths it names more than once. Text inside an HTML comment or a fenced
// code block is not part of the table.
func parseMapRows(doc string) (rows map[string]bool, dupes []string) {
	rows = map[string]bool{}
	inMap, inComment := false, false
	fence := "" // the delimiter that opened the current code block, or ""
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if inComment {
			if strings.Contains(line, "-->") {
				inComment = false
			}
			continue
		}
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			fence = "```"
			continue
		}
		if strings.HasPrefix(trimmed, "~~~") {
			fence = "~~~"
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") {
			if !strings.Contains(trimmed, "-->") {
				inComment = true
			}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			inMap = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "Map"
			continue
		}
		if !inMap {
			continue
		}
		if m := mapRow.FindStringSubmatch(line); m != nil {
			if rows[m[1]] {
				dupes = append(dupes, m[1])
			}
			rows[m[1]] = true
		}
	}
	return rows, dupes
}

// goDirs returns every directory under pkg/ and cmd/ that holds a non-test Go
// file, skipping testdata trees.
func goDirs(t *testing.T) map[string]bool {
	t.Helper()
	dirs := map[string]bool{}
	for _, root := range []string{"pkg", "cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				dirs[filepath.ToSlash(filepath.Dir(path))] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return dirs
}
