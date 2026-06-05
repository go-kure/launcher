# Errors

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/errors.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/errors)

The `errors` package provides structured error types and thin wrapping helpers used
across launcher. Application code uses these helpers instead of calling `fmt.Errorf`
directly, so that error wrapping is consistent and machine-inspectable.

## Structured error types

`ValidationError` is returned when a semantic constraint is violated. It serves two
shapes:

- **Enum errors** (unknown value): populate `Value` + `ValidValues` via
  `NewValidationError(field, value, component, validValues)`.
- **Custom-message errors** (missing field, bad format): populate `Message`.

`ParseError` is returned when a document fails YAML parsing; it carries `Kind`,
`File`, `Line`, `Column`, and the wrapped cause (`NewParseError(...)`), and supports
`errors.Unwrap`.

```go
import "github.com/go-kure/launcher/pkg/errors"

return errors.NewValidationError("type", "frobnicate", "webservice",
    []string{"webservice", "worker", "cronjob"})

var ve *errors.ValidationError
if errors.As(err, &ve) {
    // inspect ve.Field, ve.ValidValues, …
}
```

## Wrapping helpers

```go
return errors.Wrap(err, "reading application file")
return errors.Wrapf(err, "accessing %q", path)
return errors.New("description of error")
return errors.Errorf("invalid value: %s", val)
```

`Wrap`/`Wrapf` return `nil` when the input error is `nil`, so they are safe to use
unconditionally.

## API overview

| Symbol | Purpose |
|--------|---------|
| `ValidationError`, `NewValidationError` | Semantic constraint violations (enum or custom). |
| `ParseError`, `NewParseError` | YAML parse failures with location. |
| `New`, `Errorf` | Create plain/formatted errors. |
| `Wrap`, `Wrapf` | Wrap an error with context (nil-safe). |
| `Is`, `As` | `errors.Is`/`errors.As` passthroughs for inspection. |
