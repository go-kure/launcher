---
title: "Upstream fields a hand-parsed kind does not read"
weight: 14
---

Implementation, history and tests of the sections on the reference page
[Upstream fields a hand-parsed kind does not read](../../api-reference/oam-components/upstream-fields-not-read.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Upstream fields a hand-parsed kind does not read

- **Implementation:**
  - `ParseSecretProperties`
    [named after "the `secret` kind's parser" in the reference's sentence on `webservice`, `worker` and the traits.]
- **History:**
  - go-kure/launcher#790
    [named after "The rule that closes that gap", the words before the reference's quotation.]

## Below the top level

- **History:**
  - go-kure/launcher#790
    [named after `NestedUnsupportedFieldHint` in the reference's sentence on a document.]

## One level down

- **History:**
  - go-kure/launcher#790
    [the reference's first sentence of this section names it as its subject, so that sentence stays on the reference page whole.]

## An explicit null on a refused key

- **History:**
  - go-kure/launcher#790
    [the reference's sentence "This is each kind's behavior from before …, left as it was" names it, and stays on the reference page whole.]

## What holds this

- **Tests:**
  - `TestHandParsedKinds_CoverEveryUpstreamField` walks the upstream type of
    each of the ten kinds, from the object down every level whose fields are
    the component's properties, and fails on a field that is neither read, nor
    refused, nor placed in one of the classes above — so a field a later
    `k8s.io/api` adds cannot be dropped in silence. It stops at the properties:
    what a property holds inside is held to the upstream type only for a
    container's `resources` and the pod's `podResources`.
    ["the classes above" are the reference's sections "Refused, with the reason", "Read in another shape", "One level down", "Not authorable, and not refusable by name" and "Outside the properties".]
  - `TestRefusedKeys_BothPathsGiveTheReason` holds every entry of every refusal
    map to the two texts above; `TestRefusedKeys_OneLevelDown` holds the two
    refusals inside a declared property.
    ["the two texts above" are the two texts under the reference's "The two paths".]
  - `TestHandParsedKinds_MatchExpressionsAtEverySelector` runs a match
    expression through each of the 47 label selectors these kinds read, to the
    generated object, and five defective ones to their refusal.
  - `TestHandParsedKinds_TemplateMetadataAndContainerName` and
    `TestHandParsedKinds_ServicePortFieldsReachTheObject` pin the template's
    metadata, the main container's name and the six fields of a Service port.
