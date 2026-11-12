---
title: "Kinds: secrets, certificates and backup"
weight: 12
---

Implementation, history and tests of the entries on the reference page
[Kinds: secrets, certificates and backup](../../api-reference/oam-components/kinds-secrets-certs-backup.md). Every test
named here is in `pkg/oam/builtin/components/`, in the file the note beside it names.

## issuer, clusterissuer, certificate

- **Implementation:** Each is built on `policyHeldKind` (`policyFreeKind` with a
  policy check, see the **storageclass** entry) ("Each" is `issuer`,
  `clusterissuer` and `certificate`).
- **History:** go-kure/launcher#790 (the kind-named projections);
  go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`).
- **Tests:**
  - `TestCertManagerKinds_NoDefaultedZeros` holds the types to having no number,
    boolean or string that is omitted when zero and that the CRD defaults to
    something else. ("the types" are the reference's spec types, `IssuerSpec` and
    `CertificateSpec`, and the test is in `certmanager_kinds_internal_test.go`.)
  - `TestKindComponents_OmittedRequiredAndWrittenDefaults` derives the two from
    the CRD and shows the refusals. ("the two" are the `cron` and the
    `windowDuration` of a renewal window, and "the refusals" are the refusals of
    either when it is left out, and the test is in `kind_api_sets_internal_test.go`.)
  - `TestKindComponents_NullRequired` shows each refusal by running the CRD's
    schema validator on the object the type would encode. ("each refusal" is the
    refusal of an unauthored `nodeSelectorTerms` and of an unauthored `name` of a
    parent reference, in an ACME HTTP01 solver, and the test is in
    `kind_null_required_internal_test.go`.)
  - A dependency bump that adds, drops or moves one fails
    `TestKindComponents_RequiredIsGenerated` until the list is regenerated.
    ("one" is a field the reference's generated lists name, and "the list" is
    that list, and the test is in `kind_required_internal_test.go`.)
  - `TestKindComponents_GeneratedRequiredBothWays` builds each field authored and
    refuses its omission, and runs one per type through the CRD's own validation,
    which refuses it too. ("each field" is a field the reference's generated
    lists name, and the test is in `kind_required_internal_test.go`.)
  - `TestLabelSelectorKinds_CoverEverySelector` shows, at each of the 16 label
    selectors, what the API server answers for an expression without its `key` or
    its `operator`. (The test is in `label_selector_internal_test.go`.)

## secretstore, clustersecretstore, externalsecret, clusterexternalsecret

- **Implementation:** All four are built on `policyHeldKind`: the stores for the
  credentials a provider takes as a value, the two external-secret kinds for the
  one field that has the operator write another kind than a Secret
  (`target.manifest`, under **Policy** below). ("All four" are `secretstore`,
  `clustersecretstore`, `externalsecret` and `clusterexternalsecret`, and
  "Policy" is the Policy paragraph in the reference's Validation section.)
- **History:** go-kure/launcher#790 (the kind-named projections; the published
  schema marks `provider` of a `secretstore` and of a `clustersecretstore`, and
  `externalSecretSpec` of a `clusterexternalsecret`, as required);
  go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`);
  go-kure/launcher#1108 (the operator's CRDs vendored for the tests).
- **Tests:**
  - `TestExternalSecretsKinds_DefaultedZeros` holds that list to the default
    markers of the linked module's source in both directions: no other number or
    boolean of the four specs is of that shape. ("that list" is the reference's
    list of the three store fields whose authored zero or `false` the API would
    replace, and "that shape" is a zero the type omits and the API defaults to
    another value, and the test is in `externalsecrets_kinds_internal_test.go`.)
  - `TestKindComponents_RequiredIsGenerated` fails where the generated file and
    that derivation differ, in either direction and for either list, and names
    the path; a dependency bump that adds, drops or moves one fails there.
    ("the generated file" is `zz_generated_kind_required.go`, "that derivation" is
    the reference's derivation of the lists from the linked module's source,
    "either list" is the generated required list and the list of four store
    fields the type always writes, and "one" is a path, and the test is in
    `kind_required_internal_test.go`.)
  - Regenerate with `UPDATE_KIND_REQUIRED=1 go test ./pkg/oam/builtin/components/
    -run TestKindComponents_RequiredIsGenerated`. ("Regenerate" is regenerating
    the generated file the previous bullet names.)
  - `TestExternalSecretsKinds_RequiredMatchSource` shows that no field of the four
    specs is required and omitted by the type when unauthored at this pin, and
    fails on the first one a bump brings. ("the first one" is the first such
    field, and the test is in `externalsecrets_kinds_internal_test.go`.)
  - The API server holds the lists too: `TestKindComponents_GeneratedRequiredBothWays`
    runs one path of every type holding a generated path through its validator on
    the operator's CRDs, vendored for the tests from the release the linked commit
    is tagged (go-kure/launcher#1108; see "Vendored CRDs" under the required
    fields), and every generated path is one those CRDs declare. ("the lists" are
    the reference's required lists, and the test is in `kind_required_internal_test.go`.)
  - Every rule the source declares on a type these specs reach is classified in
    `TestExternalSecretsKinds_Rules`, which fails on one that is added or
    reworded. ("the source" is the linked module's source, "these specs" are the
    four specs, and "one" is a rule, and the test is in
    `externalsecrets_kinds_internal_test.go`.)
  - `TestExternalSecretsKinds_WrittenUnauthored` pins those five. ("those five"
    are the five empty blocks the reference names, an external secret's
    `secretStoreRef` and `target`, a `clusterexternalsecret`'s
    `externalSecretMetadata`, an AWS provider's `auth` and a Vault provider's
    `tls`, and the test is in `externalsecrets_kinds_test.go`.)
  - `TestExternalSecretsKinds_InlineValues` finds every string `value` beside a
    `secretRef` in the linked module (14 at this pin) and fails on one that is in
    neither list. ("neither list" is the reference's list of refused fields and
    its list of seven other fields that are not refused, and the test is in
    `externalsecrets_kinds_internal_test.go`.)
  - `TestSecretStoreKinds_FakeData` pins the order. ("the order" is the order of
    the two refusals the reference gives for the `fake` provider's `data`, and the
    test is in `externalsecrets_kinds_test.go`.)
  - `TestPolicyReadsKind_IsWhatTheRenderedObjectCheckReads` fails when the refused
    set and the check differ. ("the refused set" is the reference's list of kinds
    that `target.manifest` refuses, and "the check" is the rendered-object check
    the reference names, and the test is in `policy_reads_kind_internal_test.go`.)

## replicationsource, replicationdestination

- **Implementation:** Each is built on `policyHeldKind` (`policyFreeKind` with a
  policy check, see above) ("Each" is `replicationsource` and
  `replicationdestination`).
- **History:** go-kure/launcher#790 (the kind-named projections);
  go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`);
  go-kure/launcher#883 (the kinds' required lists are generated from the linked
  module's CRDs).
- **Tests:**
  - `TestVolsyncKinds_NoDefaults` holds the linked CRDs to that, and a dependency
    bump that adds a default fails there with the field named. ("that" is the
    reference's "VolSync's CRDs default no field under `spec`", and the test is in
    `volsync_kinds_internal_test.go`.)
  - `TestKindComponents_NullRequired` shows the refusal with the validator of the
    linked CRDs. ("the refusal" is the refusal of an unauthored
    `nodeSelectorTerms` in a mover's required node affinity, and the test is in
    `kind_null_required_internal_test.go`.)
  - The lists (192 paths for a source, 142 for a destination) are derived from the
    CRDs the linked module ships, by `TestKindComponents_RequiredIsGenerated`; a
    dependency bump that adds, drops or moves one fails there until the list is
    regenerated. ("The lists" are the reference's required lists, "one" is a path
    and "there" is `TestKindComponents_RequiredIsGenerated`, and the test is in
    `kind_required_internal_test.go`.)
  - `TestKindComponents_GeneratedRequiredBothWays` shows the API server's answer
    to the omission of a field of each type that holds one (15 rows for a source,
    14 for a destination). (The test is in `kind_required_internal_test.go`.)
  - `TestVolsyncKinds_WrittenEmptyAccepted` holds that every other field of
    VolSync's own types the type writes empty is one the CRDs take empty. (The
    test is in `volsync_kinds_internal_test.go`.)
  - `TestVolsyncKinds_AbsentFromCRD` derives the fields the types reach that the
    linked CRDs have no property for and holds the refusals to them, both ways: a
    bump of either module that closes the gap, or widens it, fails there with the
    field named. ("the refusals" are the refusals of the two fields the reference
    names, `secret.defaultUser` and `secret.items[].user`, and the test is in
    `volsync_kinds_internal_test.go`.) It also runs the API server's own create
    sequence on the linked CRDs (`crdCreate`): the API server names each of the
    two as an unknown field, for which a request made with strict field
    validation is refused, and prunes it from the object it goes on with. ("It" is
    `TestVolsyncKinds_AbsentFromCRD`, and "the two" are the same two fields.)
  - `TestVolsyncKinds_NoExpressionRules` holds the linked CRDs to that, and a
    dependency bump that adds one fails there with the rule named. ("that" is the
    reference's "The CRDs declare no expression rule" and "one" is such a rule, and the
    test is in `volsync_kinds_internal_test.go`.)
  - `TestVolsyncKinds_WrittenEmptyAccepted` holds every such field to its schema.
    ("every such field" is a field the type writes empty, as the `customCA` block
    of an `rclone` or `restic` mover, and the test is in `volsync_kinds_internal_test.go`.)
