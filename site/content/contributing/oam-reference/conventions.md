---
title: "Conventions"
weight: 17
---

Implementation, history and tests of the sections on the reference page
[Conventions](../../api-reference/oam-components/conventions.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Conventions

- **History:**
  - go-kure/launcher#396
    ["the maps themselves", at the end of the reference's sentence on the `postgresql` Cluster's `spec.inheritedMetadata`.]

## The `app` label

- **History:**
  - go-kure/launcher#572
    [named after "a 10-hex-character sha256 digest" in the reference's second paragraph.]
  - go-kure/launcher#788
    [named after "where the key is absent" in the reference's paragraph on the component label.]
  - go-kure/launcher#790
    [named after "refused when it is generated" in the same paragraph.]

## The object name (`objectName`)

- **Implementation:**
  - `ComponentObject`, in `object_name.go` or, for
    a kind added since, in the kind's own file
    ["every type whose handler declares its object", in the paragraph "The property is not in any handler's `PropertySchema`".]
- **History:**
  - go-kure/launcher#787
    [named after "in place of the component name" in the reference's first paragraph.]

## The object's labels and annotations (`labels`, `annotations`)

- **History:**
  - go-kure/launcher#790
    [named after "each a map of strings" in the reference's first paragraph.]
- **Tests:**
  - `TestObjectMetadata_EveryKindComponentTakesIt` holds every registered kind
    to the two properties, so a kind added later cannot ship without them.
    ["the two properties" are `labels` and `annotations`.]

## The pod template's labels and annotations (`podMetadata`)

- **History:**
  - go-kure/launcher#1178
    [named after "own metadata" in the reference's first paragraph.]
  - go-kure/launcher#1216
    [named after "`webservice` and `worker` take it too" in the same paragraph.]

## A label selector's match expressions

- **History:**
  - go-kure/launcher#790
    [named after "each named by its path" in the reference's first paragraph.]
- **Tests:**
  - `TestValidateLabelSelector_MatchesAPIMachinery` holds launcher's check to the
    linked function on those three.
    ["the linked function" is `ValidateLabelSelectorRequirement` of `k8s.io/apimachinery`, named in the reference's paragraph before; "those three" are the three defects the reference lists.]
  - `TestExternalSecretsKinds_RequiredMatchSource` derives the two from
    that source by that rule, beside the fields the operator's own types mark required, and holds
    each kind's list to them.
    ["the two" are `key` and `operator`; "that source" and "that rule" are the linked `metav1.LabelSelectorRequirement` and the schema generators' rule, both in the reference's paragraph "The Prometheus operator's module, …".]

## Required fields a kind writes unauthored and does not refuse

- **Implementation:**
  - `scripts/vendor-prometheus-operator-gates.sh`
    [after `testdata/upstream/prometheus-operator/` in the reference's first vendored-CRD bullet.]
  - `scripts/forbidden-terms-lib.sh`, self-tested by `make
    test-forbidden-terms`
    [after "the script reads the guard's own matcher" in the second bullet.]
- **History:**
  - go-kure/launcher#1108
    [named after `mise run vendor-external-secrets-crds vX.Y.Z` in the third bullet.]
  - Before go-kure/launcher#883 generated their lists, the same was shown of `imageCatalogRef` on
    `cnpg-cluster` and `pgbouncer.imageCatalogRef` on `cnpg-pooler`: a `kind` written `""` refused
    by the CRD's own rule on the reference, a `name` written `""` and a `major` written `0` accepted.
    All three are refused now.
    ["the same" is what the reference's paragraph "What the API server makes of them" shows of the Pooler and the Prometheus operator kinds.]
  - go-kure/launcher#1108
    [named after "on their vendored CRDs" in the reference's paragraph "At this pin the file holds 25 members …".]
- **Tests:**
  - `TestRequiredWrittenKinds_CoverEveryCRDKind` holds the measured kinds to the kind inventory:
    every `kind` row whose API group client-go's scheme does not register, so a CRD serves it, is
    measured or named as not measured with its reason, so a new kind component of such an API
    fails there until it is one or the other.
    ["there" is that test; it follows the reference's sentence on `apiservice`.]

## Every spec field is this package's to write

- **Implementation:**
  - `common.go`
    ["`ValidateImageRef`", in the reference's paragraph "Two injected defaults are gone from the emitted manifests".]
  - The `obj.Annotations = nil`
    assignments scattered through the handlers, which existed to strip the `app:`
    annotation the constructors used to stamp, are now no-ops — kept so the field
    stays at a known value regardless of what a future constructor does.
    ["the constructors" are the `Create<Kind>` constructors of the reference's first paragraph.]
- **History:**
  - its
    plugin name and parameters were corrected afterwards by go-kure/launcher#643
    ["the barman-cloud WAL-archiver plugin entry", in the reference's paragraph "`postgresql` writes every CNPG value itself".]
