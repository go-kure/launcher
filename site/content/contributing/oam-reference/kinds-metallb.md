---
title: "Kinds: MetalLB"
weight: 10
---

Implementation, history and tests of the entries on the reference page
[Kinds: MetalLB](../../api-reference/oam-components/kinds-metallb.md). Every test
named here is in `pkg/oam/builtin/components/metallb_kinds_internal_test.go`
unless a line says another file.

## metallb-bfdprofile

- **Implementation:** built on `policyFreeKind` as the kinds above are, and
  emits that one object.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`).
- **Tests:**
  - `TestMetalLBKinds_NoDefaultIsLost` fails on a default a dependency bump
    adds to a field that could not carry an authored empty value.
  - `TestMetalLBKinds_RequiredMatchCRD` holds that to the linked module's
    `v1beta1` CRD ("that" is the reference's "Required: nothing").
  - The CRD declares none; `TestMetalLBKinds_ExpressionRules` fails on one that
    is added ("none" is the CRD's expression rules).

## metallb-bgpadvertisement

- **Implementation:** built on `policyFreeKind` as the two kinds above are, and
  emits that one object.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#883 (the Required list is generated from the linked module's
  `v1beta1` CRD); go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_NoDefaultIsLost` holds the defaults of the linked CRD to
    that ("that" is the reference's account of the two aggregation lengths:
    pointers, whose API defaults are 32 and 128, one route per address).
  - `TestMetalLBKinds_RequiredMatchCRD` holds it to that CRD as well ("it" is
    the Required list, generated from the linked module's `v1beta1` CRD).
  - `TestMetalLBKinds_ExpressionRules` holds the kind's answer to the API
    server's for the linked CRD, on properties that break the rule and on
    properties that keep it, and fails on a rule that is added or reworded.

## metallb-bgppeer

- **Implementation:** built on `policyHeldKind`, the helper of the kinds above
  with one check under the environment policy, and emits that one object. The
  version it is built at, and why, are in the reference.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#883 (the Required list is generated from the linked module's
  `v1beta2` CRD); go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_EmitTheServedVersion` holds each kind to the version its
    CRD serves and stores.
  - `TestMetalLBKinds_NoDefaultIsLost` holds the kind's list of such fields to
    the linked CRD ("such fields" are the reference's fields with a default
    the type cannot carry, as `peerPort`).
  - `TestMetalLBKinds_RequiredMatchCRD` holds it to that CRD as well ("it" is
    the Required list, generated from the linked module's `v1beta2` CRD).
  - `TestMetalLBKinds_ExpressionRules` holds the kind's answer to the API
    server's for the linked CRD, on properties that break each rule and on
    properties that keep it, and fails on a rule that is added or reworded.
  - `TestMetalLBBGPPeerKind_SessionPassword` (`metallb_bgppeer_test.go`) holds
    this ("this" is the reference's account of `password` under each policy).

## metallb-community

- **Implementation:** built on `policyFreeKind` as the kinds above are, and
  emits that one object.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_NoDefaultIsLost` fails on a default a dependency bump
    adds to a field that could not carry an authored empty value.
  - `TestMetalLBKinds_RequiredMatchCRD` holds that to the linked module's
    `v1beta1` CRD ("that" is the reference's "Required: nothing").
  - The CRD declares none; `TestMetalLBKinds_ExpressionRules` fails on one that
    is added ("none" is the CRD's expression rules).

## metallb-ipaddresspool

- **Implementation:** built on `policyFreeKind`, and emits that one object. The
  type name carries the `metallb-` prefix as the kinds of Cilium's API carry
  theirs: both APIs have a pool of addresses and an advertisement for BGP.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#883 (the Required list is generated from the fields the
  linked module's `v1beta1` CRD requires and the type writes unauthored);
  go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_NoDefaultIsLost` holds the defaults of the linked CRD to
    that: one that is not the field's empty value, on a field that is no
    pointer, fails there ("that" is the reference's account of `autoAssign`
    and `avoidBuggyIPs`).
  - So a dependency bump that adds, drops or moves one fails until it is
    regenerated; `TestMetalLBKinds_RequiredMatchCRD` holds it to that CRD as
    well ("one" and "it" are the fields of the Required list, generated from
    the fields the linked module's `v1beta1` CRD requires and the type writes
    unauthored).
  - The CRD declares none; `TestMetalLBKinds_ExpressionRules` fails on one that
    is added ("none" is the CRD's expression rules).

## metallb-l2advertisement

- **Implementation:** built on `policyFreeKind` as `metallb-ipaddresspool` is,
  and emits that one object.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#883 (the Required list is generated from the linked module's
  `v1beta1` CRD); go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_RequiredMatchCRD` holds it to that CRD as well ("it" is
    the Required list, generated from the linked module's `v1beta1` CRD).
  - The CRD declares none; `TestMetalLBKinds_ExpressionRules` fails on one that
    is added ("none" is the CRD's expression rules).
