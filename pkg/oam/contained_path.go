package oam

import (
	"path/filepath"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// ErrPathOutsideDirectory is the error ResolveInside returns when a path leaves its
// directory.
var ErrPathOutsideDirectory = errors.New("the path resolves outside its directory")

// ResolveInside returns the file rel names under dir, with every symlink on the way
// resolved, and refuses one whose target is outside dir. The text of a relative path
// can be checked local and a symlink can still lead out of dir, so the resolved
// target is what is checked and what the caller must read. A path that cannot be
// resolved (it does not exist, say) returns the filesystem's own error, for the caller
// to name; one that leaves dir returns ErrPathOutsideDirectory.
func ResolveInside(dir, rel string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(base, rel))
	if err != nil {
		return "", err
	}
	inner, err := filepath.Rel(base, target)
	if err != nil || inner == ".." || strings.HasPrefix(inner, ".."+string(filepath.Separator)) {
		return "", ErrPathOutsideDirectory
	}
	return target, nil
}
