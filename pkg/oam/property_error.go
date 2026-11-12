package oam

import (
	"fmt"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

// PropertyErrorKind says what a PropertyError refuses.
type PropertyErrorKind string

const (
	// PropertyErrorUnsupportedField is a key the schema does not declare.
	PropertyErrorUnsupportedField PropertyErrorKind = "unsupported-field"
	// PropertyErrorRequired is a Required key the properties leave out.
	PropertyErrorRequired PropertyErrorKind = "required"
	// PropertyErrorExclusive is a breach of an exclusive group: two of its keys set, or
	// none of a group that needs one.
	PropertyErrorExclusive PropertyErrorKind = "exclusive"
	// PropertyErrorType is a value of a type the schema does not allow.
	PropertyErrorType PropertyErrorKind = "type"
	// PropertyErrorEnum is a value outside the schema's allowed set.
	PropertyErrorEnum PropertyErrorKind = "enum"
	// PropertyErrorRange is an integer outside the range of int.
	PropertyErrorRange PropertyErrorKind = "range"
	// PropertyErrorNullElement is a null array element.
	PropertyErrorNullElement PropertyErrorKind = "null-element"
	// PropertyErrorSchema is a defect in the handler's own schema, not in the document.
	PropertyErrorSchema PropertyErrorKind = "schema"
	// PropertyErrorPlatformReserved is an authored value for a property the platform
	// reserves for ClusterProfile capability rendering (go-kure/launcher#1469). It
	// wraps ErrPlatformReserved, so errors.Is finds that too.
	PropertyErrorPlatformReserved PropertyErrorKind = "platform-reserved"
)

// PropertyError is the refusal of a property the author wrote, or of the schema it is
// checked against (go-kure/launcher#1466). Error returns the refusal text, which names
// the location in prose; the fields below hold the same location for a caller that
// maps it, so none has to read it back out of the text. Find one with errors.As; the
// error ValidateAuthoredProperties returns may wrap it. The platform-reservation
// refusal is one too, of kind PropertyErrorPlatformReserved: it is also found with
// errors.Is against ErrPlatformReserved.
type PropertyError struct {
	Kind PropertyErrorKind
	// Position is where in the document the refused properties sit: "component",
	// "trait" or "policy". It is empty where no document position is known: a
	// refusal made on a schema or on a property a rule emitted.
	Position string
	// Component is the component's name, for a trait the one it sits on. Trait is the
	// trait's type, and Policy the policy's name. Each is empty where it does not apply.
	Component string
	Trait     string
	Policy    string
	// Field is the refused location below the properties: dotted keys, an array element
	// as [n], for example "probes.httpGet[0].port". For an unsupported-field and a
	// required refusal it is the key itself; for an exclusive refusal the object holding
	// the group, and empty for the top level; for a schema refusal the node whose
	// declaration is wrong. Empty also where Position is.
	Field string

	msg string
	// The location as the refusal text spells it: the path, and a key below it. keyed
	// says there is one, because an empty key is a key.
	at, key string
	keyed   bool
	// An unsupported-field refusal also keeps what a handler's hint reads.
	allowed string
	parents []string
	// cause is the sentinel a platform-reserved refusal wraps. Only that refusal sets
	// it, so Unwrap is nil for every other kind.
	cause error
}

func (e *PropertyError) Error() string { return e.msg }

// Unwrap returns the sentinel a platform-reserved refusal wraps, ErrPlatformReserved,
// and nil for every other kind.
func (e *PropertyError) Unwrap() error { return e.cause }

// propertyErrorf is a refusal at path, worded "<path>: <text>".
func propertyErrorf(kind PropertyErrorKind, path, format string, args ...any) *PropertyError {
	return &PropertyError{Kind: kind, at: path, msg: path + ": " + fmt.Sprintf(format, args...)}
}

// withKey records the key the refusal names below its path.
func (e *PropertyError) withKey(key string) *PropertyError {
	e.key, e.keyed = key, true
	return e
}

// locate sets where err is, when err refuses a property: where carries the position
// and the names, prefix is the path every refusal of that position starts from, and
// Field is what follows it.
func locate(err error, prefix string, where PropertyError) error {
	var pe *PropertyError
	if !errors.As(err, &pe) {
		return err
	}
	pe.Position, pe.Component, pe.Trait, pe.Policy = where.Position, where.Component, where.Trait, where.Policy
	full := pe.at
	if pe.keyed {
		full += "." + pe.key
	}
	pe.Field = strings.TrimPrefix(strings.TrimPrefix(full, prefix), ".")
	return err
}
