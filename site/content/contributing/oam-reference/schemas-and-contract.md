---
title: "Property schemas and contract metadata"
weight: 25
---

Implementation, history and tests of the sections on the reference page
[Property schemas and contract metadata](../../api-reference/oam/schemas-and-contract.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Property schemas

- **History:**
  - go-kure/launcher#421 (a kurel parameter's `Type` is `string`, `integer`, `boolean`, `array` or `object`).
  - go-kure/launcher#383 (`Types` is the union idiom: a leaf that accepts more than one scalar type).
  - go-kure/launcher#790 (`Exclusive` is the one-of idiom).
  - go-kure/launcher#635 (a reservation on an inner field of a declared array of objects is enforced wherever it is declared, not only at the top level; a reserved key authored as `config: {locked: null}` is not gone before `Transform` can refuse it).
  - go-kure/launcher#1231 (`spec.featureGates` overrides a gate's state in both directions).
  - go-kure/launcher#1183 (a `Gate` no row names, or a row no schema uses, fails the schema tests).
  - go-kure/launcher#1245 (`gatedPaths` is read by the gate check alone, so property validation and what a document is accepted as do not change).
  - go-kure/launcher#1320 (a struct that does not encode as JSON is refused, with its path, where a gate below it is off at the target).
  - go-kure/launcher#1450 (a pointer type that is its own element type, `type P *P`, is read through its JSON form like any other).
  - go-kure/launcher#1230 (like `Gate`, the restart-policy gate key is rejected at the two flat call sites, so it is absent from their allow-sets).
  - go-kure/launcher#1330 (like `Gate`, the key is rejected at the two flat call sites).
  - go-kure/launcher#408 (`ValidateAuthoredProperties` is the authored-path counterpart of the emitted-path check, and closes it).
  - go-kure/launcher#1001 (`postgresql` points a key its `backup` replaced, `objectStore` and `objectStoreObjectName`, to where it went; and names the reason `backup.objectStore` refuses `serverName`, `azureCredentials` and `googleCredentials`).
  - The hint was asked of a terminal component
    handler alone before go-kure/launcher#790; the three other positions are new. [The hint is `UnsupportedFieldHint`.]
  - Before go-kure/launcher#790 a nested refusal carried no hint.
  - go-kure/launcher#1466 (every refusal the property validators make is a `*PropertyError`).
  - go-kure/launcher#1469 (the platform-reservation check is one of those refusals).
  - go-kure/launcher#325 (the shape check is the only place an authored scalar's type is enforced for every property).
  - go-kure/launcher#1282 (the `app` argument is never edited, for a plain type name and for a declared type identity alike).
  - go-kure/launcher#1463 (top-level `Required` is enforced as written, and a null counts as absent).
  - go-kure/launcher#765 (nested `Required`, inside an object the author did write, is enforced).
  - go-kure/launcher#750 (for a trait a binding matches, the check reads the trait's properties as the rendering merges into them, recursively, so a required key the rendering supplies is not refused).
- **Tests:**
  - A completeness test (`pkg/cmd/kurel`) enforces that every built-in
    schema node declares exactly one of `Type` and `Types`.
  - A completeness test (`pkg/cmd/kurel`) enforces that no built-in
    schema node is left without a description.

### What an explicit `null` means on the emitted path

- **History:**
  - go-kure/launcher#394 (aligning the parser-level layer with this contract, a separate, lower-level concern, is not closed by this).
  - go-kure/launcher#431 (capability rendering, `checkCapabilityValueType`, now agrees with the contract's own behaviour).
  - go-kure/launcher#430 (the networkpolicy peer parser read a typed nil `namespaceSelector` differently from an untyped one; both parsers now route their presence check through `oam.IsNullValue`'s reflect-based classification).
- **Tests:**
  - What is
    asserted is the rule above: `TestBuiltinHandlerSchemaEnumMembersHoldNoNull`
    (`pkg/cmd/kurel`) walks every schema that ships for a member holding a null,
    because the runtime arm only fires once a document validates against the property
    carrying the `Enum`. [The rule is that no `Enum` member holds a null.]

## Contract metadata

- **History:**
  - go-kure/launcher#789 (`HandlerContractSet` gained the `Policies` field, a breaking library change; every built-in handler and lowering rule implements `ContractDescriber`).
  - go-kure/launcher#1027 (`Prerequisites` lists what must be in place in the target cluster, or beside it, for the output to work once applied).
  - go-kure/launcher#1134 (`Readiness` lists, for each kind of resource an entity of this contract provides, how a reader judges that the resource is ready).
  - go-kure/launcher#1144 (`postgresql` declares readiness, one entry for each kind it lowers into).

### Lowering targets and `Seal`

- **History:**
  - go-kure/launcher#789 (a consumer that registers a built-in lowering rule without the types it lowers into now fails at `Seal`, a breaking library change).
