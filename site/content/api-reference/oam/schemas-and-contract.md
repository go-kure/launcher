---
title: "Property schemas and contract metadata"
weight: 6
---

## Property schemas

Handlers may implement `PropertySchemaProvider` (`PropertySchema() map[string]PropertySchema`)
to declare a constrained schema for their user-facing properties. `PropertySchema` is launcher's
single schema vocabulary — the same type also backs `kurel.yaml` parameters (`ParameterDecl`) and
`CapabilityDefinition` rendering properties. It has `Type` (string/integer/boolean/number/array/object),
`Types`, `Description`, `Required`, `Default`, `Enum`, nested `Properties`, `Items`, `AdditionalProperties`
(default false; escape-hatch fields set it true), and `Exclusive`. The rich fields (`Types`, `Enum`, `Properties`, `Items`,
`AdditionalProperties`, `Exclusive`) are meaningful only for handler properties: the two flat call sites (kurel
parameters, capability rendering) reject them at decode time, so unifying the type does not widen
their accepted behavior.

A kurel parameter's `Type` is `string`, `integer`, `boolean`, `array` or `object`
(go-kure/launcher#421). `ResolveParameters` replaces a whole-value `${name}` for an `array` or
`object` parameter with the value's YAML list or map. With no `items` or `properties` to declare,
the parameter checks only that shape (its default too, and a string default is refused); the
substituted value is checked by `ValidateAuthoredProperties` against the consuming handler's schema
like any authored property. Such a parameter cannot be embedded in a larger string, in the template
or in another parameter's string default.

`Types` is the union idiom (go-kure/launcher#383): a leaf that accepts more than one scalar type
lists them, and a value is accepted when any member's single `Type` accepts it — whatever order the
members are listed in — normalised exactly as the first member, in declared order, that accepts it
would (below). Members are two or more distinct scalar types (string, integer,
number, boolean); `Type` and `Types` are mutually exclusive. A schema that sets both, or a malformed
union, is a schema error reported as soon as a value reaches the leaf. Every Kubernetes
`intstr.IntOrString` leaf — the rolling-update `maxUnavailable`/`maxSurge` knobs, the networkpolicy
`port`, the `service` kind's `ports[].targetPort` — declares `integer`/`string`, and every `resource.Quantity` leaf (`cpu`, `memory`, a claim's
`storage`) declares `string`/`number`, because their parsers take a fractional number too. `Type`
stays empty on a union leaf, so a schema consumer that does not read `Types` sees an untyped leaf and
keeps accepting every member. A completeness test (`pkg/cmd/kurel`) enforces that every built-in
schema node declares exactly one of `Type` and `Types`. `Transformer.HandlerSchemas()` returns a `HandlerSchemaSet{ Components, Traits, Policies, Exclusive }`
of every registered handler and lowering rule that declares one, so the downstream runtime's validator can check a
component's, trait's or policy's properties before the handler is invoked. Built-in examples: the `configmap` trait and the
`passthrough` component.

`Exclusive` is the one-of idiom (go-kure/launcher#790): on an `object` node it lists groups of that
object's `Properties` keys, `ExclusiveGroup{ Keys, Required }`, of which a value sets at most one, and
exactly one when the group is `Required`. A key counts as set when it is present and not a null, as
for `Required`. A handler's top level is a map with no node to carry the groups, so a handler or
lowering rule declares them through the optional `ExclusivePropertiesProvider`
(`ExclusiveProperties() []ExclusiveGroup`), and `HandlerSchemaSet.Exclusive` publishes them by
position and type name. `Exclusive` is nil when no handler declares a top-level group, so a set
without one marshals as it did before the field existed; the nested groups travel in the schemas
themselves. A group's `Required` is relaxed exactly where field `Required` is: an authored document's
top level is held to at most one only, and a capability-bound trait's nested groups are checked on
the properties merged with the rendering, where a merge that sets two keys of a group is refused
naming the capability. A component's capability defaults (`ComponentCapabilityDefaults`) are held to
the authored bound once merged: a default that fills one key of a top-level group beside an authored
key of it is refused, naming the capability. Emitted properties are held to both bounds at every level. A group of fewer
than two keys, a key the object does not declare, a key in two groups, a `Required` key, or groups
on a non-object node is a schema error. Property validation always refuses it, whatever the
document supplies: `ValidateAuthoredProperties*` and the check of emitted properties hold the whole
schema, objects and array items the document leaves out included. So does `Transform` wherever a
capability rendering may supply values the author did not write (a trait it applies or lowers, a
component's capability defaults); a caller that skips `ValidateAuthoredProperties*` gets no other
schema check, as for every other property rule. It is reported before the document's errors about that object's keys
(`Required` and group membership); a reserved-property or value-type error of the document may be
reported first. A consumer
that does not read `Exclusive` sees each key as the optional key it is declared as.
`CheckSchemaNodeGroups(field, path)` applies those five rules to one declared node and does not
recurse, for a caller that walks a schema itself and spells the path itself. `model.Load` calls it
for each node of the input schemas it visits, so a malformed group in a model is refused at load.

`Description` is optional (`json:"description,omitempty"`) but every built-in property populates it —
including nested object fields and array item schemas at every depth — so the downstream runtime can surface prose in
its generated Handler API Reference. A completeness test (`pkg/cmd/kurel`) enforces that no built-in
schema node is left without a description.

A schema field may also be marked `PlatformReserved`: its value may arrive only via
`ClusterProfile` capability rendering, never authored inline. `enforcePlatformReserved`
(`property_validate.go`) rejects an authored value for such a field — including an
explicit `null` — before capability rendering is merged in, wrapping
`ErrPlatformReserved`; it walks declared nested object fields and the items of a
declared array of objects too (named `properties.items[0]`), so a reservation on an
inner field is enforced wherever it is declared, not only at the top level
(go-kure/launcher#635).
`createApplications` and `applyTraits` (`transform.go`) run this check on the authored
path, and the lowering engine runs it on a `TraitLoweringRule`'s input trait before
capability rendering is resolved into it (see [Lowering](transform-and-lowering.md#lowering)). A value a lowering rule
recorded with `Component.RenderReserved` or `Trait.RenderReserved` is exempt while the
key still holds it (see [Lowering](transform-and-lowering.md#lowering)).

A schema field may also name a Kubernetes feature gate in `Gate` (published as `gate`):
the field needs that gate on the cluster. A `ClusterProfile` that states
`spec.kubernetesVersion` below the release where the gate is on by default has an authored
value refused by `ValidateAuthoredPropertiesForTarget`, naming the property's path, the gate,
that release and the profile's version; with no version nothing is checked. The gates are
rows of one table in `featuregates.go` (name, first release with Default true, read from the
Kubernetes feature gates reference), held to the schemas by tests in both directions: a `Gate`
no row names, or a row no schema uses, fails (go-kure/launcher#1183). Only Kubernetes core
properties of the built-in components carry one today: `EnvFiles`
(`env[].valueFrom.fileKeyRef`), `ContainerRestartRules` (`restartPolicyRules`: an init
container's on the workload kinds, any container's on the raw-PodSpec kinds below), `PodLevelResources` (`podResources`) and `HostnameOverride`
(`hostnameOverride`). The check reads the declared schema, so it stops at an open object
(`AdditionalProperties` with no `Properties`). The raw-PodSpec kinds (`pod` and the kinds
that take a pod template, and the `template` of `cnpg-pooler`) leave their containers and template spec open, and their open
nodes carry `GatedPaths` (published as `gatedPaths`) instead: gate-relative paths
(`env[].valueFrom.fileKeyRef`, with `[]` after an array key) to the gate the field needs,
read by the gate check alone, so property validation and what a document is accepted as do
not change (go-kure/launcher#1245). A consumer that predates `gatedPaths` ignores it.
The gate check reads a value as the writer writes it. A property value built in Go can
hold a struct from the Kubernetes API types (a `corev1.PodSpec` in a template, a
`[]corev1.EnvVar`) where a decoded document holds a map. The check reads such a struct, a
pointer to one, or a slice of either, through its JSON form, so it is held to the same
gates as the decoded document. A struct that encodes to null is absent, as a decoded null
is. A struct that does not encode as JSON is refused, with its path, where a gate
below it is off at the target (go-kure/launcher#1320). The read comes before the null
check, so a nil slice of a named type whose own `MarshalJSON` writes an array is present,
and a pointer type that is its own element type (`type P *P`) is read through its JSON
form like any other (go-kure/launcher#1450).

A gate may also cover single values of an enum property: `ValueGates` (published as
`valueGates`) maps an `Enum` value to the gate that value alone needs, and the property is
accepted at every version for its other values. An authored value listed there is refused
below the gate's release with the same fields plus the value
(`value "Never" requires the Kubernetes feature gate ContainerRestartRules, ...`). A key
that is not an `Enum` value, or a gate no row names, fails the schema tests. Like `Gate`,
the key is rejected at the two flat call sites, so it is absent from their allow-sets
(go-kure/launcher#1230). Two properties carry it today: an init container's
`restartPolicy` (`Never` and `OnFailure`, `ContainerRestartRules`) and `service`'s
`trafficDistribution` (`PreferSameZone` and `PreferSameNode`, `PreferSameTrafficDistribution`).
`GatedPaths` names a field, not a value. A path that gates values sits in `ValueGatedPaths`
(published as `valueGatedPaths`): a gate-relative path maps to a map from a value to its
gate, and the check walks the path as it walks `GatedPaths`. The raw-PodSpec kinds use it
for the `restartPolicy` of an init container, from the same table as the shorthand
(`Never` and `OnFailure`, `ContainerRestartRules`). Their `restartPolicy` on a regular
container is a plain `GatedPaths` entry, because Kubernetes forbids the field there with
the gate off, `Always` included. A null, a value that is not a string and a string the table
does not name use no gate. Like `Gate`, the key is rejected at the two flat call sites
(go-kure/launcher#1330).

Separately, `validateProperties` (`property_validate.go`) checks an EMITTED
component/trait/policy's properties against its TARGET handler's declared schema —
enforcing `Required`, `Type`/`Types`, `Enum`, and nested `Properties`/`Items`/
`AdditionalProperties` — immediately after a lowering rule returns it, so a rule
cannot silently produce properties its own target handler would reject.

Both this check and the authored one below write a normalised value back where the
handler's reader would not accept what was supplied: a typed Go collection becomes
`[]any`/`map[string]any`; a `string`, `boolean` or `number` value of a named Go type
(`type Mode string`) becomes its predeclared type; and an `integer` value of kind
`int8`, `int16` or any unsigned kind, or of any named integer type
(`type Replicas int32`), becomes `int` — a value outside the `int` range is rejected
instead. Values already of type `int`, `int32`, `int64` or `float64` are left as they
are, because some readers accept `int` but not `int64`; a named integer type becomes
`int` rather than its underlying type for the same reason. A `Types` union leaf is
rewritten by whichever member the value matched. A property whose schema declares
neither `Type` nor `Types`, or a key an open object leaves undeclared, is not rewritten.

`ValidateAuthoredProperties` (`property_validate_authored.go`) is that check's
authored-path counterpart, and closes go-kure/launcher#408. Parsing is strict
(`KnownFields(true)`) only down to the envelope: `Component.Properties`,
`Trait.Properties` and `ApplicationPolicy.Properties` are each `map[string]any`, so
before this every handler read the keys it knew and silently dropped the rest — a
misspelled or stale property built successfully and produced nothing. The check walks
each authored component and its traits in document order, looks up the same schema
`validateProperties` would (terminal handler first, then a `ComponentLoweringRule` /
`TraitLoweringRule` claiming the type), rejects any key the schema does not declare,
and checks the shape of every key it does. `kurel build` calls it immediately after
parsing — and, in package mode, necessarily *after* `ResolveParameters`, because a
`${...}` placeholder is a bare string until substituted.

**A reason on a refused key.** The refusal of an undeclared *top-level* key can
carry the reason the type has for it. A component handler, a component lowering
rule, a trait handler or a trait lowering rule that has the method
`UnsupportedFieldHint(key string) string` is asked for the refused key, and a
non-empty answer is appended to the refusal after `"; "`:

```
component "batch" (type "job"): properties: unsupported field "scheduling" (allowed: …); scheduling: not read by this component — alpha upstream, behind the WorkloadWithJob feature gate
```

The built-in hand-parsed kinds answer with the reason they refuse a field of the
Kubernetes type they project, `webservice` and `worker` with their `deployment`'s,
and the `pvc` trait with the `persistentvolumeclaim` kind's
(`pkg/oam/builtin/components/README.md`, "Upstream fields a hand-parsed kind does
not read"); `helmchart` and `helmrelease` point a removed key to its replacement,
and `postgresql` a key its `backup` replaced (`objectStore`, `objectStoreObjectName`)
to where it went (go-kure/launcher#1001).
An empty answer adds nothing. The hint was asked of a terminal component
handler alone before go-kure/launcher#790; the three other positions are new.

A key refused *below* the top level, inside an object the type declares, is
never passed to `UnsupportedFieldHint`, so a hint written for a top-level key is
not given for a nested key of the same name. At the same four positions, the
optional method `NestedUnsupportedFieldHint(parents []string, key string) string`
is asked instead. `parents` are the declared keys from the top level down to the
object that refuses the key, list items unnamed: `initContainers[0].probes` is
asked as `["initContainers"]`, `"probes"`. A non-empty answer is appended the same
way:

```
component "web" (type "webservice"): properties.affinity: unsupported field "nodeAffinity" (allowed: …); affinity.nodeAffinity: not read — …
```

The built-in workload kinds, `webservice` and `worker` answer with the reason
their parser gives for the key at that position (the components README, "Below
the top level"), and `postgresql` names the replacement of a key `backup` no longer
takes (`destinationPath`, `endpointURL`, `secretName`) and the reason `backup.objectStore` refuses a key
(`serverName`, `azureCredentials`, `googleCredentials`; go-kure/launcher#1001). Before go-kure/launcher#790 a nested refusal carried no hint.
The refusal text itself is unchanged at every depth.

**Where a refusal is, as data.** Every refusal the property validators make is a
`*PropertyError` (go-kure/launcher#1466): `errors.As` finds it in the error the call
returns, a hinted refusal included, and `Error()` is the text above, byte for byte.
The one exception is the platform-reservation check described below: its refusal
wraps `ErrPlatformReserved`, which `errors.Is` finds, and it names its location in
its text only. A caller that maps a refusal to a reply of its own reads these fields
and parses no text:

| Field | Holds |
|---|---|
| `Kind` | `unsupported-field`, `required`, `exclusive`, `type`, `enum`, `range`, `null-element`, or `schema` (the handler's own schema is malformed, so the document is not at fault) |
| `Position` | `component`, `trait` or `policy`; empty where the refusal was made on no authored document (a schema, a property a rule emitted) |
| `Component`, `Trait`, `Policy` | the component's name (for a trait, the one it sits on), the trait's type, the policy's name; empty where they do not apply |
| `Field` | the location below `properties`: dotted keys, array elements as `[n]`, as in `probes.httpGet[0].port`. For `unsupported-field` and `required` it is the key itself; for `exclusive` the object holding the group, empty at the top level; for `schema` the node whose declaration is wrong |

```go
var pe *oam.PropertyError
if errors.As(err, &pe) && pe.Kind == oam.PropertyErrorRequired {
    // pe.Component, pe.Trait and pe.Field name the missing key
}
```

`Field` is the path as the refusal builds it, so a key that itself holds a dot
reads the same as a nested one (`a.b` is one key or two); the kind and the
position are exact either way. A refusal that names a document position is made
by the three boundaries of this check; the same refusal made by `Transform`'s
emission checks carries a kind and no position. The exported helper
`CheckSchemaNodeGroups` returns the `schema` kind too.

Before any of that, it runs `Transform`'s own reservation check
(`enforcePlatformReserved`) over the document, with the same schemas and the same
error text `Transform` would report for it. It has to come first: validation drops an
explicit `null` under a nested declared object, so a reserved key authored as
`config: {locked: null}` would otherwise be gone before `Transform` could refuse it
(go-kure/launcher#635).

The shape check is also the only place an authored scalar's *type* is enforced for
every property (go-kure/launcher#325). Many built-in handlers read string properties
with a comma-ok assertion (`cfg.Delivery, _ = props["delivery"].(string)`), so on
the handler alone a `delivery: 123` becomes `""` and is defaulted as though it were
absent. Type-checking once here, against the schema each handler already declares,
is what rejects it — not a per-field check in each handler. A caller that drives
`Transform` directly must therefore call `ValidateAuthoredPropertiesWithCapabilities`
first (after any parameter substitution, with the bindings `EvaluateProfile`
returned) to get the same guarantee `kurel build` gives. `ValidateAuthoredProperties`
is the same check for a caller with no profile.

The validators return the document they validated, together with the error:
`validated, err := tr.ValidateAuthoredProperties(app)`. It is a copy of `app` whose
property trees carry the result of the check: under the schema-declared fields, an
explicit nested `null` is stripped, and a named scalar or a typed collection is
rewritten to the plain Go shape a handler reads. A key accepted through
`AdditionalProperties` is left as authored, as before. The `app` argument is never
edited, for a plain type name and for a declared type identity alike
(go-kure/launcher#1282). `Transform` strips and normalizes
nothing itself, so **`Transform` the document the validator returned**, not `app`: a
nested `null` in `app` still reaches the handler as `nil`. On an error the returned
document is nil and `app` is as it was authored. A nil `app` returns nil and no error.

Authored policies are checked the same way, after every component, in document order:
against the `PolicyHandler` registered for the type, else the `PolicyLoweringRule`
claiming it. The built-in policy handlers each declare a `PropertySchema`, so a
misspelt `placement` key such as `teir` is a build error rather than a
setting the handler never reads.

Three positions are exempt, each because there is no schema to check against: a
component or trait type no handler and no lowering rule claims (rejected separately
by the type allowlists and by `validateSettled`); a custom trait type from a
`CapabilityDefinition`, which declares that the type *exists* but not what properties
it accepts; and a policy type nothing is registered for, which the transform rejects
with `no handler for policy type`.

`Required` is enforced at the top level too, as written, and a null counts as absent
(go-kure/launcher#1463): `component "web" (type "webservice"): properties: "image" is
required`. Two cases are left to the platform and are not refused here. A trait a
capability binding matches is checked on the merged properties, as described below, because
`ClusterProfile` capability rendering merges into its top-level property map after this
runs. A component key that its handler lists in `ComponentCapabilityDefaults` is filled by
the capability, so its absence is accepted.

Nested `Required`, inside an object the author did write, is enforced
(go-kure/launcher#765), and top-level `Required` on a matched trait follows the same
rule:

- `ValidateAuthoredProperties` knows no profile, so it checks nested `Required` as
  written, on every trait.
- `ValidateAuthoredPropertiesWithCapabilities(app, capabilities)` does the same for a
  trait no binding matches. For a trait a binding matches, it checks the trait's
  properties as the rendering merges into them, recursively
  (go-kure/launcher#750), so a required key the rendering supplies is not refused:
  a partial override such as `issuerRef: {kind: Issuer}` over a rendered
  `issuerRef: {name: ca}` is accepted. A missing key is then reported as `"name" is
  required; neither the trait nor capability "certificate"'s rendering sets it`. A
  required key inside an array element is always checked as written, because a
  rendering never merges into a list.
- `ValidateAuthoredPropertiesForTarget(app, capabilities, target)` is the capability
  variant plus the feature-gate check (`PropertySchema.Gate`, above) for a cluster at
  `target`, a `*KubernetesVersion` (`ClusterProfileSpec.TargetVersion` returns the
  profile's). Everything the capability variant refuses is refused first. A nil `target`
  is an unknown version, and the result is the capability variant's, the validated copy
  included. `Transform` does not
  read the version: the check only refuses, so an accepted document renders the same at
  any version. `ParseKubernetesVersion` takes `1.35`, `v1.35` and `v1.35.2` (patch ignored)
  and refuses anything else, a major other than 1 included.
- `Transform` makes the same check itself on every trait's merged properties, at both
  merge sites (trait dispatch and the lowering fixpoint), whether or not a binding
  matched. This is new for a caller that runs `Transform` without the validator: a
  nested required key missing from both the trait and the rendering used to reach the
  handler and is now refused. `kurel build` already ran the validator, so its output
  is unchanged.

The Required keys inside an object are checked only when that object is present after
the merge. An optional object property that is left out entirely is not checked, as at
the top level. An object property that is itself Required and is left out is refused as
a missing key, like any other.

One property is legal on **every** trait regardless of what its handler declares:
`scope`. It is read by the transform engine rather than by a handler —
`buildCapabilityKey` builds the `"<traitType>.<scope>"` capability key for every trait
type, so a `pvc` or `certificate` trait can select a scoped `ClusterProfile` binding
even though neither handler declares such a property. `expose`, `ingress` and
`httproute` do declare it, but for an unrelated reason (sub-application naming), and
their own declaration wins over the engine default. Engine-owned keys are checked even
on a trait whose handler or lowering rule declares no `PropertySchema` at all (for
example a custom handler registered through `RegisterTrait`): a non-string `scope`
there is rejected, while the handler's own keys stay unchecked because nothing
declares them. A handler that refuses keys it does not read (for example
`topology-spread`, which takes none) asks `IsEngineTraitProperty` and lets these
through. It cannot tell an authored `scope` from one a capability rendering merged
in, and a rendered one selects nothing (the binding was already chosen), so such a
handler also refuses rendering keys in `ValidateAndApplyDefaults`, at
`EvaluateProfile`. Add to `engineTraitProperties` if
another engine-read property is ever introduced; the merge never mutates the
handler's returned schema, so `HandlerSchemas` still advertises only what each handler
actually declares.

`validateProperties`'s null check (`isNullValue`) treats a typed-nil pointer,
slice, or map — not just a bare `nil` interface — as `null`: a Go type assertion
alone can't tell an uninitialized slice/map apart from a validly-typed empty
collection, so a lowering rule that emits an unset (rather than empty) collection
field is still caught.

`IsNullValue` is the exported form of that check, and with it the contract itself:
**a null value is absent, not present-and-empty.** The predicate is nil-ness, not
serialization, and that distinction is load-bearing rather than pedantic: whether
the two shapes serialize differently depends on the ENCODER. `encoding/json` (and
`sigs.k8s.io/yaml`, which routes through it) writes `null` for a nil map or slice
and `{}`/`[]` for an allocated empty one, but `gopkg.in/yaml.v3` — the YAML library
this package parses with — renders both as `{}`/`[]`, because its encoder
dispatches on `reflect.Kind`. Keying the contract on nil-ness rather than on
rendered output is what makes it hold under either. The predicate parts company
with every encoder only on values no document round-trips (a nil channel is
reported null though `encoding/json` cannot marshal it at all).

It exists for parsers outside this package — handler and trait property readers,
and out-of-tree lowering rules — that must classify a null the same way the
validator does. Use it rather than `value == nil`: a typed nil is a non-nil
interface holding a nil value, so `== nil` is false and a `.(map[string]any)`
assertion on it succeeds with `ok=true` and a nil map, making the key read as an
authored empty collection. Where empty and absent mean different things that
difference is a behaviour change, not a cosmetic one. The standing example is a
`metav1.LabelSelector` in a `NetworkPolicy` peer: an empty one matches everything,
while a nil one applies no constraint on that axis — which is *not* the same as
matching nothing, since what the peer then selects depends on the sibling fields it
still has (`k8s.io/api` `networking/v1/types.go:199-222`).

`IntegerValue` is the matching reader for integers. It returns a property value as
an `int64` when it is a whole number of any Go integer kind, named or not, or a
finite integral float. It refuses a fraction, NaN/±Inf, a non-number, an unsigned
value above `math.MaxInt64`, and a float outside −2^63 ≤ value < 2^63 (−2^63 itself
fits `int64` and reads; 2^63 does not). The builtin handlers
read every integer property through it, and each then checks the result against its
own target (a port is 1–65535, a replica count fits `int32`), refusing a value that
does not fit rather than converting it. So a plain `int32` or `int64` from a lowering
rule or a Go caller reads the same as the `int` the YAML decoder produces, and a
value that would only fit after wrapping, like 2^32+80 for a port, is an error
instead of port 80. An out-of-tree handler should read integers through it for the
same reason. `IntegerInRange(value, lo, hi)` adds the target check and names the true
refusal reason, so an overflow like `1e20` never reads as a type error; a capability
`integer` property is checked with it too.

### What an explicit `null` means on the emitted path

The contract, stated once because "null" has several readers here and aligning
them one at a time is what made this take six rounds:

> A value that serializes to JSON null is absent, at every depth, on every path;
> a null is never a member of any `Items` type, so a null array element is a type
> error; reservation is about the KEY being written.

Applied in two places, because a key and an array element are not the same kind
of thing:

- **A declared, optional object key holding a null is DELETED** before its value
  is checked. `Required` already classifies a null as absent, so materialising it
  as a present key contradicted the classification the file had already made — and
  a handler parser decides presence with a bare two-value map lookup, so it saw a
  key the validator had decided was not there.
- **A null ARRAY ELEMENT is rejected**, by an explicit guard rather than by the
  type check. An element cannot be absent — it is present by being in the list —
  so there is nothing to normalise it to, and dropping it would renumber its
  siblings under a schema that may constrain length and order. The guard is
  necessary rather than decorative: a *typed* nil (`map[string]any(nil)`,
  `[]any(nil)`, reachable from a rule written in Go) satisfies the plain type
  assertion the object/array coercers try first, and iterating the resulting empty
  collection rejects nothing, so the type check alone accepted it. An `Items`
  schema with no declared type checks nothing at all — and so does *no* `Items`
  schema, which is why the guard runs before the `Items` walk rather than inside
  it: declaring no element schema says nothing about the members, but it does not
  license the one member no `Items` type could ever have matched.
- **An `Enum` member holding a null where the value's null would be stripped or
  rejected is refused.** Members are compared against a value that has already been
  normalised, while the declared members are not, so such a member could never
  match anything that reaches the comparison. Refusing it names the schema defect
  instead of leaving an `Enum` that silently never matches.

  The member is walked alongside its schema, because normalisation does not reach
  everywhere: a key an object leaves to `AdditionalProperties: true`, anything inside
  an array element with no `Items` schema, and anything below a schema with no
  declared `Type` pass through as written. A value can hold a null there, so a member
  holding the same null matches it and is accepted — `{opaque: null}` against
  `Enum: [{opaque: null}]` on an open object. A member's null compares equal only to
  a null value, whatever either side's Go type, and never to an empty collection. The
  rule is one-sided: where only the *value* holds a typed nil under such a key and the
  member holds an empty `[]`/`{}`, the two still match, as they always have — an
  undeclared key is outside every normalisation rule, so this does not start
  distinguishing a typed nil from an empty collection there. A member that cannot match
  for another reason — the wrong shape for the schema's type, or a key a closed
  object refuses — is still refused for any null it holds, as before.

  Refused per *member*, not per schema type. Refusing every `Enum` declared on an
  array or object type is simpler to state and was the first shape of this rule,
  but it also refuses the null-free compound enums that match perfectly well —
  and `PropertySchema` is exported, so a handler outside this repo would have seen
  a schema this validator used to accept start failing for a reason that does not
  apply to it. No built-in schema declares an `Enum` on a non-scalar type today,
  but nothing asserts that as a rule and this check does not depend on it. What is
  asserted is the rule above: `TestBuiltinHandlerSchemaEnumMembersHoldNoNull`
  (`pkg/cmd/kurel`) walks every schema that ships for a member holding a null,
  because the runtime arm only fires once a document validates against the property
  carrying the `Enum`.

  That walk is exported as `CheckEnumMembersHoldNoNull(schema PropertySchema) error`,
  so a handler outside this package can assert the same of its own schemas. It
  reports every `Enum` member — on the schema and on every schema nested under its
  `Properties` (in sorted key order) or `Items` — that holds a null anywhere, named
  by schema path and member index, and returns `nil` when none does. It is
  deliberately **stricter** than the runtime arm: it does not read the schema around
  a member, so it also reports the matchable nulls described above (under an
  `AdditionalProperties` key, inside an array element with no `Items`, below a schema
  with no `Type`). A schema it passes is never refused by the runtime's null rule; one
  it fails may still validate. A member nesting past the validator's depth bound
  counts as holding a null, as it does at runtime.

Two things this deliberately does not do:

- **It makes no exception for `PlatformReserved` keys.** Reservation
  (`enforcePlatformReserved`) is a rule about what a user *wrote*. On the authored
  surface the two rules never meet — it runs upstream of any emission validation, and
  `ValidateAuthoredProperties` runs it before its own strip —
  so reservation keeps treating an explicit null as *present* while the strip
  treats one as absent. Exempting reserved keys here would not have preserved the
  authored rule; it would only have handed a reserved null to the type check,
  producing a loud rejection with the wrong reason. The **component** and **trait**
  surfaces keep the same separation: an authored component or trait is checked
  before any rule can rewrite it. One a rule emitted from checked input — whose
  properties are the ones the strip touches — is exempt from reservation; the output
  of a rule whose input was not checked stays authored, and its components and
  traits are checked as they are emitted, before the strip (see the rule-output
  contract in [Lowering](transform-and-lowering.md#lowering)).
- **A key the schema does not declare is untouched**, including inside an object
  that sets `AdditionalProperties`. Nothing describes such a value, so nothing
  here can normalise it, and a null inside an opaque object still reaches the
  handler parser. This is the same horizon as validation itself.

Scope, because this contract is not settled repository-wide: `ValidateAuthoredProperties`
(above) *does* reach `validatePropertyValue` — it calls it directly for every
authored key, with no strip of its own first. That is why
`validatePropertyValue` carries its own top-level null check
(`if isNullValue(value) { return value, nil }`) rather than relying entirely on
`validateObjectProperties`'s strip: on the emitted path the strip runs first and
the check is usually moot, but on the authored path it is what makes an
explicit null under an optional field read as absence instead of a type error.
What the contract does *not* reach is any path that skips both validators
entirely — a component a lowering rule constructs directly in Go, or a
built-in parser's own field read (`pkg/oam/builtin/components`) — which is why
those parsers keep their explicit-null guards regardless. Aligning that
parser-level layer with this contract is a separate, lower-level concern,
tracked as `go-kure/launcher#394`, and is not closed by this.

Two surfaces used to be named here as sitting outside the contract; both are now
fixed, so the note is historical rather than a live caveat:

- **Capability rendering** (`checkCapabilityValueType`, `capability.go`) is still
  outside the contract by the scope sentence — it is a parallel implementation,
  not a call-through to `validatePropertyValue` — but it now agrees with the
  contract's own behaviour: a present null reads as absence, matching the handler
  surface, and the type switch has a `default` arm. See [Capability rendering:
  types and nulls](policy-and-capability.md#capability-rendering-types-and-nulls) for the current, authoritative description. Fixed as
  `go-kure/launcher#431`.
- **The networkpolicy peer parser** used to read a *typed* nil `namespaceSelector`
  differently from an untyped one. Fixed as `go-kure/launcher#430` — both parsers
  now route their presence check through `oam.IsNullValue`'s reflect-based
  classification, so a typed nil reads as omission on every path that reaches it,
  the same as an untyped one.

## Contract metadata

Handlers and lowering rules may implement `ContractDescriber` (`ContractMetadata()
ContractMetadata`) to declare registration metadata about the contract they
implement: `Family`, `Version`, `RequiredCapabilityKeys` (the `ClusterProfile`
capability keys an entity of this contract needs), `Prerequisites` and `Readiness` (see
below), `Deprecated`, and `DeprecationMessage`. It is primarily a discovery/documentation surface — the engine
does not enforce any of these fields (`CapabilityAware.CapabilityRequired` is what the
engine actually enforces). Two fields are read. A lowering rule's `Version` composes
the `"@<version>"` suffix on `Origin.Rule` (see below). `Deprecated` turns every
authored component or trait of that type into one warning through the Transformer's
warning handler (`SetWarningHandler`), in document order: `component "<name>": type
<t> is deprecated[: <DeprecationMessage>]`, or `trait type <t>` for a trait. Both
`Transform` and `TransformWithPolicy` warn; `LowerRaws` does not, because its output
re-enters `Transform` as authored and warns there. The check reads the authored
document before lowering, so a component a rule synthesizes never warns. A
deprecation is never an error and never changes the output. Nothing else here is
read or enforced. Consumers otherwise: schema publication, artifact
provenance in a downstream consumer, and deprecation tooling. Metadata rides the
existing registration mechanism; there is no separate contract registry.

`Transformer.HandlerContracts()` returns a `HandlerContractSet{ Components, Traits,
Policies }` of every registered component, trait and policy handler, and every
component, trait and policy lowering rule, that implements `ContractDescriber` — the
same six registries `HandlerSchemas()` covers (componentHandlers, traitHandlers,
policyHandlers and the three lowering-rule registries), for the identical reason: a
type reachable only through a lowering rule must still publish its metadata.
**Breaking library change** (go-kure/launcher#789): `HandlerContractSet` gained the
`Policies` field, so an unkeyed composite literal of it no longer compiles.

A lowering rule that implements `ContractDescriber` also has its `Version` folded
into the lowering-rule identity recorded on `Origin.Rule` (see [Lowering](transform-and-lowering.md#lowering)), e.g.
`"trait/expose@v1alpha1"`.

Every built-in handler and lowering rule implements `ContractDescriber`
(go-kure/launcher#789). The scheme: `Family` is the type name the built-in is
registered under (`webservice`, `expose`, `placement`), `Version` is
`builtin.ContractVersion` (`v1alpha1`, one value for all of them), and
`RequiredCapabilityKeys` is the type name for a built-in whose
`CapabilityAware.CapabilityRequired` returns `true` (`certificate`, `expose`) and
empty otherwise. No built-in is deprecated. A built-in rule's identity therefore
reads `component/webservice@v1alpha1`, on `Origin.Rule`, on `LoweringStep.Rule` and
in a `LoweringError` chain, where it read `component/webservice`.

`Prerequisites` (go-kure/launcher#1027) lists what must be in place in the target
cluster, or beside it, for the output to work once applied: an operator, a CRD, an
object the output names and does not create. Each `ContractPrerequisite{ Name,
Version, When }` is text for a reader: `Version` is `""` when any version will do,
`When` is the use of the entity that needs it, `""` when every use does. The build
runs offline and checks none of them, and the engine never reads the field; a
consumer can, for example to check a cluster before the first apply. Unlike
`CapabilityAware.CapabilityRequired` it never fails a build. A nil list means none is
declared, not that none is needed: a contract whose output is a CRD's object needs
that CRD and its controller whether it lists them or not. Of the built-ins only the
`postgresql` rule sets it, with the prerequisites of its `backup` and `recovery`
(When `backup or recovery set`): the CloudNativePG operator, the Barman Cloud
plugin, its `objectstores.barmancloud.cnpg.io` CRD, and the bucket and Secrets the
objects name; and, each When its property is set, the ObjectStore
`recovery.source.objectStoreName` names and the owner's Secret `recovery.secret.name`
names. `docs/postgresql-backup-recovery.md` has the same list with the
reasons. cert-manager
is not on it: the plugin's install recommends it for its own TLS, which can be
supplied otherwise. **Breaking library change**: `ContractMetadata` gained the
field, so an unkeyed composite literal of it no longer compiles.

`Readiness` (go-kure/launcher#1134) lists, for each kind of resource an entity of
this contract provides, how a reader judges that the resource is ready. Each
`ContractReadiness{ Group, Kind, NamePattern, Condition, Field, Value, Generic }` names
the kind (`Group` is `""` for the core group), says in text how the resource is named
(`NamePattern`, for example `<component>-pooler`), and gives its signal: either
`Condition`, a status condition type that reads `True` when the resource is ready, or
`Field` with the `Value` it holds when ready, never both. `Generic` says whether a
generic health check, one that reads a `Ready` condition and applies no other rule,
can judge the kind: `GenericJudges` (`"judges"`, valid only with `Condition: "Ready"`),
`GenericCannotJudge` (`"cannot-judge"`, the kind needs a resource-specific rule; both
signal fields may then be empty when the entity gives none), or `GenericUndeclared`
(`""`, the entity does not say). An entity that provides several kinds lists one
entry per kind, in a stable order. The declaration is made by the entity, read by a
consumer and enforced by nothing: the engine never reads the field, no registration
or build step checks it, and nothing checks that the named condition or field exists
on the real resource. A nil list means none is declared, not that the resources are
ready at once or need no waiting; an empty non-nil list reads the same as nil. Only
`postgresql` declares readiness (go-kure/launcher#1144), one entry for each kind it
lowers into; `pkg/oam/builtin/components/README.md` lists them. **Breaking library change**: `ContractMetadata`
gained the field, so an unkeyed composite literal of it no longer compiles.

### Lowering targets and `Seal`

A lowering rule emits elements other handlers render, so a registry that holds the
rule without them fails only when a document reaches the gap. A rule that
implements `LoweringTargetDeclarer` declares, in a `LoweringTargets{ ComponentTypes,
TraitTypes, PolicyTypes }`, every type it can emit over all of its inputs: the
components it emits, the traits it emits or attaches to a component it emits, and
the policies it emits. A type it only forwards from its input is not a target, and
document kinds are not listed.

`Transformer.Seal()` checks every registered rule that declares targets, of any
kind (document, raw document, component, trait, policy): each declared type must be
registered at its position, as a handler or as another lowering rule. The check
cannot run at registration, because a rule and the handlers of its targets are
registered in any order. `Transform` and `TransformWithPolicy` call `Seal` first, so
an incomplete registry is refused whether or not the document uses the rule;
`LowerRaws` does not, because it dispatches no handler. A caller that has finished
registering may call `Seal` itself to learn of a gap before it has a document. The
error names each rule and the type it is missing, sorted:

```text
registry incomplete: lowering rule component/webservice@v1alpha1 lowers into component type "service", which is not registered
```

`Seal` stores nothing and locks nothing: it reads the registry as it stands, may be
called any number of times, and accepts on the next call a type registered after a
refusal. A rule that does not implement `LoweringTargetDeclarer` is not checked, and
the engine does not check that a rule emits only what it declares.
**Breaking library change** (go-kure/launcher#789): a consumer that registers a
built-in lowering rule without the types it lowers into now fails at `Seal` or at
the first transform, where it failed only for a document that reached the missing
type. The built-in rules declare:

| Rule | Components | Traits | Policies |
|------|------------|--------|----------|
| `webservice` | `deployment`, `service`, `serviceaccount` | `topology-spread`, `pvc` | |
| `worker` | `deployment`, `serviceaccount` | `topology-spread`, `pvc` | |
| `helm` | `helmrelease`, `helmtemplate`, `helmrepository`, `ocirepository`, `gitrepository`, `bucket` | `configmap`, `secret` | |
| `postgresql` | `cnpg-cluster`, `cnpg-objectstore`, `cnpg-pooler`, `cnpg-database`, `cnpg-scheduledbackup` | | `dependency`, `placement` |
| `expose` (trait) | | `ingress`, `httproute` | |

### The "no handler" error

A type with no registered handler fails the transform, and the message names where
the element is: `no handler for component type "x" (component "web")`, `no handler
for trait type "x" (on component "web")`, `no handler for policy type "x" (policy
"first")`. For an element a lowering rule emitted, it also names the rule and the
authored component it lowered:

```text
no handler for component type "service" (component "web", emitted by lowering rule component/role for component "web" (type "role") in document "app" (kind "Application"))
```

A trait the author wrote, forwarded by a rule onto a component the rule emitted under
another name, names that component and, through it, the rule and the authored
component: `no handler for trait type "x" (on component "web-rendered", itself emitted
by lowering rule component/role for component "web" (type "role") in document "app"
(kind "Application"))`. One a document rule moved to another component names the
component it was written on: `(on component "worker", authored on component "web" (type
"webservice") in document "app" (kind "Moving"))`. On the component the author wrote it
on, by name, the clause is left out.

The emitted form is reached only for a type the package knows and the registry holds no
handler for, and only for a rule that does not declare the type as a target (`Seal`
refuses a declared one first). A type the package does not know is refused earlier,
when the lowered document is validated.

