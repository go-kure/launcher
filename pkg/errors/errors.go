package errors

import (
	"errors"
	"fmt"
	"strings"
)

// ValidationError is returned when a semantic constraint is violated.
// For enum-type errors (unknown type values), Value and ValidValues are populated.
// For custom-message errors (missing fields, invalid format), Message is populated.
type ValidationError struct {
	Field       string   // the field that failed
	Component   string   // application name or context (e.g. "application")
	Value       string   // the rejected value (enum errors)
	ValidValues []string // valid options (enum errors)
	Message     string   // custom message (non-enum errors)
}

func (e *ValidationError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if len(e.ValidValues) > 0 {
		return fmt.Sprintf("invalid value %q for %q in %q; must be one of: %s",
			e.Value, e.Field, e.Component, strings.Join(e.ValidValues, ", "))
	}
	return fmt.Sprintf("invalid value %q for %q in %q", e.Value, e.Field, e.Component)
}

// NewValidationError creates a structured enum-type ValidationError.
// Use this when a field value is not in the set of known valid values.
func NewValidationError(field, value, component string, validValues []string) *ValidationError {
	return &ValidationError{
		Field:       field,
		Value:       value,
		Component:   component,
		ValidValues: validValues,
	}
}

// ParseError is returned when a document fails YAML parsing.
type ParseError struct {
	Kind    string
	File    string
	Line    int
	Column  int
	Wrapped error
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("parse error in %s at line %d: %v", e.Kind, e.Line, e.Wrapped)
	}
	return fmt.Sprintf("parse error in %s: %v", e.Kind, e.Wrapped)
}

func (e *ParseError) Unwrap() error { return e.Wrapped }

// NewParseError creates a ParseError for the given document kind.
func NewParseError(kind, file string, line, col int, cause error) *ParseError {
	return &ParseError{Kind: kind, File: file, Line: line, Column: col, Wrapped: cause}
}

// PatchError is returned when a patch operation fails to apply.
type PatchError struct {
	Operation    string // the patch operation, e.g. "replace", "delete", "resolve"
	Path         string // the target path within the resource, if any
	ResourceName string // the resource the patch targeted, if known
	Reason       string // human-readable reason for the failure
	Cause        error
}

func (e *PatchError) Error() string {
	msg := fmt.Sprintf("patch %q failed", e.Operation)
	if e.ResourceName != "" {
		msg += fmt.Sprintf(" on %q", e.ResourceName)
	}
	if e.Path != "" {
		msg += fmt.Sprintf(" at %q", e.Path)
	}
	if e.Reason != "" {
		msg += fmt.Sprintf(": %s", e.Reason)
	}
	if e.Cause != nil {
		msg += fmt.Sprintf(": %v", e.Cause)
	}
	return msg
}

func (e *PatchError) Unwrap() error { return e.Cause }

// NewPatchError creates a PatchError describing a failed patch operation.
func NewPatchError(operation, path, resourceName, reason string, cause error) *PatchError {
	return &PatchError{
		Operation:    operation,
		Path:         path,
		ResourceName: resourceName,
		Reason:       reason,
		Cause:        cause,
	}
}

// New returns a new error with the given message.
func New(msg string) error { return errors.New(msg) }

// Errorf returns a new formatted error.
func Errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// Wrap wraps err with a context message. Returns nil if err is nil.
func Wrap(err error, msg string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", msg, err)
}

// Wrapf wraps err with a formatted context message. Returns nil if err is nil.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), err)
}

// Is reports whether any error in err's tree matches target.
func Is(err, target error) bool { return errors.Is(err, target) }

// As finds the first error in err's tree that matches target and sets target to that value.
func As(err error, target any) bool { return errors.As(err, target) }
