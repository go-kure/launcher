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
  - `TestMetalLBKinds_RequiredMatchCRD` holds "Required: nothing" to the linked
    module's `v1beta1` CRD.
  - `TestMetalLBKinds_ExpressionRules` fails on an expression rule that is
    added; the CRD declares none.

## metallb-bgpadvertisement

- **Implementation:** built on `policyFreeKind` as the two kinds above are, and
  emits that one object.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#883 (the Required list is generated from the linked module's
  `v1beta1` CRD); go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_NoDefaultIsLost` holds the defaults of the linked CRD to
    the two aggregation lengths being pointers.
  - `TestMetalLBKinds_RequiredMatchCRD` holds the Required list to the linked
    module's `v1beta1` CRD.
  - `TestMetalLBKinds_ExpressionRules` holds the kind's answer to the API
    server's for the linked CRD, on properties that break the rule and on
    properties that keep it, and fails on a rule that is added or reworded.

## metallb-bgppeer

- **Implementation:** built on `policyHeldKind`, the helper of the kinds above
  with one check under the environment policy, and emits that one object. It is
  built at `metallb.io/v1beta2` because that is the version the linked module's
  CRD stores, `v1beta1` is served and marked deprecated there, and the base
  library's constructor builds the peer at `v1beta2`.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#883 (the Required list is generated from the linked module's
  `v1beta2` CRD); go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_EmitTheServedVersion` holds each kind to the version its
    CRD serves and stores.
  - `TestMetalLBKinds_NoDefaultIsLost` holds the kind's list of fields with a
    default the type cannot carry to the linked CRD.
  - `TestMetalLBKinds_RequiredMatchCRD` holds the Required list to the linked
    module's `v1beta2` CRD.
  - `TestMetalLBKinds_ExpressionRules` holds the kind's answer to the API
    server's for the linked CRD, on properties that break each rule and on
    properties that keep it, and fails on a rule that is added or reworded.
  - `TestMetalLBBGPPeerKind_SessionPassword` (`metallb_bgppeer_test.go`) holds
    the `password` behaviour under the environment policy.

## metallb-community

- **Implementation:** built on `policyFreeKind` as the kinds above are, and
  emits that one object.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_NoDefaultIsLost` fails on a default a dependency bump
    adds to a field that could not carry an authored empty value.
  - `TestMetalLBKinds_RequiredMatchCRD` holds "Required: nothing" to the linked
    module's `v1beta1` CRD.
  - `TestMetalLBKinds_ExpressionRules` fails on an expression rule that is
    added; the CRD declares none.

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
    the reference's account of `autoAssign` and `avoidBuggyIPs`: a default
    that is not the field's empty value, on a field that is no pointer, fails
    there.
  - `TestMetalLBKinds_RequiredMatchCRD` holds the Required list to the linked
    module's `v1beta1` CRD. A dependency bump that adds, drops or moves a
    required field fails until the list is regenerated.
  - `TestMetalLBKinds_ExpressionRules` fails on an expression rule that is
    added; the CRD declares none.

## metallb-l2advertisement

- **Implementation:** built on `policyFreeKind` as `metallb-ipaddresspool` is,
  and emits that one object.
- **History:** go-kure/launcher#790 (the kind-named projection);
  go-kure/launcher#883 (the Required list is generated from the linked module's
  `v1beta1` CRD); go-kure/launcher#922 (the object kind policy).
- **Tests:**
  - `TestMetalLBKinds_RequiredMatchCRD` holds the Required list to the linked
    module's `v1beta1` CRD.
  - `TestMetalLBKinds_ExpressionRules` fails on an expression rule that is
    added; the CRD declares none.
