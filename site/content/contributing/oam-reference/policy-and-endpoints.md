---
title: "Policy defaults, endpoints and outputs"
weight: 3
---

Implementation, history and tests of the sections on the reference page
[Policy defaults, endpoints and outputs](../../api-reference/oam-components/policy-and-endpoints.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Policy defaults & enforcement ordering

- **Implementation:**
  - `enforce.go`'s `applyDefaultQuantity`
    ["fills any request/limit the author left unset", in the reference's tier 2.]
  - `oam.PolicyDefaultReporter`: the workload kinds, `cnpg-cluster`, `persistentvolumeclaim` and the `pvc` trait
    embed an `oam.PolicyDefaultLog`; the `scaler` trait implements the interface
    itself, so that its config stays comparable
    ["is reported to the provenance report as a `policy` entry", in the reference's tier 2.]
  - `oam.BuiltinDefaultReporter`, embedded `oam.BuiltinDefaultLog` in
    `cnpg-cluster`
    ["is reported as a `builtin` entry", in the reference's tier 2.]
  - `common.go`
    ["`buildResourceRequirements`", in the reference's tier 3.]
  - `capPolicyRequestDefaults`, `resource_policy_defaults.go`
    ["held to the authored limit for that resource", in the reference's tier 3.]
  - `enforceMaxResources` (`enforce.go`) closes this by calling the same
    `buildResourceRequirements` the generator calls, enforcing against its
    result, and discarding it — `buildResourceRequirements` deep-copies its
    maps, so this is read-only and generated output is unchanged.
    ["this" is the gap described in the History entry below it.]
  - `cnpgResourceList`
    ["copies every entry of `c.Resources` straight onto the Cluster spec", in the reference's sentence on `postgresql`.]
  - `common.go`'s `buildResourceRequirements` call sites at the init-container
    and sidecar builders
    ["receive the same intrinsic defaults", in the reference's paragraph on init containers and sidecars.]
  - `enforce.go`
    ["the shared `enforceExtraContainer` helper", in the same paragraph.]
  - `common.go`
    ["a dedicated `JobSpecConfig`/`parseJobSpec`/`applyJobSpec`", in the reference's paragraph on the twelve JobSpec-level properties.]
  - `applyJobSpec` copies every value it writes rather than assigning the config's
    own pointers (`copyPtr` for the scalars, `DeepCopy` for `successPolicy` and
    `podFailurePolicy`, which each own a slice of rules). A handler config outlives one `Generate` call and
    `Generate` may run more than once, so an aliased pointer would leave a mutable
    path from the generated object back into the config — a writer through the
    object would change what the next `Generate` emits.
- **History:**
  - go-kure/launcher#1181
    ["an authored opt-out", in the reference's tier 1.]
  - go-kure/launcher#1217; the apiserver refuses a request above its limit
    ["renders a 64Mi request, not 128Mi", in the reference's tier 3.]
  - Prior to go-kure/launcher#251
    the enforcement check ran before the intrinsic tier was computed, so an
    application that omitted `spec.resources` entirely could ship a Deployment
    whose 100m CPU / 128Mi memory intrinsic defaults exceeded the enforced
    maximum.
    ["the enforcement check" is the check of `oam.Policy.MaxCPU()`/`MaxMemory()` described in the reference's paragraph on the effective value.]
  - go-kure/launcher#312
    ["`ApplyPolicy` enforces them too", in the reference's paragraph on init containers and sidecars.]
  - go-kure/launcher#344
    ["`job` onto its own `spec`", in the reference's paragraph on the twelve JobSpec-level properties.]
- **Tests:**
  - Both halves are pinned: `TestSchemaJobSpec_HasNoSuspendKey` on the schema,
    `TestCronjobHandler_SuspendWritesCronJobSpecOnly` and
    `TestJobHandler_SuspendWritesJobSpec` on the generated objects.
    ["Both halves" are the two halves described in the reference's paragraph "One JobSpec field is deliberately not shared: `suspend`".]

## Policy refusal classes

- **Implementation:**
  - `enforce.go`,
    `enforcePodTemplatePolicy`, `enforceRenderedObjectPolicy`
    ["one check raises it", in the reference's first bullet.]
- **History:**
  - go-kure/launcher#849
    ["instead of matching text", in the reference's first paragraph.]

## Pod view of a monitoring workload object (`MonitoringPodSpec`)

- **History:**
  - go-kure/launcher#974
    ["finds none to scan", in the reference's first paragraph.]
- **Tests:**
  - Each monitoring workload kind adds its case from its own `monitoringWorkload` mapping;
    `TestMonitoringPodSpec_CoversEveryRegisteredWorkloadKind` (`pkg/oam/builtin/registry`) fails on a
    registered kind of the operator's API whose spec lists containers and that has no case.
    ["its own `monitoringWorkload` mapping" is also "the same mapping" in the reference's bullet "What it holds".]

## Declared endpoints of a chart

- **Implementation:**
  - `chartEndpoints`
    ["One shared schema and parser", in the reference's first paragraph.]
- **History:**
  - go-kure/launcher#1132
    ["implement `oam.EndpointProvider` from an `endpoints` property", in the reference's first paragraph.]
- **Tests:**
  - the schema-parity test names
    `endpoints` as its one exception
    ["`helmrelease` decodes the rest as its `HelmReleaseSpec`", in the reference's bullet "Not a chart value".]

## Declared outputs

- **History:**
  - go-kure/launcher#1259
    ["implement `oam.OutputProvider`", in the reference's first paragraph.]
