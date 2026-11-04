# Patch - Declarative Resource Patching

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/patch.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/patch)

The `patch` package provides a JSONPath-based system for declaratively modifying Kubernetes resources. It supports both TOML and YAML patch file formats with structure-preserving modifications and variable substitution.

## Scope and Status

`pkg/patch` is a supported, standalone library. It is **not** invoked by `kurel build` — that
pipeline is closed through explicit registration (author-declared `${var}` parameters plus
registered component, trait, and lowering-rule handlers), not universally typed: a handler may
declare a `PropertySchema` for an out-of-process validator to consume, but launcher's own authored
document path does not check property shape against it — see `pkg/oam/validate.go` and
`pkg/oam/property_validate.go`'s scope note. No implicit patch step is planned. `pkg/patch` is the
designated implementation for explicit, user-invoked patching (e.g. interactive patch mode), used
directly by callers that want to modify Kubernetes resources outside the kurel build path.

## Overview

Patches allow you to modify Kubernetes manifests without rewriting them. The system uses JSONPath expressions to target specific fields and applies changes while preserving the original YAML structure (comments, ordering, formatting).

## Patch File Formats

### TOML Format (`.kpatch`)

```toml
# Target a specific resource by kind and name
[deployment.myapp.spec]
replicas = 3

[deployment.myapp.spec.template.spec.containers.0]
image = "${app.image}:${app.tag}"
resources.requests.cpu = "200m"
resources.requests.memory = "256Mi"

# Append to a list with .-
[deployment.myapp.spec.template.spec.containers.-]
name = "sidecar"
image = "envoy:latest"
```

### YAML Format

```yaml
target:
  kind: Deployment
  name: myapp
patches:
  - path: spec.replicas
    value: 3
  - path: spec.template.spec.containers[0].image
    value: "nginx:latest"
```

## Quick Start

```go
import "github.com/go-kure/launcher/pkg/patch"

// Load patches from file
file, _ := os.Open("patches/customize.kpatch")
specs, err := patch.LoadPatchFile(file)

// Create patchable set with resources and patches
patchSet, err := patch.NewPatchableAppSet(resources, specs)

// Resolve targets and apply
resolved, err := patchSet.Resolve()
for _, r := range resolved {
    err := r.Apply()
}

// Write patched output
err = patchSet.WriteToFile("output.yaml")
```

## Key Features

### Variable Substitution

Patches support variable references that resolve against a parameter context:

```toml
[deployment.myapp.spec.template.spec.containers.0]
image = "${registry}/${image}:${tag}"
replicas = "${replicas}"
```

```go
varCtx := &patch.VariableContext{
    Variables: map[string]interface{}{
        "registry": "docker.io",
        "image":    "myapp",
        "tag":      "v1.0.0",
        "replicas": 3,
    },
}
specs, err := patch.LoadPatchFileWithVariables(file, varCtx)
```

### List Selectors

Target specific items in lists using selectors:

```toml
# By index
[deployment.myapp.spec.template.spec.containers.0]
image = "updated:latest"

# Append to list
[deployment.myapp.spec.template.spec.containers.-]
name = "new-container"

# By field value (name selector)
[deployment.myapp.spec.template.spec.containers.{name=myapp}]
image = "updated:latest"
```

### Structure Preservation

When using `NewPatchableAppSetWithStructure`, the original YAML document structure is preserved through patching, maintaining comments, key ordering, and formatting.

### Strategic Merge Patch

For broad document-level changes, strategic merge patch (SMP) deep-merges a partial YAML document into the target resource. Known Kubernetes kinds merge lists by key (e.g. containers by `name`); unknown kinds fall back to JSON merge patch (RFC 7386). On an unknown kind a nil patch changes nothing, the resource included; on a known kind the resource is normalized as for any patch, as described below. With a patch, both paths read a nil `map[string]any` or `[]any` in the resource or in the patch, through chains of those two types, as an empty one, so only an explicit `null` deletes a field, and a chain nested more than 1000 levels deep, or containing itself, is an error, in the resource as in the patch, which then stays unchanged; any other container type is left to `encoding/json`, so a nil `map[string]string` is `null`. On an unknown kind a nil map in a patch used to delete its field and now keeps it, as it does on a known kind; a patch nested deeper than 1000 levels used to merge there and is now refused, as it is on a known kind. On an unknown kind a nil `[]any` or `map[string]any` in the resource used to come out as `null` and now comes out empty, as on a known kind; a resource nested deeper than 1000 levels is now refused there too.

On both paths, known kind and JSON merge fallback alike, the merged object holds only the types of a decoded JSON document, so it can be deep-copied. Whole numbers are `int64`, even when written as `80.0` or `8e1`, and keep every digit up to `1e21` (`9007199254740993.0` is `9007199254740993`). Fractions are `float64`, and a whole number from `2^63` up to `1e21`, and a plain integer of any size beyond the `int64` range, is a `json.Number` that keeps its digits. Written with a point or an exponent, a whole number from `1e21` is a `float64`. A number written with a point or an exponent is read for its digits only up to 64 bytes (`maxNumberLiteral`, about twice the longest literal that matters, so that a very long literal costs no more than its length to copy); a longer one stays the `json.Number` it was, with its text unchanged, and is never rounded to a `float64`. A nil `[]any` or `map[string]any` becomes an empty one, at any depth through chains of those two types only, and an explicit `null` stays `null`; a nil map that is the whole document stays nil. Any other container type, and whatever it holds, goes to `encoding/json` as it is, so a nil `[]string` or `map[string]string` is `null`, and so is a nil `[]any` inside an array or a typed slice: patches reach this path decoded from YAML, which yields only `[]any` and `map[string]any`. A value that cannot be serialized to JSON is an error, and the resource is left unchanged. On an unknown kind a number used to come out as a `float64`, which rounded an integer beyond `2^53`; it now has the type a known kind gives it.

```yaml
- target: deployment.my-app
  type: strategic
  patch:
    spec:
      template:
        spec:
          containers:
          - name: main
            resources:
              limits:
                cpu: "500m"
          - name: sidecar
            image: envoy:v1.28
```

```go
// Enable kind-aware merging
lookup, _ := patch.DefaultKindLookup()
patchSet.KindLookup = lookup

// Detect conflicts before applying
resolved, reports, err := patchSet.ResolveWithConflictCheck()
```

SMP patches are applied before field-level patches. See [DESIGN.md](https://github.com/go-kure/launcher/blob/main/pkg/patch/DESIGN.md) for full specification.

## API Reference

### Loading Patches

| Function | Description |
|----------|-------------|
| `LoadPatchFile(r)` | Load with automatic format detection |
| `LoadPatchFileWithVariables(r, ctx)` | Load with variable substitution |
| `LoadTOMLPatchFile(r, ctx)` | Load TOML-format patches |
| `LoadYAMLPatchFile(r, ctx)` | Load YAML-format patches |

### Applying Patches

| Function | Description |
|----------|-------------|
| `NewPatchableAppSet(resources, patches)` | Create patchable set |
| `NewPatchableAppSetWithStructure(docSet, patches)` | Create with structure preservation |
| `ParsePatchLine(path, value)` | Parse a single patch operation |

### Strategic Merge Patch

| Function | Description |
|----------|-------------|
| `ApplyStrategicMergePatch(resource, patch, lookup)` | Apply SMP to a single resource |
| `DefaultKindLookup()` | Create a KindLookup from the built-in scheme |
| `DetectSMPConflicts(patches, lookup, gvk)` | Check pairwise conflicts among patches |
| `ResolveWithConflictCheck()` | Resolve patches with conflict detection |

`DetectSMPConflicts` compares every patch pair exactly once (`O(n²)` in the
number of patches targeting one resource) rather than sampling, so it stays
accurate; a resource's authored patch list is expected to stay small, which is
what keeps the quadratic cost unnoticeable in practice.

## Error Handling

Individual patch *operation* failures (`op.go` — `replace`, `delete`, `insert`, and the other
per-line ops applied to a document) and patch-target resolution failures are returned as
[`*errors.PatchError`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/errors#PatchError)
(`github.com/go-kure/launcher/pkg/errors`), carrying the failed operation, target path, resource
name, and the underlying cause where one exists:

```go
var pe *errors.PatchError
if errors.As(err, &pe) {
    // inspect pe.Operation, pe.Path, pe.ResourceName, pe.Reason
}
```

`errors.As(err, &pe)` also matches failures from `PatchableAppSet.Resolve()`'s target-lookup check
and `ResourceWithPatches.Apply()` (both build on the same `NewPatchError` calls above) even where a
caller in `set.go` wraps them again with `fmt.Errorf("...: %w", err)` — `%w` preserves the chain.

Two call paths never produce a `*errors.PatchError`, so `errors.As(err, &pe)` will not match them:
direct use of `ApplyStrategicMergePatch` (and its `applyJSONMergePatch`/`applyTypedSMP` helpers in
`strategic.go`), and the YAML-structure-preserving document mutation helpers
(`YAMLDocument.ApplyPatchesToDocument` / `ApplyStrategicPatchesToDocument` in `yaml_preserve.go`,
reached via `PatchableAppSet.WriteToFile`'s per-document apply loop, and
`UpdateDocumentFromResource`). Those return plain wrapped errors (`errors.Wrap`/`fmt.Errorf`). Do
not assume every error out of this package is a `*errors.PatchError` — check the call path.

## Related Packages

- [pkg/oam](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) - the `kurel build` pipeline; independent of this package (see Scope and Status above)
- [pkg/errors](https://pkg.go.dev/github.com/go-kure/launcher/pkg/errors) - structured error types, including `PatchError`
- [kure pkg/io](https://pkg.go.dev/github.com/go-kure/kure/pkg/io) - YAML parsing for patch targets
