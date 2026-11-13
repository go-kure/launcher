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

## pod

- **History:**
  - go-kure/launcher#790 (`pod` as the kind-named projection of a v1 Pod; image volumes held
    to the allowed registries and to the tag rule, a breaking change for a document, a chart
    or a source naming an untagged or `:latest` image in a field held to the registry rule).
  - go-kure/launcher#1177 (the nested form of a `volumes` entry (`type: image`, `image:
    {reference: ...}`) on the components with a volume schema of their own).
  - go-kure/launcher#788 (the transform sets the component label on every object a component
    owns).
- **Tests:**
  - The list is held to the API server's own defaulting code by
    `TestKubernetesDefaulters_MatchVendoredSource`. It reads an excerpt of that code, copied
    unmodified from kubernetes/kubernetes at the release of the linked `k8s.io/api` under its
    Apache-2.0 licence, in `testdata/upstream/kubernetes/` (see the next paragraph). The test
    walks the generated `SetObjectDefaults_` function of the Pod, PodTemplate,
    ReplicationController and ReplicaSet objects and every defaulting function they call. It
    fails on a field the code defaults and the list does not hold, on a default the list
    states otherwise, and on a row the code does not set. The walk is closed: it accepts only
    the kinds of statement, expression and type, the unary operators and the calls the excerpt
    at the vendored tag uses, each listed in the test (a method by its receiver's identity, a
    builtin only while no name of the excerpt shadows it), and fails on anything else, naming
    it and its position, rather than guess. It also fails on listed shapes it cannot follow:
    among others, a write through a copy of a field (read by name or through a dereference), a
    list element reached by anything but a range over that list, a `break` out of a loop, a
    method called on the object, a variable shadowing one of an enclosing block, or a write it
    cannot place in the object. The trade-off is that a re-vendoring whose code uses a new
    shape or call fails the test until that shape is understood and listed, even when it
    changes no default. Most defaults are set by a `SetDefaults_` function. The exceptions are
    a port's `protocol` and the `iscsi`, `rbd` and `scaleIO` strings, which `k8s.io/api`
    declares with a `+default` marker and the generated functions apply, and `hostPort`, which
    `defaultHostNetworkPorts` sets. A second test holds the numbers to the field comments of
    the linked types. ["The list" is the list of values the API server would replace with its
    default, in the item above; the first test is in `kubernetes_defaulters_internal_test.go`,
    "the next paragraph" is the next bullet, and the second test is
    `TestPodSpecDefaultedZeros_MatchFieldDocs`, in `pod_defaulted_zero_internal_test.go`]
  - The excerpt in `testdata/upstream/kubernetes/` holds eight files of kubernetes/kubernetes
    under its Apache-2.0 licence, each with its header, and kubernetes/kubernetes's `LICENSE`,
    the licence's full text, at the same tag. Its `SOURCE` file names the tag and the git blob
    id of each file and of `LICENSE`; the tests check each against its file, the tag against
    the linked `k8s.io/api`, and fail when `LICENSE` is missing. `mise run
    vendor-k8s-defaulters vX.Y.Z` re-fetches it when that module moves; it takes the
    Kubernetes tag, not the module version: `k8s.io/api` v0.X.Y pairs with Kubernetes v1.X.Y.
    CI never runs it, and the tests need no network. ["The excerpt" is the one the first test
    in the previous bullet reads, and "the tests" are the tests of
    `kubernetes_defaulters_internal_test.go` that read it, among them
    `TestKubernetesDefaulters_ExcerptMatchesLinkedRelease`, which holds the tag to the linked
    `k8s.io/api`]
  - `TestKindComponents_OmittedRequiredAndWrittenDefaults` lists the four with that reason.
    ["the four" are the four fields the paragraph names: the `action` and the exit codes'
    `operator` of a container restart rule, and the `signerName` and `keyType` of a pod
    certificate source; "that reason" is the reason the paragraph gives, and the test is in
    `kind_api_sets_internal_test.go`]
  - A test walks the linked `k8s.io/api` type and fails on a field whose name holds `image`,
    or whose type is the image volume source, that is neither held nor listed with a reason;
    an image field under another name and another type is not found by it. [the test is
    `TestImageFields_HeldOrListed`, in `image_fields_internal_test.go`]
  - The test that walks the types fails on a field held to the registry rule and not to this
    one. ["The test that walks the types" is `TestImageFields_HeldOrListed`, named in the item
    for image volumes]

## replicaset

- **History:**
  - go-kure/launcher#790 (`replicaset` as the kind-named projection of an apps/v1 ReplicaSet).
  - go-kure/launcher#788 (the transform sets the component label on the object and its
    template).
- **Tests:**
  - A test holds the probe list to every non-pointer `omitempty` number or boolean under
    `ReplicaSetSpec` whose field comment states a non-zero default, the template's metadata
    included, and `TestKubernetesDefaulters_MatchVendoredSource` holds the whole list to the
    API server's defaulting code for a ReplicaSet. ["the probe list" is the list of values the
    API server would replace with its default, and the first test is
    `TestPodTemplateKindsDefaultedZeros_MatchFieldDocs`, in
    `pod_template_defaulted_zero_internal_test.go`; the second is the test the `pod` entry
    names]

## replicationcontroller

- **History:**
  - go-kure/launcher#790 (`replicationcontroller` as the kind-named projection of a v1
    ReplicationController).

## podtemplate

- **History:**
  - go-kure/launcher#790 (`podtemplate` as the kind-named projection of a v1 PodTemplate).
  - go-kure/launcher#794 (`security-context` with `psaLevel` alone is accepted on a
    `podtemplate`, as a declaration of the level).
  - go-kure/launcher#788 (the transform sets the component label on the object and its
    template).

## storageclass, volumeattributesclass, priorityclass, runtimeclass, ingressclass, csidriver

- **Implementation:** **One shared helper builds all six** (`policyFreeKind`, in
  `kind_policy_free.go`), for a kind to which no dimension of the environment policy applies;
  `servicecidr`, `poddisruptionbudget`, `endpointslice`, the four kinds of the RBAC API, the
  four of the Prometheus operator's API, the four of Cilium's BGP control plane and the five
  kinds of the Gateway API's infrastructure objects are built on it too. The three kinds of
  cert-manager's API, the two of VolSync's, `alertmanager`, `thanosruler` and `prometheus` are
  built on `policyHeldKind` (`kind_policy_held.go`): this helper, unchanged, with an
  `ApplyPolicy` that asks one function of the kind whether the policy refuses the decoded
  value. It refuses or passes; it fills no default. A kind is a value of it naming the
  upstream type, an optional check of required fields, an optional list of required fields the
  type writes whether or not they were authored, and the base-library constructor; the helper
  is the rest: the strict decode of the property map into the upstream type under the
  package's null contract, which refuses a `null` list element by its path (`decodeKindSpec`),
  the refusal of a key written in two spellings (`refuseUncarriedSpecValues`), the refusal of
  a listed required field that was not authored (`refuseUnauthoredRequired`, which reads the
  authored properties, since the decoded value does not show the omission, and follows a list
  to each of its items), a config whose `ApplyPolicy` does nothing, and a `Generate` that
  returns the constructor's object, under the object name the config carries, with a deep copy
  of what was decoded, so two builds of one config share nothing. The config type is
  unexported: a component is only built from properties that went through the decode. [the
  sentence on `defaultedZeros`, which follows on the reference page, is the rule this
  paragraph goes on from; the next bullet under Tests holds the rest of this paragraph, which
  names the tests that hold the helper's kinds to the API defaults]
- **History:**
  - go-kure/launcher#790 (the six kinds as kind-named projections of one cluster-scoped object
    each).
  - go-kure/launcher#788 (the transform sets the component label on every object a component
    owns).
- **Tests:**
  - A kind that lists none suits a type only when none of its omit-when-zero numbers or
    booleans has a non-zero API default; `TestPolicyFreeKinds_NoDefaultedZeros` reads the
    field comments of every Kubernetes type built on it and fails on one whose comment states
    such a default in a form it recognises (`Defaults to 1`, `Default is true`). A default the
    comment words otherwise, or does not state, is not found, and a string's default is not
    read from a comment. The types of the Prometheus operator's API publish no field comment;
    `TestMonitoringKinds_DefaultedZeros` reads the default markers of their source instead,
    strings included. Cilium's publish none either, and its module ships the CRDs:
    `TestCiliumBGPKinds_DefaultsSitOnPointers` holds every default of the four BGP CRDs to a
    field that is a pointer in the Go type. ["A kind that lists none" is a kind that lists no
    `defaultedZeros`, in the sentence before it on the reference page;
    `TestPolicyFreeKinds_NoDefaultedZeros` is in `kind_policy_free_internal_test.go`]
  - `TestKindComponents_DefaultedEmptyLists` walks every kind in its table of API sources,
    finds each list omitted when empty that its source gives a default (a CRD schema or a
    default marker) or whose field comment mentions a default, and holds each to an answer:
    refused, or not refused with the reason. A newly defaulted list fails it until it is
    answered. The three refused defaults are held to the API server's defaulting code by
    `TestKubernetesDefaulters_ListDefaultsMatchVendoredSource`, from the excerpt the `pod`
    kind's list is held to: the functions that reach those defaults are held to their exact
    statements, and each default to being the only write to its field or to anything beneath
    it. Two limits: the lists of the Kubernetes types are found from their field comments, so
    a list the API server defaults without its comment saying so is not found; and the table
    of kinds it walks is not itself proven to hold every kind component. [the test is in
    `kind_defaulted_lists_internal_test.go`; "it" is that test, and "the excerpt the `pod`
    kind's list is held to" is the excerpt the `pod` entry describes]

## horizontalpodautoscaler

- **History:**
  - go-kure/launcher#790 (`horizontalpodautoscaler` as the kind-named projection of an
    `autoscaling/v2` HorizontalPodAutoscaler).
- **Tests:**
  - The type has no number or boolean that is omitted when zero, which
    `TestHorizontalPodAutoscalerSpec_NoOmittedZeros` holds it to. [the test is in
    `horizontalpodautoscaler_internal_test.go`]
