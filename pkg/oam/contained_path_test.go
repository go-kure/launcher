package oam

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveInside(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	mustWrite := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustLink := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(dir, "a", "file.yaml"))
	mustWrite(filepath.Join(outside, "file.yaml"))
	mustLink("a/file.yaml", filepath.Join(dir, "inside-link.yaml"))
	mustLink(filepath.Join(outside, "file.yaml"), filepath.Join(dir, "file-link.yaml"))
	mustLink(outside, filepath.Join(dir, "dir-link"))

	want, err := filepath.EvalSymlinks(filepath.Join(dir, "a", "file.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a/file.yaml", "inside-link.yaml"} {
		got, err := ResolveInside(dir, rel)
		if err != nil || got != want {
			t.Errorf("ResolveInside(%q) = %q, %v; want %q", rel, got, err, want)
		}
	}
	for _, rel := range []string{"file-link.yaml", "dir-link/file.yaml", "../" + filepath.Base(outside) + "/file.yaml"} {
		if _, err := ResolveInside(dir, rel); !errors.Is(err, ErrPathOutsideDirectory) {
			t.Errorf("ResolveInside(%q): %v, want ErrPathOutsideDirectory", rel, err)
		}
	}
	// A path that does not exist is the filesystem's error, not a containment verdict.
	if _, err := ResolveInside(dir, "missing.yaml"); err == nil || errors.Is(err, ErrPathOutsideDirectory) {
		t.Errorf("ResolveInside(missing): %v, want the filesystem's error", err)
	}
}
