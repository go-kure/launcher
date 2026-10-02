# Flux Duration Check

Package `fluxduration` (internal to `pkg/oam`) checks a duration string against the
form Flux's CRDs accept on their duration fields, so a value Flux would reject is
refused at build time instead of at apply time.

Flux declares the form as a `+kubebuilder:validation:Pattern` on each duration
field, at the API versions `go.mod` pins. It uses two, and a `Form` names each:

- `Interval`: every helm-controller `HelmRelease` duration field (`interval`,
  `timeout`, `chart.spec.interval`, the per-action timeouts and the strategy
  `retryInterval`s) and `spec.interval` on the source-controller and
  kustomize-controller kinds.

  ```text
  ^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$
  ```

- `SourceTimeout`: `spec.timeout` on the source-controller `HelmRepository`,
  `OCIRepository`, `GitRepository` and `Bucket` kinds. It has no `h` unit.

  ```text
  ^([0-9]+(\.[0-9]+)?(ms|s|m))+$
  ```

`time.ParseDuration` alone is wider: it accepts a sign and the `ns`, `us` and `µs`
units.

- `Form.Validate(value)` checks the authored text: `time.ParseDuration` must accept
  it (its own error is returned unchanged otherwise), and it must match the
  pattern (`ErrForm` otherwise). The package-level `Validate` is
  `Interval.Validate`.
- `Form.Format(d)` is the text a parsed duration is emitted as: `Duration.String()`,
  which a `metav1.Duration` serializes to, except under `SourceTimeout`, where a
  duration of an hour or more has its hours folded into its minutes (`1h30m0s` as
  `90m0s`), since `Duration.String()` would write an `h` that pattern refuses. A
  caller emitting such a duration writes this text in place of the
  `metav1.Duration`'s.
- `Form.ValidateEmitted(value)` also checks the form the value is emitted in,
  `Form.Format`, not the authored text, and that switches to `µs` or `ns` below one
  millisecond: `0.5ms` matches the pattern but is written as `500µs`, which Flux
  rejects. Such a value returns a `*ResolutionError` carrying the emitted form. So
  does a positive value below the nanosecond resolution of `time.Duration`, which
  `time.ParseDuration` truncates to zero without an error: `0.0000000001ms` would be
  written as `0s`. A value authored as zero (`0s`, `0ms`, `0h0m`, `0.000s`) is
  accepted. The package-level `ValidateEmitted` is `Interval.ValidateEmitted`.
- `Form.ValidateDuration(d)` checks a decoded duration's emitted form alone, for a
  config built directly rather than parsed: `ErrForm` for a negative one, and
  `*ResolutionError` as above.

In practice the accepted values are `0s` and anything of at least `1ms`; under
`SourceTimeout`, as authored, without `h`. The kind-named Flux components (`helmrelease`,
`helmrepository`, `ocirepository`, `gitrepository`, `bucket`, `helmchart`) check every duration
field this way, and the `oci` component its `interval`, at parse
time and again when the config generates its objects. The `reconciliation` policy
checks its `interval`, `retryInterval` and `timeout` with the package-level
`ValidateEmitted`: the Flux `Kustomization` generated from each bundle carries them
as `metav1.Duration`, and all three take the `Interval` form.
