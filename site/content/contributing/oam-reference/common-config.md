---
title: "Common config"
weight: 1
---

Implementation, history and tests of the entries on the reference page
[Common config](../../api-reference/oam-components/common-config.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Common config

- **History:**
  - go-kure/launcher#423 (the six properties and `command`/`args` read through
    presence-reporting helpers).
  - go-kure/launcher#447 (a top-level entry with `name` absent or null refused by schema
    validation).
  - go-kure/launcher#453 (the parser message for a non-string at those positions).
  - go-kure/launcher#546 (a dotted or longer name refused at conversion by the Service-name
    rule).
  - go-kure/launcher#690 (`daemonset` and `statefulset` stop emitting a Service, and `port`
    goes).
  - go-kure/launcher#383 (`cpu` and `memory` declared as a string-or-number union).
  - go-kure/launcher#342 (the pod-level `resourceClaims` property accepted).
  - go-kure/launcher#1228 (a `sleep` longer than the pod's `terminationGracePeriodSeconds`
    refused).
  - go-kure/launcher#790 (the container `windowsOptions` key set, its breaking rules for a
    pod-level value, and the closed key set of the `affinity` shorthand).
  - go-kure/launcher#384 (`pvc.size` must be a positive quantity).
  - go-kure/launcher#761 (`volumeClaimTemplates.storageClass` keeps the absent-versus-empty
    distinction).
  - go-kure/launcher#793 (`oam.ShortenNameWithSuffix` shortens a qualified volume name over
    253 characters).
  - go-kure/launcher#1177 (a volume entry can author its source as the upstream object under a
    key named like its `type`).
  - go-kure/launcher#1172 (a `volumeMounts` item is a closed key set).
  - go-kure/launcher#1193 (one helper reads the mount keys under the rules of upstream
    `ValidateVolumeMounts`).
  - go-kure/launcher#1199 (`subPath` and `subPathExpr` refused with a `..` element in all four
    places).
  - go-kure/launcher#1198 (the main container reads the same mount keys).
  - go-kure/launcher#1212, go-kure/launcher#1223 (the mount rules held on the typed upstream
    mounts of the other kinds, with the error naming the container and the mount's position).
  - go-kure/launcher#321 (an `initContainers` entry is a closed key set).
  - go-kure/launcher#1225 (port names unique across the pod).
  - go-kure/launcher#1226 (a pod whose containers share a name refused when the document is
    read).
  - go-kure/launcher#1254 (a container name in either list must be a DNS-1123 label).
  - go-kure/launcher#1278 (the same check again when the pod is generated).
  - go-kure/launcher#448 (the `postgresql` component's affinity block contract).
  - go-kure/launcher#452 (a non-string `nodeSelector` value, a behavior change under
    `v1alpha1`).
  - go-kure/launcher#444 later folded the dedicated
    `optionalObject`/`optionalObjectList`/`optionalStringList` wrappers those six originally
    used into the base `parseObjectField`/ `parseStringField`/etc. helpers themselves —
    callers now use the base helpers directly, and the null-as-absence behavior applies
    uniformly rather than only through the wrappers. [the six: `env`, `command`, `args`,
    `initContainers`, `sidecars` and `affinity`]
  - `job` refused the name from its introduction; the other six gained the check in
    go-kure/launcher#407. [the name is a workload component name; `job` and the other six are
    the seven workload kinds `webservice`, `worker`, `deployment`, `statefulset`, `daemonset`,
    `cronjob` and `job`]
  - Until go-kure/launcher#405 all three were read with a bare comma-ok assertion, so a
    handler called directly — without the schema validation an authored document gets first,
    which already refused these shapes — dropped the value and emitted a container with no
    requests or limits. [all three: `resources`, `requests` and `limits`]
  - Until go-kure/launcher#423 they were mishandled twice over: a mistyped `command: /bin/sh
    -c true` fell through their comma-ok guard and the container built with no command at all,
    and `command: [ls, 3]` emitted `["ls"]` and said nothing about the `3`. Closing it changed
    their signature to return an error, which is why the fix touches all 9 call sites — the
    seven workload kinds' own main containers, plus `initContainers` and `sidecars` in
    `common.go` [they: `command` and `args`]
  - go-kure/launcher#660; before that the name and protocol were taken unchecked, and a
    non-list `ports`, an unknown key, a non-string name or a non-string protocol, which then
    read as `TCP`, was dropped [the sentence is about the entries of a sidecar's `ports`]
  - before go-kure/launcher#466 it was refused as `must be a string, got <nil>` [it: a
    `nodeSelector` value that is null, such as `rack: null`]

