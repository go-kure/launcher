package patch

import (
	"fmt"

	"github.com/go-kure/launcher/pkg/errors"
)

// skipsPatchFile reports whether a patch file is skipped, not failed, because
// err says a target it names is not among the base resources. The decision
// rests on the type, so rewording the message does not change it.
func skipsPatchFile(err error) bool {
	var notFound *TargetNotFoundError
	return errors.As(err, &notFound)
}

// TargetNotFoundError reports that a patch target names no resource in the base
// resources. Test for it with errors.As; its text is for people to read.
//
// Explicit is true when the patch gave the target itself (PatchSpec.Target)
// rather than leaving it to be found from the path.
type TargetNotFoundError struct {
	Target   string
	Explicit bool
}

// Error returns the message, which does not carry any extra wrapped context.
func (e *TargetNotFoundError) Error() string {
	if e.Explicit {
		return "explicit target not found: " + e.Target
	}
	return fmt.Sprintf("target %q not found in base resources", e.Target)
}
