package oam

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"gopkg.in/yaml.v3"
)

// Parse parses an Application YAML document in strict mode.
// Unknown fields are rejected; the document is semantically validated before returning.
func Parse(data []byte) (*Application, error) {
	var app Application

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	if err := dec.Decode(&app); err != nil {
		return nil, yamlParseError(err)
	}

	if err := validate(&app); err != nil {
		return nil, err
	}

	return &app, nil
}

// ParseMulti parses a multi-document YAML stream. Each document is validated
// independently. Returns an error if no documents are found or any document fails.
func ParseMulti(data []byte) ([]*Application, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var apps []*Application
	for {
		var app Application
		if err := dec.Decode(&app); err != nil {
			if stderrors.Is(err, io.EOF) {
				break
			}
			return nil, yamlParseError(err)
		}
		if err := validate(&app); err != nil {
			return nil, err
		}
		apps = append(apps, &app)
	}

	if len(apps) == 0 {
		return nil, errors.NewParseError("Application", "", 0, 0,
			fmt.Errorf("no documents found"))
	}
	return apps, nil
}

// MustParse parses an Application YAML document and panics on error.
// Use only in tests or initialization code where errors are truly unexpected.
func MustParse(data []byte) *Application {
	app, err := Parse(data)
	if err != nil {
		panic(err)
	}
	return app
}

// yamlParseError converts a yaml decode error into a ParseError,
// extracting line information from yaml.TypeError when available.
func yamlParseError(err error) *errors.ParseError {
	var typeErr *yaml.TypeError
	if stderrors.As(err, &typeErr) && len(typeErr.Errors) > 0 {
		line := extractYAMLLine(typeErr.Errors[0])
		return errors.NewParseError("Application", "", line, 0, err)
	}
	return errors.NewParseError("Application", "", 0, 0, err)
}

// extractYAMLLine parses the line number from a yaml.TypeError error string.
// yaml.TypeError strings have the form "line N: <message>".
func extractYAMLLine(s string) int {
	rest, ok := strings.CutPrefix(s, "line ")
	if !ok {
		return 0
	}
	numStr, _, ok := strings.Cut(rest, ":")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(numStr))
	if err != nil {
		return 0
	}
	return n
}
