// Package errors provides launcher's structured error types (ValidationError,
// ParseError, PatchError) and thin wrapping helpers (New, Errorf, Wrap, Wrapf,
// Is, As). Application code uses these instead of calling fmt.Errorf, so that
// wrapping is consistent and callers can test an error with Is and As.
package errors
