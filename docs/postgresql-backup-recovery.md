# PostgreSQL backup and point-in-time recovery

How to back up a `postgresql` component to S3-compatible object storage, and
how to recover it into a new component, to the end of the archived WAL or to a
point in time (go-kure/launcher#1001). The properties are specified in the
components README (**postgresql**, "Backup" and "Recovery"); this guide is how
to use them and how to check that they work.

Both go through the Barman Cloud plugin, the one path CloudNativePG keeps once
its in-tree Barman support is removed. launcher lowers them into the base kinds
`cnpg-cluster`, `cnpg-objectstore` and `cnpg-scheduledbackup` and adds nothing
to them.

## Prerequisites

launcher builds offline and cannot see the cluster, so it checks none of these.

| What | Requirement |
|---|---|
| CloudNativePG operator | 1.26 or later, the plugin's minimum. The emitted objects follow the 1.30.1 API launcher is built against; use 1.30.x. |
| Barman Cloud plugin | `barman-cloud.cloudnative-pg.io` v0.15.x, with its `barmancloud.cnpg.io/v1` `ObjectStore` CRD. |
| cert-manager | Recommended by the plugin's install for the TLS between the plugin and the operator; otherwise supply those certificates yourself. |
| Bucket and Secrets | The bucket, the Secret `s3Credentials` names and, for an endpoint whose certificate a private CA signs, the Secret `endpointCA` names exist beforehand. launcher references them and never creates them. |

Check the installed versions before the first apply:

```sh
kubectl get deployment -n cnpg-system cnpg-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
kubectl get deployment -n cnpg-system barman-cloud \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
kubectl get crd objectstores.barmancloud.cnpg.io
```

A missing `ObjectStore` CRD fails the apply. An older CRD need not: the API
server drops the fields its schema lacks without an error, so a clean apply is
no proof that the configuration arrived intact. Read the applied objects back
(below).

## Back up

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
  - name: orders
    type: postgresql
    properties:
      version: "16"
      storageSize: 10Gi
      backup:
        objectStore:
          destinationPath: s3://backups/
          endpointURL: https://s3.example.internal:9000
          endpointCA: {name: s3-ca, key: ca.crt}
          s3Credentials:
            accessKeyId: {name: s3-creds, key: ACCESS_KEY_ID}
            secretAccessKey: {name: s3-creds, key: ACCESS_SECRET_KEY}
        retentionPolicy: 30d
        schedules:
        - name: daily
          schedule: "0 0 3 * * *"
```

This builds the Cluster with the plugin as its WAL archiver, an `ObjectStore`
named `orders`, and a ScheduledBackup `orders-daily` of method `plugin`. WAL is
archived from the start. `immediate` defaults to `true`, so the new Cluster gets
a base backup at once; the schedule takes the next ones. The schedule has six
fields, seconds first.

Build it against a profile of the target cluster and apply the output to that
cluster, where the namespace exists already:

```yaml
# cluster.yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: production
spec:
  capabilities: {}
```

```sh
kurel build app.yaml --profile cluster.yaml > built.yaml
kubectl apply --server-side -f built.yaml
```

A delivery tool such as Flux takes the same output instead.

Point-in-time recovery needs a base backup taken before the target and the WAL
after it. Without `schedules`, WAL is archived but nothing takes a base backup:
take one with a `cnpg-backup` component.

### Check that it works

The Cluster's `Ready` condition does not show it. A Cluster whose store cannot
be reached becomes Ready all the same: it is the archiving and backup
conditions that fail.

```sh
kubectl -n shop get cluster orders -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
# ContinuousArchiving=True ContinuousArchivingSuccess
# LastBackupSucceeded=True LastBackupSucceeded
kubectl -n shop get backup -l cnpg.io/scheduled-backup=orders-daily \
  -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,ID:.status.backupId
kubectl -n shop get objectstore orders -o jsonpath='{.status.serverRecoveryWindow}'
kubectl -n shop get cluster orders -o jsonpath='{.spec.plugins}'
```

The Backup reaches phase `completed` with a backup ID. The store's recovery
window names the server folder (`orders`, the Cluster's name unless
`backup.serverName` sets it) with its first recoverability point. The applied
Cluster still carries its `barman-cloud.cloudnative-pg.io` plugin entry with
`isWALArchiver: true`.

## Recover

A bootstrap is read only when a Cluster is created. Recover into a **new
component beside the source**, which stays as it is. `recovery` on a component
whose Cluster exists already changes nothing. Recovering by renaming the
source component's Cluster (`clusterObjectName`) creates a new Cluster, and a
delivery that prunes what the build no longer emits (Flux's `prune: true`,
`kubectl apply --prune`) then deletes the original, with its data, unless it
is protected.

Add the component to the document that holds the source:

```yaml
  - name: orders-restored
    type: postgresql
    properties:
      version: "16"
      storageSize: 10Gi
      recovery:
        source:
          serverName: orders          # the folder the source wrote
          objectStoreName: orders     # the source's ObjectStore
        target:
          time: "2026-10-26T01:43:31.582943Z"
```

`source.serverName` is the folder the source cluster wrote: its
`backup.serverName`, else its Cluster's name. The store is either
`objectStoreName`, an ObjectStore that exists in the namespace, or
`objectStore`, written inline as `backup.objectStore` is, which the component
emits as an ObjectStore of its own (`orders-restored-recovery-source`).

`target` is where recovery stops; without it, recovery replays all the archived
WAL. It takes at most one of `time`, `lsn`, `xid`, `name` and `immediate`, plus
`backupID`, `timeline` and `exclusive`. `xid`, `name` and `immediate` need
`backupID`, the base backup to start from, since the plugin can pick one by time
or LSN only:

```yaml
        target:
          name: before-migration      # a pg_create_restore_point() name
          backupID: "20261026T014258" # Backup .status.backupId
```

A time target stops at the first transaction committed after the time. If no
transaction follows it in the archived WAL, recovery fails rather than ending
at the last one.

The restored Cluster is a new database on a new timeline. To archive it as
well, give it a `backup` of its own: a destination other than its source's
folder, since a cluster archiving into the folder it is recovered from would
overwrite it.

### Check that it works

```sh
kubectl -n shop get cluster orders-restored -o jsonpath='{.spec.bootstrap.recovery}{"\n"}{.spec.externalClusters}'
kubectl -n shop wait --for=condition=Ready cluster/orders-restored --timeout=10m
```

Then query the data you expect, and the data you expect to be absent.

## When it fails

- **At build.** What CloudNativePG's webhook refuses without looking at a
  cluster is refused by `kurel build`, by its path: a credentials block other
  than exactly one way (both keys, or `inheritFromIAMRole: true`), a
  `retentionPolicy` or `schedule` that does not parse, two recovery targets, a
  `time` or `lsn` that does not parse, `xid`, `name` or `immediate` without
  `backupID`, `recovery` with `bootstrap.pg_basebackup`. The removed shapes
  (top-level `objectStore`, `bootstrap.recovery`, an external cluster's
  `barmanObjectStore`, the old `backup` keys) are refused naming where each
  went. The components README lists them all.
- **Archiving into the source.** A cluster that would archive its WAL into the
  folder it is recovered from is refused at build where launcher sees both
  stores: inline stores, the component's own backup store, and a store another
  component of the document emits (a `postgresql` component's backup store or
  inline recovery store, or a `cnpg-objectstore`). Another component's store is
  known by the name its author wrote, else, where no naming hook is set, by its
  default name: the component's name, or `<component>-recovery-source` for an
  inline recovery store (go-kure/launcher#1044). A store outside the document,
  and another component's store whose name the author did not write while a
  naming hook is set, are left to the plugin, which checks before it first
  archives and refuses a destination that already holds WAL.
- **At apply.** A missing CRD fails it. An older CRD can drop fields silently:
  read the objects back.
- **At run time.** A wrong endpoint, CA or credential shows as
  `ContinuousArchiving=False` (`ContinuousArchivingFailing`),
  `LastBackupSucceeded=False` and Backups in phase `failed`, with the cause in
  the `plugin-barman-cloud` container's log of the instance pod (for a missing
  CA: `SSL validation failed … CERTIFICATE_VERIFY_FAILED`). The Cluster is
  `Ready` all the same. A recovery runs as a job before the new Cluster's first
  instance starts (`orders-restored-1-full-recovery`); until it succeeds the
  Cluster is not ready, and the job's log has the cause.

## Verification

The steps below were run on 2026-10-26 against a disposable kind cluster built
like the Barman Cloud plugin's own end-to-end environment: Kubernetes 1.37.0,
cert-manager 1.21.2, CloudNativePG 1.30.1, the plugin 0.15.1, and the plugin
tests' S3-compatible server behind TLS from a private CA, so `endpointCA` was
used. Every object was `kurel build` output. The record, with the commands and
the observed results, is on go-kure/launcher#1001.

1. Build and apply the source with a schedule. Wait for its immediate Backup
   to reach `completed` and note its backup ID.
2. Commit marker rows. Note the time, then run
   `SELECT pg_create_restore_point('<name>')`.
3. Commit later rows (a time target needs a transaction after it), run
   `SELECT pg_switch_wal()`, and check that the segment reached the bucket:
   `pg_stat_archiver.last_archived_wal` in the source, and the object under
   `<destinationPath>/<serverName>/wals/`.
4. Add a component recovering to the noted time. Once it is Ready, the marker
   rows are there and the later rows are not.
5. Repeat with `name` and `backupID`; same result.

The source kept all its rows, and the applied Clusters and ObjectStore read
back as built.

## What this does not cover

Volume-snapshot backups; creating buckets or credentials; Azure and Google
Cloud Storage, which the `cnpg-objectstore` component still takes; replica
clusters; recovery into an existing Cluster; and deleting backups other than by
`retentionPolicy` or owner references.
