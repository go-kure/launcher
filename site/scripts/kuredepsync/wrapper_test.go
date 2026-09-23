package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wrapper script fetches its own --base ref. That fetch must never turn a full
// clone shallow: a --depth fetch into a full clone writes
// .git/shallow and grafts the fetched commit as a root, silently breaking rebase and
// merge-base for every linked worktree (go-kure/launcher#463). These tests run the
// real script against a throwaway origin/clone pair and assert
// `git rev-parse --is-shallow-repository` is unchanged across a --base run.

// launcherRoot is the launcher checkout this package lives in (site/scripts/kuredepsync).
const launcherRoot = "../../.."

// fixtureEnv isolates git from the developer's global/system config and gives the
// fixture commits a fixed identity.
func fixtureEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
	)
}

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = fixtureEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s (in %s): %v\n%s", name, strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitFile writes content to name in repo and commits it, returning the new HEAD.
func commitFile(t *testing.T, repo, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, repo, "git", "add", name)
	runCmd(t, repo, "git", "commit", "-q", "-m", "fixture: "+name)
	return runCmd(t, repo, "git", "rev-parse", "HEAD")
}

// newOrigin builds a bare origin whose main carries launcher's go.mod/go.sum, the
// wrapper script and the helper sources, plus enough history (two more commits) that
// a depth-1 fetch has something to cut off. It returns the bare repo's file:// URL
// (a plain path would take git's local-clone shortcuts and ignore --depth) and the
// seed working repo used to push further commits.
func newOrigin(t *testing.T) (url, seed string) {
	t.Helper()
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "origin.git")
	seed = filepath.Join(tmp, "seed")

	runCmd(t, tmp, "git", "init", "-q", "--bare", "-b", "main", bare)
	runCmd(t, bare, "git", "config", "uploadpack.allowAnySHA1InWant", "true")
	runCmd(t, tmp, "git", "init", "-q", "-b", "main", seed)

	copyFile(t, filepath.Join(launcherRoot, "go.mod"), filepath.Join(seed, "go.mod"))
	copyFile(t, filepath.Join(launcherRoot, "go.sum"), filepath.Join(seed, "go.sum"))
	copyFile(t, filepath.Join(launcherRoot, "site/scripts/check-kure-dep-sync.sh"),
		filepath.Join(seed, "site/scripts/check-kure-dep-sync.sh"))
	for _, f := range []string{"go.mod", "go.sum", "main.go"} {
		copyFile(t, f, filepath.Join(seed, "site/scripts/kuredepsync", f))
	}
	runCmd(t, seed, "git", "add", "-A")
	runCmd(t, seed, "git", "commit", "-q", "-m", "fixture: launcher files")
	commitFile(t, seed, "one.txt", "1\n")
	commitFile(t, seed, "two.txt", "2\n")

	url = "file://" + bare
	runCmd(t, seed, "git", "remote", "add", "origin", url)
	runCmd(t, seed, "git", "push", "-q", "origin", "main")
	return url, seed
}

func isShallow(t *testing.T, repo string) string {
	t.Helper()
	return runCmd(t, repo, "git", "rev-parse", "--is-shallow-repository")
}

// runWrapper runs the fixture clone's own copy of the script with --base.
func runWrapper(t *testing.T, clone, base string) {
	t.Helper()
	runCmd(t, clone, "bash", "site/scripts/check-kure-dep-sync.sh", "--base", base)
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"git", "bash", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH: %v", tool, err)
		}
	}
}

func TestWrapperBaseFetchKeepsFullCloneFull(t *testing.T) {
	requireTools(t)

	t.Run("remote-tracking base", func(t *testing.T) {
		url, seed := newOrigin(t)
		clone := filepath.Join(t.TempDir(), "clone")
		runCmd(t, "", "git", "clone", "-q", url, clone)
		if got := isShallow(t, clone); got != "false" {
			t.Fatalf("fixture precondition: clone is-shallow=%s, want false", got)
		}
		// Advance origin/main past the clone: the script must still refresh a
		// remote-tracking base, not settle for the stale local copy.
		tip := commitFile(t, seed, "four.txt", "4\n")
		runCmd(t, seed, "git", "push", "-q", "origin", "main")

		runWrapper(t, clone, "origin/main")

		if got := isShallow(t, clone); got != "false" {
			t.Errorf("--base origin/main turned a full clone shallow (is-shallow=%s)", got)
		}
		if got := runCmd(t, clone, "git", "rev-parse", "origin/main"); got != tip {
			t.Errorf("origin/main not refreshed: got %s, want %s", got, tip)
		}
	})

	t.Run("missing commit base", func(t *testing.T) {
		url, seed := newOrigin(t)
		clone := filepath.Join(t.TempDir(), "clone")
		runCmd(t, "", "git", "clone", "-q", url, clone)

		// A commit the clone has never seen, reachable only from a branch the clone
		// did not fetch — forces the script's fetch-on-miss path.
		runCmd(t, seed, "git", "checkout", "-q", "-b", "side")
		side := commitFile(t, seed, "three.txt", "3\n")
		runCmd(t, seed, "git", "push", "-q", "origin", "side")

		runWrapper(t, clone, side)

		if got := isShallow(t, clone); got != "false" {
			t.Errorf("--base <missing sha> turned a full clone shallow (is-shallow=%s)", got)
		}
		runCmd(t, clone, "git", "cat-file", "-e", side+"^{commit}")
	})
}

// An already-shallow clone (CI's checkout) keeps today's depth-1 behaviour and stays
// shallow; the fix must not start deepening it either.
func TestWrapperBaseFetchKeepsShallowCloneShallow(t *testing.T) {
	requireTools(t)

	url, seed := newOrigin(t)
	clone := filepath.Join(t.TempDir(), "clone")
	runCmd(t, "", "git", "clone", "-q", "--depth=1", url, clone)
	if got := isShallow(t, clone); got != "true" {
		t.Fatalf("fixture precondition: clone is-shallow=%s, want true", got)
	}
	// Advance origin/main so the refresh has something to fetch.
	tip := commitFile(t, seed, "four.txt", "4\n")
	runCmd(t, seed, "git", "push", "-q", "origin", "main")

	runWrapper(t, clone, "origin/main")

	if got := isShallow(t, clone); got != "true" {
		t.Errorf("shallow clone is-shallow=%s after --base run, want true", got)
	}
	if got := runCmd(t, clone, "git", "rev-list", "--count", "origin/main"); got != "1" {
		t.Errorf("shallow clone was deepened: origin/main has %s commits locally, want 1", got)
	}
	if got := runCmd(t, clone, "git", "rev-parse", "origin/main"); got != tip {
		t.Errorf("origin/main not refreshed: got %s, want %s", got, tip)
	}
}
