---
title: "Transform, extension and lowering"
weight: 22
---

Implementation, history and tests of the sections on the reference page
[Transform, extension and lowering](../../api-reference/oam/transform-and-lowering.md).

## Transform & extension

- **History:**
  - go-kure/launcher#734 (a `TraitHandler.Apply` must not replace, remove or rename a component's application, its own or any other component's of the bundle, including one whose traits already ran: the transform fails, naming the trait and the component, because NetworkPolicy synthesis finds a component's application by its name and then its pointer, and a replaced, removed or renamed one would silently get no policy).
  - go-kure/launcher#752 (nor may a `TraitHandler.Apply` rename a sibling group member's application: the member would generate its objects under a name the group does not carry, so the transform fails, naming the trait, the group and the member's type).
  - go-kure/launcher#751 (each value `ComponentCapabilityDefaults` fills is validated against the component's own `PropertySchema`, which must declare every listed key: the trait side's schema may differ).
  - go-kure/launcher#772 (a `ComponentCapabilityDefaults` handler that declares no schema relies on `EvaluateProfile`, which validates the binding only through the trait handler or trait lowering rule of the key's type. Its fill is refused unless that handler or rule validates the rendering: it implements `ValidateAndApplyDefaults`, or the type is not built in and has a `CapabilityDefinition`, whose schema `EvaluateProfile` applies).
  - go-kure/launcher#742 (`persistentvolumeclaim` implements `ComponentCapabilityDefaults` for `pvc`, `storageClassName`; every bound key a `ComponentCapabilityDefaults` handler takes defaults from is in `ConsumedCapabilities`).
  - go-kure/launcher#761 (`statefulset` implements `ComponentCapabilityFiller`: it fills `pvc`'s `storageClassName` into each `volumeClaimTemplates` entry that leaves `storageClass` unauthored; a `ComponentCapabilityFiller` reads a binding only through `lctx.Capability`, which records the key in `ConsumedCapabilities`).
  - go-kure/launcher#686 (every key a component, trait, document or policy lowering rule reads through `LoweringContext.Capability` is in `ConsumedCapabilities`).
  - go-kure/launcher#792 (`helmtemplate` implements `ApplicationNameSetter`, whose hook-group child layouts are named `<application>-<component>-NN-<phase-slug>`).
  - go-kure/launcher#787 (`helmtemplate` implements `HookGroupNamePrefixSetter` through `HelmTemplateConfig.HookGroupNamePrefix`, and `LayoutKustomizationNameSetter` through `HelmTemplateConfig.LayoutKustomizationName`, which `AugmentLayout` sets on the layout unless it carries one).
  - go-kure/launcher#763 (when a `SubApplicationDecorator`'s `Apply` runs on a sub-application, it must not add, remove, replace, rename or reorder the bundle's applications there (the transform fails), nor rename a sibling group member's application, which the bundle does not hold: the transform fails, naming the trait, the sub-application it was decorating, the group and the member's type).
  - go-kure/launcher#782 (`prune-protection` and `force-replace` implement `SubApplicationDecorator`, whose `Apply` sets the application's delivery intent and wraps nothing).
  - go-kure/launcher#702 (no pod kind generates a ServiceAccount, so `ServiceAccountName` returns `""` when none is authored; a `webservice`/`worker` hands its `deployment` member the name of the account it generates).
  - go-kure/launcher#781 (no built-in handler writes `PolicyResult.Extensions`).
  - go-kure/launcher#1317 (a named `int32` or `int64` is the same value as the `int` an integer schema gives it and as the `int32` or `int64` a number schema keeps, in either width and at any depth in a list or object: a rule that hands a named `int32` on as an `int64`, or the reverse, reports `copied` under either schema).
  - go-kure/launcher#1308 (a plain `int`, `int32` or `int64` is left as it is, so a change of width between plain integers reports `rendered`, since they keep their width).
  - go-kure/launcher#1323 (Provenance assumes a 64-bit `int`: on a 32-bit build a named `int64` outside the range of `int` is not folded and reports `rendered`; launcher builds for amd64 and arm64 only).

## Lowering

- **History:**
  - go-kure/launcher#626 (a component or policy is checked on its own copy of its properties in `LowerRaws`, so checking it cannot strip a reserved `null` from a trait or another element that shares its map before `Transform` sees it).
  - go-kure/launcher#357 (`LowerRaws` schema-checked emitted traits itself; it does not any more, so a caller that relied on that adds the `ValidateAuthoredProperties` call).
  - go-kure/launcher#1338 (`errors.Is(err, oam.ErrGeneratedObjectCollision)` is true, also behind a prefix a caller adds, and `errors.As` finds the error and tells which objects collide and who produces each, without parsing the text).
  - go-kure/launcher#646 (a component and another component's trait, two components, or two traits can each generate the same object, and both pass `Transform`).
  - go-kure/launcher#757 (one component's own traits, or a trait and an application the component generates itself, rendering one object; two traits of one component whose sub-applications share a name read as `2 sub-applications "dup" of component "web"`, a trait named after its own component and that component as `2 applications "web" of component "web"`).
  - go-kure/launcher#787 (the transform itself refuses both of those shapes within one bundle (two applications of one name), so the check meets them only in a cluster built some other way; a name the `Naming` hook gave that meets, on another member, one it did not give is refused by name).
  - go-kure/launcher#1072 and go-kure/launcher#1074 (both checks also hold an object a controller generates for a generated object as generated: for a HelmRelease with a chart template (`spec.chart`), helm-controller generates a source of the template's kind named `<namespace>-<name>` after the HelmRelease, and the HelmChart claim is a refusal that a document that built before the claim existed may now meet: a `helmchart` component named `<namespace>-<name>` after a `helmrelease` beside it, in the same namespace).
  - go-kure/launcher#1080 (`GeneratedChartSource(obj)` returns that source, and the `helmrelease` component reads its claim from it, and refuses a HelmRelease whose source helm-controller could not apply).
  - go-kure/launcher#720 (a PersistentVolume or PersistentVolumeClaim that is force-applied, or covered by the `ForceReplace` delivery intent, is warned about, not refused: when an update changes an immutable field of a force-applied object, Flux deletes and recreates it instead of failing the apply, which can lose a claim's data).
  - go-kure/launcher#782 (an application is forced when it carries the `ForceReplace` delivery intent, which the `force-replace` trait sets).
  - go-kure/launcher#781 (an application is forced when its bundle sets `Force`; launcher never does, so only a bundle whose `Force` the caller set before generating).
  - go-kure/launcher#702 (a `webservice`/`worker` `volumes` entry that generates a claim, which one with `claimName` does not, is a synthesized `pvc` trait, so the claim is named as a sub-application of its component).
  - go-kure/launcher#702 (`webservice` lowers to a deployment, a service and a serviceaccount member, which give Deployment, Service, ServiceAccount, then the claims of the deployment member's synthesized `pvc` traits).
  - go-kure/launcher#794 (a config's `PodTemplateLabels() map[string]string`: a member without it counts as running no pods; of the built-in pod kinds only `deployment` has it, the one pod kind a lowering rule emits into a group).
  - go-kure/launcher#755 (sub-applications are compared after each one's `ApplyPolicy` has run, so a policy that renames a sub-application onto another member's is refused, and the name it moved away from is free).
  - go-kure/launcher#612 (a value a rule copied into a reserved key from a trait, a policy, metadata or another component, or changed after recording it, is refused like any authored one, including a number that only compares equal, such as an authored `-0.0` over a rendered `0.0`, which a handler tells apart with `math.Signbit`).
  - go-kure/launcher#609 and go-kure/launcher#626 (for the output of a rule whose input was not checked, which stays authored, the components and traits it emits at trait position and those nested in the components it emits are checked for reserved keys as they are emitted, before emission validation strips an explicit `null`, so a reserved key written as `null` is refused as when authored directly).
  - go-kure/launcher#849 (a step attached with `Component.AfterPolicy` that holds the config to the policy refuses as `ApplyPolicy` does: an error that holds a `PolicyRefusal` fails the transform as the component's `ViolationError` with the refusal's class, any other error as a `TransformError`).
  - go-kure/launcher#281 and go-kure/launcher#729 (the `postgresql` rule attaches a step to the `cnpg-cluster` it emits, `CnpgClusterConfig.ApplyPostgresqlDefaults`, to set the values postgresql derives after the policy).
  - go-kure/launcher#783 (a rule that emits components of which one must be applied before another declares it on the later one with `Component.OrderAfter(names...)`).
  - go-kure/launcher#784 (the `helm` rule orders the release after the source it generates or adopts, and the `oci` rule does the same for each Kustomization of a source several `oci` components share; the `SourceDeduplicatable` interface and the pass that let the first config of a shared source key emit it were removed).
  - go-kure/launcher#1041 (a component a rule emits that must not wait for the component it lowers, but must still wait for what that one waits for, declares it with `Component.WaitsAs(name)`).
