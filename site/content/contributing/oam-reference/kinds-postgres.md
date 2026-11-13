---
title: "Kinds: PostgreSQL and CloudNativePG"
weight: 11
---

Implementation, history and tests of the entries on the reference page [Kinds:
PostgreSQL and
CloudNativePG](../../api-reference/oam-components/kinds-postgres.md). Every test
named here is in `pkg/oam/builtin/components/`, in the file the note beside it
names.

## cnpg-imagecatalog, cnpg-clusterimagecatalog, cnpg-backup, cnpg-scheduledbackup, cnpg-databaserole, cnpg-publication, cnpg-subscription

- **History:**
  - go-kure/launcher#790 (the kind-named projections) [The reference cited it
    after "**cnpg-clusterimagecatalog**, **cnpg-backup**,
    **cnpg-scheduledbackup**, **cnpg-databaserole**, **cnpg-publication**,
    **cnpg-subscription**" in its introduction.]
  - go-kure/launcher#1001 (complete CNPG backup and recovery to S3 through OAM
    lowering) [The reference cited it after "a `cnpg-scheduledbackup` for each
    `backup.schedules` entry" in its Outputs section.]
  - go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`) [The
    reference cited it after "through the object kind policy
    (`oam.ObjectKindPolicy`" in its Validation section.]
  - go-kure/launcher#883 (refusing an omitted CRD-required field the Go type
    writes empty) [The reference cited it after "require and the types write
    unauthored" in its Validation section.]
- **Tests:**
  - With `connectionLimit` these are every such field of the seven specs;
    `TestCnpgKindsDefaultedZeroFields_MatchCRD` derives each kind's list from
    the linked CRDs and fails on another. ["These" are the seven string fields
    the CRDs default, which the reference page gives, "each kind's list" is the
    list of such fields of that kind, "another" is a field of the same kind that
    the list does not hold, and the test is in
    `cnpg_kinds_defaulted_zero_internal_test.go`.]
  - `TestCnpgPublication_TargetAsWritten` shows the first for each of the four,
    and the second for the three that can stand alone (an empty `columns` alone
    is a table that lists none). ["The first" is the case in which the API
    server refuses the document as authored and accepts the object the build
    writes, "the second" is the case in which the API server accepts the
    document as authored and the build refuses it, "the four" are `allTables:
    false` and an empty `objects`, `tablesInSchema` or `columns`, which the
    reference page gives, and the test is in
    `cnpg_further_kinds_internal_test.go`.]
  - `TestCnpgFurtherKinds_ExpressionRules` lists all 26 with what is done with
    each, and fails on a rule that is added, dropped or reworded. ["All 26" are
    the 26 expression rules of the seven CRDs, which the reference page counts,
    and the test is in `cnpg_further_kinds_internal_test.go`.]
  - It holds each of the 15 to the API server's own expression validator, run
    over the linked CRD after the defaults the API server fills: with every
    other rule set aside, the validator refuses the properties that break the
    rule, and with every rule it accepts the ones next to them and the object
    the build writes for them; the build answers the same on both. ["It" is
    `TestCnpgFurtherKinds_ExpressionRules`, which the previous entry names, "the
    15" are the 15 rules that read one document, which the reference page names,
    and the test is in `cnpg_further_kinds_internal_test.go`.]
  - `TestImageTagRule_CnpgImageCatalogs` holds the three, on both kinds. ["The
    three" are the three fields of a catalog that name an image, which the
    reference page gives, "both kinds" are `cnpg-imagecatalog` and
    `cnpg-clusterimagecatalog`, and the test is in `image_tag_rule_test.go`.]
  - `TestCnpgImageCatalogs_ImagesAreHeldToTheAllowedRegistries` holds the three,
    on both kinds. ["The three" are the three fields of a catalog that name an
    image, which the reference page gives, "both kinds" are `cnpg-imagecatalog`
    and `cnpg-clusterimagecatalog`, and the test is in
    `cnpg_further_kinds_test.go`.]

## postgresql

- **History:**
  - go-kure/launcher#448 (a mistyped affinity property and its sub-fields
    silently discarded) [The reference cited it after "not a fallback to the
    default" in its Properties section.]
  - go-kure/launcher#466 (string maps that silently drop non-string values) [The
    reference cited it after "as `nodeSelector` when several are wrong" in its
    Properties section.]
  - go-kure/launcher#281 (the operator-CR component pattern, with postgresql as
    a lowering onto a CNPG Cluster component) [The reference cited it after
    "with `-<n>` appended. **Behavior-changing** under
    `launcher.gokure.dev/v1alpha1`" in its Outputs section; and after "**A
    component lowering rule** (`PostgresqlRule`, `postgresql_lowering.go`" in
    its Outputs section.]
  - go-kure/launcher#1001 (complete CNPG backup and recovery to S3 through OAM
    lowering) [The reference cited it after "**Backup**" in its Outputs section;
    and after "**Recovery**" in its Outputs section; and after "is named. **The
    recovery ObjectStore's name**" in its Validation section; and after
    "refused, naming both. **The ScheduledBackups' names**" in its Validation
    section.]
  - go-kure/launcher#1032 (refusing a backup schedule the operator would never
    act on) [The reference cited it after "the operator's look-ahead from every
    instant" in its Outputs section.]
  - go-kure/launcher#615 (refusing component names CloudNativePG rejects as a
    Cluster name) [The reference cited it after "it is now refused at build" in
    its Validation section.]
  - go-kure/launcher#589 (endpoint selectors that carry the raw component name
    and break over 63 characters) [The reference cited it after "it is now
    refused at build" in its Validation section.]
  - go-kure/launcher#539 (an empty version yielding an image reference with an
    empty tag) [The reference cited it after "that only the image pull rejected"
    in its Validation section.]
  - go-kure/launcher#623 (storage size and instance count not fully validated)
    [The reference cited it after "it is now refused at build" in its Validation
    section.]
  - go-kure/launcher#790 (the kind-named projections) [The reference cited it
    after "(`ValidateImageRef`), with or without a policy" in its Validation
    section; and after "enabled pooler without `pooler.image` is refused" in its
    Validation section; and after "**Two object store paths are required**" in
    its Validation section; and in its Validation section; and in its Properties
    section.]
  - go-kure/launcher#1269 (honouring or refusing `bootstrap.initdb` on a
    postgresql component) [The reference cited it after "**`bootstrap`** is
    declared key by key" in its Properties section.]
  - go-kure/launcher#1027 (declaring operator and plugin prerequisites
    machine-readably) [The reference cited it after "for a tool to read
    (`ContractMetadata.Prerequisites`" in its Outputs section.]
  - go-kure/launcher#1044 (the archive-into-source check not seeing a sibling's
    inline recovery store) [The reference cited it after "shortened as every
    generated name is" in its Outputs section.]
  - go-kure/launcher#1041 (transitive generated ordering not visible to a
    sibling lowering's extra member) [The reference cited it after "its Pooler,
    Databases and ScheduledBackups too" in its Outputs section.]
  - go-kure/launcher#512 (optional properties read with bare type assertions)
    [The reference cited it after "goes through the same presence-reporting
    helpers" in its Validation section.]
  - go-kure/launcher#659 (a managed role's `connectionLimit: 0` dropped) [The
    reference cited it after "is a managed role's `connectionLimit: 0`" in its
    Validation section.]
  - go-kure/launcher#787 (a name override for every generated name: an author
    property and a consumer hook) [The reference cited it after "**The Pooler's
    and the Databases' names**" in its Validation section; and after "**The
    Cluster's and the ObjectStore's names**" in its Validation section.]
  - go-kure/launcher#1261 (the interpreter for configuration-defined component
    models) [The reference cited it after "the name of the Secret
    `<cluster>-app`" in its Validation section.]
  - go-kure/launcher#1230 (version-gated values: tolerations, the Service name
    rule and `trafficDistribution`) [The reference cited it after "naming the
    entry by its index" in its Validation section.]
- **Tests:**
  - `TestPostgresqlRule_UnauthoredRequiredStrings` reads all eleven in the
    lowered component. ["All eleven" are the two strings whose parent the
    lowering fills and the other nine, which the reference page gives, and the
    test is in `postgresql_required_strings_internal_test.go`.]
  - `TestPostgresqlRule_BlockNotBuilt` holds each case. ["Each case" is each of
    the cases the reference page gives above it: an authored `enabled: false`, a
    block with nothing in it and `backup: {}`, and the test is in
    `postgresql_dropped_values_internal_test.go`.]
