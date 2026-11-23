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
	documented, problems := parseMapRows(string(data))
	for _, p := range problems {
		t.Error(p)
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

// TestParseMapRows pins what the guard reads: table rows of the Map section only, a
// path named twice is a problem, and a comment or code block inside the Map section
// is refused, because the guard reads tables and nothing else.
func TestParseMapRows(t *testing.T) {
	doc := strings.Join([]string{
		"## Elsewhere",
		"| `pkg/other` | not in the Map |",
		"## Map",
		"| `pkg/real` | a row |",
		"|  `cmd/tool`  | a row with wider cell spacing |",
		"| `pkg/real` | the same path again |",
		"## After",
		"| `pkg/after` | not in the Map |",
	}, "\n")
	rows, problems := parseMapRows(doc)
	for _, want := range []string{"pkg/real", "cmd/tool"} {
		if !rows[want] {
			t.Errorf("row %s was not found", want)
		}
	}
	for _, not := range []string{"pkg/other", "pkg/after"} {
		if rows[not] {
			t.Errorf("%s was counted as a Map row", not)
		}
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "pkg/real") {
		t.Errorf("problems = %v, want one naming pkg/real twice", problems)
	}

	for _, hidden := range []string{
		"<!--",
		"text <!-- | `pkg/inline` | hidden | -->",
		"```",
		"````markdown",
		"~~~",
	} {
		_, problems := parseMapRows("## Map\n" + hidden + "\n| `pkg/real` | a row |")
		if len(problems) != 1 || !strings.Contains(problems[0], "comment or code block") {
			t.Errorf("a Map section holding %q: problems = %v, want one about a comment or code block", hidden, problems)
		}
	}
}

// parseMapRows returns the paths named in the Map section of a Markdown document,
// and a message for each problem it finds: a path named twice, or a comment or code
// block inside the section (the guard would count a row such a block hides).
func parseMapRows(doc string) (rows map[string]bool, problems []string) {
	rows = map[string]bool{}
	inMap := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "## ") {
			inMap = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "Map"
			continue
		}
		if !inMap {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.Contains(line, "<!--") || strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			problems = append(problems, "the Map section holds a comment or code block; the guard reads tables only: "+trimmed)
			continue
		}
		if m := mapRow.FindStringSubmatch(line); m != nil {
			if rows[m[1]] {
				problems = append(problems, "the Map of ARCHITECTURE.md names "+m[1]+" twice")
			}
			rows[m[1]] = true
		}
	}
	return rows, problems
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
