---
title: "Kinds: core workloads"
weight: 16
---

Implementation, history and tests of the entries on the reference page
[Kinds: core workloads](../../api-reference/oam-components/kinds-core-workloads.md).
Text in square brackets is added here to say what a moved sentence refers to.

## webservice, worker

- **History:**
  - go-kure/launcher#341 (the `DeploymentSpec`-level surface that `webservice` and `worker`
    share with `deployment`).
  - go-kure/launcher#1473 (`webservice` and `worker` publish `tolerations` and
    `topologySpreadConstraints`).
  - go-kure/launcher#280 (`worker` and `webservice` are component lowering rules, not
    handlers).
  - go-kure/launcher#702 (both rules emit the component's ServiceAccount as a `serviceaccount`
    member and turn each claim a `pvc` volume describes into a synthesized `pvc` trait).
  - go-kure/launcher#546 (a `webservice` component name must be a valid Service name).
  - go-kure/launcher#959 (a leading digit in a `webservice` component name under
    `RelaxedServiceNameValidation`).
  - go-kure/launcher#1230 (the same rule below Kubernetes 1.36 and the `spec.featureGates`
    setting).
  - go-kure/launcher#787 (`deploymentObjectName`, `serviceObjectName` and
    `serviceAccountObjectName`).
  - go-kure/launcher#545 (an ingress `portName` on a `webservice` must be `http`).

## deployment

- **History:**
  - go-kure/launcher#412 (the raw `corev1` scheduling surface: `affinity`, `tolerations` and
    `topologySpreadConstraints`).

## service

- **History:**
  - go-kure/launcher#411 (the kind-named Service, an independent component).
  - go-kure/launcher#383 (`targetPort` is published as the `Types: [integer, string]` union).
  - go-kure/launcher#790 (`type: ExternalName`, the further `ServiceSpec` fields, and the
    refusal of `externalIPs`, `clusterIPs` and a literal `clusterIP` address).
  - go-kure/launcher#1230 (`PreferSameZone` and `PreferSameNode` below Kubernetes 1.34, and a
    leading digit in the Service name below Kubernetes 1.36).
  - go-kure/launcher#690 (a headless Service, `clusterIP: None`).
  - go-kure/launcher#959 (a leading digit in the Service name under
    `RelaxedServiceNameValidation`).
  - go-kure/launcher#794 (`service` has no `ApplyPolicy`, deliberately).
  - go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`).

## serviceaccount, persistentvolumeclaim, configmap

- **History:**
  - go-kure/launcher#702 (`serviceaccount`, `persistentvolumeclaim` and `configmap` as
    kind-named projections of one object each).
  - go-kure/launcher#741 (the `pvc` and `configmap` traits as twins of the kinds).
  - go-kure/launcher#742 (an unauthored `storageClassName` comes from the ClusterProfile `pvc`
    capability).
  - go-kure/launcher#790 (the four claim-spec fields `selector`, `dataSourceRef`, `volumeName`
    and `volumeAttributesClassName`).
  - go-kure/launcher#794 (`serviceaccount` and `configmap` have no `ApplyPolicy`,
    deliberately).
  - go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`).

## secret

- **History:**
  - go-kure/launcher#790 (`secret` as the kind-named projection of a v1 Secret).

## namespace, limitrange, resourcequota

- **History:**
  - go-kure/launcher#790 (`namespace`, `limitrange` and `resourcequota` as kind-named
    projections of one core object each).
  - go-kure/launcher#788 (the transform sets the component label on every object a component
    owns).
- **Tests:**
  - `TestCoreKindSchemas_CoverSpec` holds each schema to the linked type by reflection. ["each
    schema" is the schema of `namespace`, `limitrange` and `resourcequota`, and the test is in
    `core_kinds_test.go`]

## persistentvolume

- **History:**
  - go-kure/launcher#790 (`persistentvolume` as the kind-named projection of a v1
    PersistentVolume).
  - go-kure/launcher#794 (`csi` and `flexVolume` sources are not covered by the hostPath gate,
    a decided limit).
