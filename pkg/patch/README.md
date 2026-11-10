# Patch - Declarative Resource Patching

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/patch.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/patch)

The `patch` package provides a JSONPath-based system for declaratively modifying Kubernetes resources. It supports both TOML and YAML patch file formats with structure-preserving modifications and variable substitution.

## Scope and Status

`pkg/patch` is a supported, standalone library for callers that want to modify Kubernetes
resources in Go. It is **not** part of `kurel build`, and no other package in this repository
imports it. `kurel` has no patch subcommand (its commands are `build`, `config`, `completion` and
`version`), and no implicit patch step is planned. Unlike a Flux Kustomization patch or a Helm
post-renderer, it does not run in a cluster or in a render pipeline: the caller loads the
resources, applies the patches and writes the result.

### What is validated, and where

The `kurel build` pipeline checks authored input in its own stages, none of which use this
package:

1. Parameters are substituted into the authored document.
2. `ValidateAuthoredPropertiesForTarget` checks the authored properties: the keys, their types,
   nested required fields and, when the profile names a Kubernetes version, feature gates. It
   does not require a top-level property to be present; a handler or a capability supplies and
   checks those. It returns the validated copy.
3. `Transform` runs on that copy, then manifest generation and the in-document collision check.

A handler may also declare a `PropertySchema` for an out-of-process validator; see the scope note
in `pkg/oam/property_validate.go`.

`pkg/patch` checks patch syntax in two stages. Loading a TOML file or a list of targeted YAML
entries checks the field segments of each path up to its selector. That check first trims the dots
at the start and at the end of that part, so `containers..[0]` reads as `containers[0]`. A TOML
header also loses repeated dots: `[deployment.myapp.spec..template]` means the same as
`[deployment.myapp.spec.template]`.
After that, an empty segment in that part is refused. A selector is split off the path when the
line is parsed, and so is the path written after a selector in the middle of a path
(`containers[0].resources..cpu`). Both are read when the patch is applied. After a replacement
selector such as `[0]`, that read trims the dots at the start and at the end of the path
(`containers[0]..image.` reaches `image`). After an insertion selector (one that starts with `+` or
`-`, such as `[+0]`), the text is kept as written, so `containers[+0]..image.` inserts a map whose
key is `.image.`. No path check refuses an empty segment after the selector. A key can hold more
than one selector. The first one must be a replacement selector, and each next one selects an item
in the list that the one before it selected: `containers[0].ports[name=http].containerPort` sets the
port of the first container. A selector right after a selector (`ports[0][1]`) is not supported.
Loading also
substitutes variables: a TOML file in its values, a YAML file in its keys and values, a strategic
patch in its payload values. It does not check them, and a reference that no variable defines stays
as written. A path in a flat YAML mapping is parsed at load, but the check of its segments up to
the selector (the trim and the empty-segment refusal above) runs only when the set is built.
Building the set and applying a patch check the target and that part of the path. It does not check
a value against a schema, and it does not run the `kurel build` checks above. Kubernetes admission
is the check that sees the final object.

## Overview

Patches allow you to modify Kubernetes manifests without rewriting them. The system uses JSONPath expressions to target specific fields and applies changes while preserving the original YAML structure (comments, ordering, formatting).

## Patch File Formats

### TOML Format (`.kpatch`)

The header `[kind.name]` names the resource. Each line under it is `path: value`, split at
the first colon, so a value may contain colons (`nginx:1.27`). It is not TOML `key = value`:
a line without a colon is refused.

A value has no type of its own in this format. A value in double quotes is a string, whatever
it holds (`"8080"`, `"true"`), and its escapes are read as in Go (`"a\tb"`). An unquoted value is
a bool when it reads `true` or `false`, in any case, and an integer when the last part of its
path is a known integer field (`replicas`, `port`, `containerPort`, and similar). Any other
unquoted value stays a string. A whole `${values.x}` reference keeps the type of the variable.
The path is the header path plus the key, so a key of only dots (`.: 3` under
`[deployment.app.spec.replicas]`) sets the field the header names and gives the integer 3.

```toml
# Target a resource by kind and name
[deployment.myapp]
spec.replicas: 3
metadata.labels.env: production

# A section after the name maps to the Kubernetes path: "containers" on a
# Deployment is spec.template.spec.containers. A last part of name=value
# selects the list item with that name.
[deployment.myapp.containers.name=myapp]
image: nginx:1.27
resources.requests.cpu: 200m
resources.requests.memory: 256Mi

# A last part that is a number selects the list item by index.
[deployment.myapp.containers.0]
imagePullPolicy: Always
```

### YAML Format

The loader reads a YAML patch file in one of two shapes, chosen by the first node. A file that
holds anything else is refused.

A mapping is a flat list of `path: value` entries. A path whose first part is a resource name
picks that resource (`myapp.spec.replicas`; `kind.name` is not accepted here, because the path is
split at every dot). A path with no resource name applies to the one resource that accepts it.
Loading the file does not check that: `NewPatchableAppSet` and `NewPatchableAppSetWithStructure`
fail when none or several resources accept the path.

```yaml
myapp.spec.replicas: 3
myapp.metadata.labels.env: production
```

A YAML value keeps the type YAML gave it: `8080` is an integer, `"8080"` is a string, and
`"true"` is a string. The loader does not infer a type from the key. A mapping value, or a list
of mappings, is substituted and applied like one in the targeted shape below. A mapping with a
non-string key is substituted in its values like any other mapping. Its keys are not substituted.

A sequence is a list of targeted entries. Each has a `target` (a resource name, `kind.name`, or
`namespace/kind.name`) and a `patch` mapping of paths relative to that resource. A list index is
written `[0]`, as in `containers[0].image`.

```yaml
- target: deployment.myapp
  patch:
    spec.replicas: 3
    spec.template.spec.containers[0].image: nginx:1.27
- target: deployment.myapp
  patch:
    metadata.labels.env: production
```

An entry with `type: strategic` holds a partial resource in `patch` and is merged as described
under Strategic Merge Patch below. Any other `type` is refused.

## Quick Start

Load the patches and the manifests, build the set, and write the patched file. The manifests are
loaded with their layout, so the output keeps comments and key order:

```go
import (
    "os"

    "github.com/go-kure/launcher/pkg/patch"
)

func patchFile() error {
    pf, err := os.Open("patches/customize.kpatch")
    if err != nil {
        return err
    }
    defer pf.Close()
    specs, err := patch.LoadPatchFile(pf) // TOML or YAML, detected
    if err != nil {
        return err
    }

    mf, err := os.Open("base/deployment.yaml")
    if err != nil {
        return err
    }
    defer mf.Close()
    docs, err := patch.LoadResourcesWithStructure(mf)
    if err != nil {
        return err
    }

    set, err := patch.NewPatchableAppSetWithStructure(docs, specs)
    if err != nil {
        return err
    }
    // WriteToFile resolves and applies the patches itself.
    return set.WriteToFile("output.yaml")
}
```

`WriteToFile` needs the layout, so it fails with "no document set available" on a set built by
`NewPatchableAppSet`. To patch objects in memory, resolve and apply each one, then read its
`Base`:

```go
resources, err := patch.LoadResourcesFromMultiYAML(manifests)
if err != nil {
    return err
}
set, err := patch.NewPatchableAppSet(resources, specs)
if err != nil {
    return err
}
resolved, err := set.Resolve()
if err != nil {
    return err
}
for _, r := range resolved {
    if err := r.Apply(); err != nil {
        return err
    }
    // r.Base is the patched *unstructured.Unstructured
}
```

## Key Features

### Variable Substitution

Patches support `${values.<key>}` and `${features.<flag>}` references that resolve against a
`VariableContext`. A reference to a name the context does not hold is left as written, and
with a nil context nothing is substituted.

A value that is one whole reference (`${values.replicas}`) takes the type of the variable: an
`int` stays an `int`, and a string `"3"` stays a string. A reference inside longer text
(`n${values.a}`, or two references) gives a string.

```toml
[deployment.myapp]
spec.replicas: ${values.replicas}

[deployment.myapp.containers.name=myapp]
image: ${values.registry}/${values.image}:${values.tag}
```

```go
varCtx := &patch.VariableContext{
    Values: map[string]any{
        "registry": "docker.io",
        "image":    "myapp",
        "tag":      "v1.0.0",
        "replicas": 3,
    },
    Features: map[string]bool{"debug": true},
}
specs, err := patch.LoadPatchFileWithVariables(file, varCtx)
```

### List Selectors

Target items in a list with the last part of a TOML header:

```toml
# By index
[deployment.myapp.containers.0]
image: updated:latest

# By field value (name selector)
[deployment.myapp.containers.name=myapp]
image: updated:latest
```

A header may also spell the start of a shorthand path. `[deployment.myapp.spec.template.spec.containers.0]` means the same as `[deployment.myapp.containers.0]`, and the spelled part is not repeated. Under a container item, `ports` is the container field, so `[deployment.myapp.containers.0.ports]` does not become the Service `spec.ports`.

To append to a list, end the path with `[-]`. The value is a mapping, so use the YAML form:

```yaml
- target: deployment.myapp
  patch:
    spec.template.spec.containers[-]:
      name: sidecar
      image: envoy:v1.28
```

### Structure Preservation

When using `NewPatchableAppSetWithStructure`, the original YAML document structure is preserved through patching, maintaining comments, key ordering, and formatting. A timestamp that no patch changes is written back in its authored style: `created: 2026-11-07` stays plain, and an explicit `!!timestamp` tag stays. A timestamp that a patch sets is a string in the resource, so it is written quoted (`created: "2026-12-01"`), also when it has the same text as before. Three limits affect the quoting style only, never a value. A patch that adds, replaces or inserts a list item by index or by key leaves every timestamp in that list quoted. After a delete, items that pair by position (mapping items with no merge key, and lists inside a list) may stay quoted. Where one list holds the same text in two styles, such as `"2026-11-07"` and a plain timestamp, a delete can leave the survivor in the style of the removed item. Pairing the scalar items of a list with their authored items takes time linear in the list length.
### Value Types in Loaded Resources

`LoadResourcesFromMultiYAML` and `LoadResourcesWithStructure` turn the YAML values of a base resource into types that `Unstructured.DeepCopy` accepts, so `PatchableAppSet.Resolve` can copy the resource:

- An unquoted integer becomes `int64`.
- A quoted scalar (`"true"`, `"8080"`) keeps its authored string type, in both loaders.
- A plain timestamp keeps its authored text and loads as a string, not as a `time.Time`. An explicit `!!timestamp` tag is treated the same way when the text parses as a time.
- A mapping with a non-string key (`1: a`, `0x10: b`) loads as a string-keyed mapping. The key is its `fmt.Sprint` spelling. A write-back keeps a plain key as authored (`0x10`, `.nan`, `1.0`) and writes no second key for the converted spelling. An alias key (`*k`) is kept as the alias, and a timestamp under it keeps its authored style. The same holds for a key in a list item, at any depth. One exception: when a patch adds a key with the text of an authored key (the string `"0x10"` beside `0x10`), the authored key takes its converted spelling (`16`) and the new key is written quoted, so that the document still loads. The known limits below name the keys it does not keep.
- Two keys of one mapping that read the same after that conversion are an error: `0x10` and `"16"`, or `0x10` and `16`. Both loaders refuse the document. For `0x10` and `16` the YAML decoder would otherwise merge the two into one entry and drop the first value, so the loaders check the keys before the decode. The loaders decode each key into the key type the YAML decoder gives its mapping, and compare the results. A key and an alias of it read the same in every mapping, the root included. An alias value is followed to its anchored mapping and checked where it is used, so a mapping defined where the decoder never reads it (a merged entry the mapping overrides, a field a patch entry does not declare) is still refused when an alias reads it. An anchor that no alias reads is not read, and its document loads. The keys of a merge source (`<<`) are decoded into the mapping that names it, and a merged entry that the mapping already holds is skipped, as the decoder skips it. The root mapping of a document is read into a string-keyed map, so `0x10` and `16` at the root load as two keys. So do the keys of a merge source that a string-keyed mapping names. A `.nan` key keeps its value.
- A patch file is checked the same way when it loads, in the simple form and in the list form, and the file is not used: two keys that decode to one value (`0x10` and `16`) are refused at load. Two keys of different types that read the same only after conversion (`0x10` and `"16"`) load, and are refused when the patch is applied. A `PatchOp` that code builds by hand is refused when the patch is applied, and the resource is not changed.

#### Known limits

- An unsigned integer above the `int64` range is not converted.
- A list with no merge key pairs two items by position only when the written list has as many items as the authored one and the two items hold the same data. After an add, a delete, a reorder or a change of the item, its keys take the converted spelling (`"16"`), and the document still loads. The same holds for items that share one merge-key value: none of them pairs, and none keeps its authored key text or its comments.
- A patch can add a key with the text of a tagged key (`!!binary`), and the tagged key would then take a spelling that another key of the mapping holds. The write-back then writes the patched resource as it stands. The document loads, and all its comments are lost.
- A patch can move a list item that holds an alias key (`*k`) before the item that declares its anchor (`&k`). The merged text would then not load. The write-back checks that the merged text loads again, and if it does not, it writes the patched resource as it stands. The document loads, and all its comments are lost.
- An alias key (`*k`) can read an anchor that sits on a scalar value (`source: &k 0x10`). A write-back replaces a scalar value and drops its anchor, so the merged text does not load. The write-back then writes the patched resource as it stands: the document loads, the data is right, and all its comments are lost. The fix is tracked in go-kure/launcher#1408.
- A list item (in a list with no merge key) can hold a tagged key that decodes to the authored text of a plain key (`!!binary MHgxMA==` decodes to `0x10`, and the plain key `0x10` reads `"16"`). The write-back restores the tagged key. It writes the plain key with its converted spelling (`"16"`), because it checks the spellings the marshaler wrote. The document loads. The fix is tracked in go-kure/launcher#1409.
- A key at the root of a document can be an alias to a tagged key (`&k !!binary YWJj` under `metadata.annotations`, and `*k` at the root). The loader reads the alias as `abc`, and the root pairs its keys by authored text (`YWJj`), so the merge adds `abc` beside the alias. The merged text does not load. The write-back then writes the patched resource as it stands: the document loads, the data is right, and the alias and all comments are lost. The fix is tracked in go-kure/launcher#1410.

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

Three call paths do not produce a `*errors.PatchError`, so `errors.As(err, &pe)` will not match
them: direct use of `ApplyStrategicMergePatch` (and its `applyJSONMergePatch`/`applyTypedSMP`
helpers in `strategic.go`), `YAMLDocument.ApplyStrategicPatchesToDocument`, and
`YAMLDocument.UpdateDocumentFromResource` (both in `yaml_preserve.go`). They return plain wrapped
errors (`errors.Wrap`/`fmt.Errorf`). So do the resource loaders and the patch file loader when two
keys of one mapping read the same after conversion (`loader.go`, `op.go`; see Value Types in Loaded
Resources). The loaders in `loader.go` build these plain errors with `Wrap`, `Wrapf`, `Errorf` and
`New` from `pkg/errors`; their text is the same as before.

`YAMLDocument.ApplyPatchesToDocument` is not one of them. It applies each field patch with the same
operation code as above and wraps a failure with `%w`. So a failed field patch matches
`errors.As(err, &pe)`, also through `PatchableAppSet.WriteToFile`, whose per-document apply loop
wraps with `%w` too. Two failures carry a `*errors.ValidationError` and no `*errors.PatchError`: an
unknown `Op`, and a bad `Selector` on an array replace (`applyArrayReplace` in `op.go`).

The strategic apply in the `WriteToFile` loop wraps with `%w` as well, so `errors.Is` and
`errors.As` still reach the underlying error. That path never builds a `*errors.PatchError`, so
`errors.As(err, &pe)` finds none. `pkg/patch/patcherror_paths_test.go` pins these paths. Do not
assume every error out of this package is a `*errors.PatchError`. Check the call path.

## Related Packages

- [pkg/oam](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) - the `kurel build` pipeline; independent of this package (see Scope and Status above)
- [pkg/errors](https://pkg.go.dev/github.com/go-kure/launcher/pkg/errors) - structured error types, including `PatchError`
- [kure pkg/io](https://pkg.go.dev/github.com/go-kure/kure/pkg/io) - YAML parsing for patch targets
