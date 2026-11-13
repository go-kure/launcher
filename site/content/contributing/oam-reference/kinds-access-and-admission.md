---
title: "Kinds: access and admission"
weight: 4
---

Implementation, history and tests of the entries on the reference page
[Kinds: access and admission](../../api-reference/oam-components/kinds-access-and-admission.md).
Text in square brackets is added here to say what a moved sentence refers to.

## apiservice, validatingwebhookconfiguration, mutatingwebhookconfiguration

- **History:**
  - go-kure/launcher#943 (the kind-named projections of `apiservice` and the two webhook
    configurations).

## validatingadmissionpolicy, validatingadmissionpolicybinding, mutatingadmissionpolicy, mutatingadmissionpolicybinding

- **History:**
  - go-kure/launcher#943 (the kind-named projections of the four admission-policy kinds).

## role, rolebinding, clusterrole, clusterrolebinding

- **History:**
  - go-kure/launcher#790 (the kind-named projections of the four RBAC kinds).
  - go-kure/launcher#922 (`oam.ObjectKindPolicy` restricts these kinds).
- **Tests:**
  - `TestBuiltinMarkerKinds_RequiredMatchMarkers` derives each kind's list from the markers
    and fails on a dependency bump that changes it. [the list: the fields this entry calls
    required from the markers]
  - the same test fails if one appears [the same test:
    `TestBuiltinMarkerKinds_RequiredMatchMarkers`; one: a field the API marks required that
    the type leaves out when unauthored]
  - `TestKindComponents_OmittedRequiredAndWrittenDefaults` holds the four in its table as
    well, from the same markers: to that, and to no default marked on a field the type writes
    unauthored. [the four: `role`, `rolebinding`, `clusterrole` and `clusterrolebinding`;
    that: the statement on the reference page that the API marks no field of the four required
    that the type leaves out when unauthored]
  - These checks are of presence [the reference sentence now reads "The checks on required
    fields are of presence"; these checks: the required-field validation of the four kinds,
    `refuseUnauthoredRequired`, called by `policyFreeKind.config`, which the two tests above
    hold to the markers]

