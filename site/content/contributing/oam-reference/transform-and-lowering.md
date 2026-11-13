---
title: "Transform, extension and lowering"
weight: 22
---

Implementation, history and tests of the sections on the reference page
[Transform, extension and lowering](../../api-reference/oam/transform-and-lowering.md).

## Transform & extension

- **History:**
  - go-kure/launcher#734 (a `TraitHandler.Apply` must not replace, remove or rename a component's application: NetworkPolicy synthesis finds a component's application by its name and then its pointer, and a replaced, removed or renamed one would silently get no policy).
  - go-kure/launcher#752 (nor may it rename a sibling group member's application: the transform fails, naming the trait, the group and the member's type).
  - go-kure/launcher#751 (each value `ComponentCapabilityDefaults` fills is validated against the component's own `PropertySchema`, which must declare every listed key: the trait side's schema may differ).
  - go-kure/launcher#772 (a handler that declares no schema relies on `EvaluateProfile`, and `EvaluateProfile` validates the binding only through the trait handler or trait lowering rule of the key's type, and the fill is refused unless that handler or rule validates the rendering: it implements `ValidateAndApplyDefaults`, or the type is not built in and has a `CapabilityDefinition`, whose schema `EvaluateProfile` applies).
  - go-kure/launcher#742 (`persistentvolumeclaim` implements `ComponentCapabilityDefaults` for `pvc`, `storageClassName`; every bound key a `ComponentCapabilityDefaults` handler takes defaults from is in `ConsumedCapabilities`).
  - go-kure/launcher#761 (`statefulset` implements `ComponentCapabilityFiller` for `pvc`'s `storageClassName`; a `ComponentCapabilityFiller` reads a binding through `lctx.Capability`, so its keys are in `ConsumedCapabilities`).
  - go-kure/launcher#686 (every key a component, trait, document or policy lowering rule reads through `LoweringContext.Capability` is in `ConsumedCapabilities`).
  - go-kure/launcher#792 (`helmtemplate` implements `ApplicationNameSetter`, so its hook-group child layouts are named after the application and the component).
  - go-kure/launcher#787 (`helmtemplate` implements `HookGroupNamePrefixSetter` and `LayoutKustomizationNameSetter`).
  - go-kure/launcher#763 (a `SubApplicationDecorator`'s `Apply` must not rename a sibling group member's application, which the bundle does not hold: the transform fails, naming the trait, the sub-application it was decorating, the group and the member's type).
  - go-kure/launcher#782 (`prune-protection` and `force-replace` implement `SubApplicationDecorator`; the `force-replace` trait sets the `ForceReplace` delivery intent).
  - go-kure/launcher#702 (no pod kind generates a ServiceAccount, so `ServiceAccountName` returns `""` when none is authored).
  - go-kure/launcher#781 (no built-in handler writes `PolicyResult.Extensions`).
  - go-kure/launcher#1317 (a named `int32` or `int64` is the same value as the `int` an integer schema gives it and as the `int32` or `int64` a number schema keeps, so a rule that hands one on in the other width reports `copied`).
  - go-kure/launcher#1308 (a change of width between plain integers reports `rendered`, since they keep their width).
  - go-kure/launcher#1323 (Provenance assumes a 64-bit `int`; launcher builds for amd64 and arm64 only).

## Lowering

- **History:**
  - go-kure/launcher#626 (a component or policy is checked on its own copy of its properties in `LowerRaws`, so checking it cannot strip a reserved `null` from a trait or another element that shares its map before `Transform` sees it).
  - go-kure/launcher#357 (`LowerRaws` schema-checked emitted traits itself; it does not any more, so a caller that relied on that adds the `ValidateAuthoredProperties` call).
  - go-kure/launcher#1338 (`errors.Is(err, oam.ErrGeneratedObjectCollision)` is true, also behind a prefix a caller adds, and `errors.As` finds the error and tells which objects collide and who produces each, without parsing the text).
  - go-kure/launcher#646 (a component and another component's trait, two components, or two traits can each generate the same object, and both pass `Transform`).
  - go-kure/launcher#757 (one component's own traits, or a trait and an application the component generates itself, rendering one object; two traits of one component whose sub-applications share a name read as `2 sub-applications "dup" of component "web"`, a trait named after its own component and that component as `2 applications "web" of component "web"`).
  - go-kure/launcher#787 (the transform refuses two applications of one name within one bundle; a name the `Naming` hook gave that meets, on another member, one it did not give is refused by name).
  - go-kure/launcher#1072 and go-kure/launcher#1074 (both checks hold an object a controller generates for a generated object as generated, for a HelmRelease with a chart template; the HelmChart claim is a refusal that a document that built before the claim existed may now meet).
  - go-kure/launcher#1080 (the `helmrelease` component refuses a HelmRelease whose source helm-controller could not apply).
  - go-kure/launcher#720 (a PersistentVolume or PersistentVolumeClaim that is force-applied, or covered by the `ForceReplace` delivery intent, is warned about, not refused).
  - go-kure/launcher#782 (the `force-replace` trait sets the `ForceReplace` delivery intent that forces an application).
  - go-kure/launcher#781 (a bundle whose `Force` the caller set before generating forces its applications; launcher never sets it).
  - go-kure/launcher#702 (a `webservice`/`worker` `volumes` entry that generates a claim, which one with `claimName` does not, is a synthesized `pvc` trait, so the claim is named as a sub-application of its component).
  - go-kure/launcher#702 (`webservice` lowers to a deployment, a service and a serviceaccount member, which give Deployment, Service, ServiceAccount, then the claims of the deployment member's synthesized `pvc` traits).
  - go-kure/launcher#794 (`deployment` is the one built-in pod kind a lowering rule emits into a group, and the only one that has `PodTemplateLabels`).
  - go-kure/launcher#755 (sub-applications are compared after each one's `ApplyPolicy` has run, so a policy that renames a sub-application onto another member's is refused, and the name it moved away from is free).
  - go-kure/launcher#612 (an authored value that only compares equal to a rendered one, such as `-0.0` over `0.0`, which a handler tells apart with `math.Signbit`).
  - go-kure/launcher#609 and go-kure/launcher#626 (for the output of a rule whose input was not checked, which stays authored, the components and traits it emits at trait position and those nested in the components it emits are checked for reserved keys as they are emitted, before emission validation strips an explicit `null`, so a reserved key written as `null` is refused as when authored directly).
  - go-kure/launcher#849 (a step attached with `Component.AfterPolicy` that holds the config to the policy refuses as `ApplyPolicy` does: an error that holds a `PolicyRefusal` fails the transform as the component's `ViolationError` with the refusal's class, any other error as a `TransformError`).
  - go-kure/launcher#281 and go-kure/launcher#729 (the `postgresql` rule attaches a step to the `cnpg-cluster` it emits, `CnpgClusterConfig.ApplyPostgresqlDefaults`, to set the values postgresql derives after the policy).
  - go-kure/launcher#783 (`Component.OrderAfter(names...)`).
  - go-kure/launcher#784 (the `helm` rule orders the release after the source it generates or adopts, and the `oci` rule does the same for each Kustomization of a source several `oci` components share; the `SourceDeduplicatable` interface and the pass that let the first config of a shared source key emit it were removed).
  - go-kure/launcher#1041 (`Component.WaitsAs(name)`).
