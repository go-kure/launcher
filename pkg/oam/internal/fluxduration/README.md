# Flux Duration Check

Package `fluxduration` (internal to `pkg/oam`) checks a duration string against the
form Flux's CRDs accept on their duration fields, so a value Flux would reject is
refused at build time instead of at apply time.

Flux declares the form as a `+kubebuilder:validation:Pattern` on `Interval` in the
source-controller `OCIRepository` and `HelmRepository`, helm-controller `HelmRelease`
and kustomize-controller `Kustomization` APIs, at the versions `go.mod` pins:

```text
^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$
```

`time.ParseDuration` alone is wider: it accepts a sign and the `ns`, `us` and `µs`
units.

- `Validate(value)` checks the authored text: `time.ParseDuration` must accept it
  (its own error is returned unchanged otherwise), and it must match the pattern
  (`ErrForm` otherwise). Used by the `reconciliation` policy, which hands the
  authored string on unchanged rather than emitting it itself.
- `ValidateEmitted(value)` also checks the form the value is emitted in. A caller
  that sets a `metav1.Duration` serializes `Duration.String()`, not the authored
  text, and that switches to `µs` or `ns` below one millisecond: `0.5ms` matches
  the pattern but is written as `500µs`, which Flux rejects. Such a value returns a
  `*ResolutionError` carrying the emitted form. So does a positive value below the
  nanosecond resolution of `time.Duration`, which `time.ParseDuration` truncates to
  zero without an error: `0.0000000001ms` would be written as `0s`. A value authored
  as zero (`0s`, `0ms`, `0h0m`, `0.000s`) is accepted. Used by the `oci` and
  `helmchart` components for `interval`, at parse time and again when the config
  generates its objects. In practice the accepted values are `0s` and anything of at
  least `1ms`.
