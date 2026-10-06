package components_test

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The kind components of VolSync's API (go-kure/launcher#790):
// replicationsource and replicationdestination. What they share with the kinds
// the policy does not reach is held by the policyFreeKinds table
// (kind_policy_free_test.go), in which they are held ones; this file holds
// their fixtures and what the environment policy asks of them. What the kinds
// take from the CRDs of the linked module is held in
// volsync_kinds_internal_test.go.

// volsyncMoverKinds are the two kinds with their movers: those that size a
// volume with `capacity`, and of those the ones that share MoverConfig (a
// security context, an affinity, volumes). Syncthing, a source's only, has a
// MoverConfig and no `capacity`.
var volsyncMoverKinds = []struct {
	component string
	handler   oam.ComponentHandler
	sized     []string
	config    []string
}{
	{"replicationsource", &components.ReplicationSourceHandler{},
		[]string{"rsync", "rsyncTLS", "rclone", "restic"}, []string{"rsyncTLS", "rclone", "restic", "syncthing"}},
	{"replicationdestination", &components.ReplicationDestinationHandler{},
		[]string{"rsync", "rsyncTLS", "rclone", "restic"}, []string{"rsyncTLS", "rclone", "restic"}},
}

// moverWith is the properties of a kind that authors one field of one mover.
func moverWith(mover, field string, value any) map[string]any {
	return map[string]any{mover: map[string]any{field: value}}
}

// withoutProperty is props without one property.
func withoutProperty(props map[string]any, name string) map[string]any {
	delete(props, name)
	return props
}

// syncthingPeer is a Syncthing peer with the three fields the API requires of
// one.
func syncthingPeer() map[string]any {
	return map[string]any{
		"address": "tcp://peer.example.com:22000", "introducer": false,
		"ID": "MFZWI3D-BONSGYC-YLTMRWG-C43ENR5-QXGZDMM-FZWI3DP-BONSGYY-LTMRWAD",
	}
}

// syncthingPeers is the properties of a replicationsource whose Syncthing mover
// has the peers.
func syncthingPeers(peers ...any) map[string]any {
	return moverWith("syncthing", "peers", append([]any{}, peers...))
}

// volsyncMoverConfig is own with a value of every field of MoverConfig, inside
// ptStrictPolicy: the resources at its maxima, and hostProcess authored false,
// which the policy refuses only when it is true. The first volume is a Secret,
// and the three sources a volume may have are each there once.
func volsyncMoverConfig(own map[string]any) map[string]any {
	config := map[string]any{
		"moverSecurityContext": map[string]any{
			"runAsUser": 0, "runAsNonRoot": false, "fsGroup": 65534,
			"windowsOptions": map[string]any{"hostProcess": false},
		},
		"moverServiceAccount": "mover",
		"moverPodLabels":      map[string]any{"backup": "nightly"},
		"moverResources": map[string]any{
			"limits":   map[string]any{"cpu": "2", "memory": "1Gi"},
			"requests": map[string]any{"cpu": "100m", "memory": 67108864},
		},
		"moverAffinity": map[string]any{"nodeAffinity": map[string]any{
			"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{"nodeSelectorTerms": []any{map[string]any{
				"matchExpressions": []any{map[string]any{"key": "kubernetes.io/os", "operator": "In", "values": []any{"linux"}}},
			}}},
		}},
		"moverVolumes": []any{
			map[string]any{"mountPath": "creds", "volumeSource": map[string]any{"secret": map[string]any{
				"secretName": "mover-creds", "items": []any{map[string]any{"key": "token", "path": "token", "mode": 256}},
			}}},
			map[string]any{"mountPath": "media", "volumeSource": map[string]any{"nfs": map[string]any{"server": "nfs.example.com", "path": "/export/media"}}},
			map[string]any{"mountPath": "archive", "volumeSource": map[string]any{"persistentVolumeClaim": map[string]any{"claimName": "archive", "readOnly": true}}},
		},
	}
	maps.Copy(config, own)
	return config
}

// volsyncSourceVolume is own with a value of every volume option of a source's
// mover, the capacity at ptStrictPolicy's storage maximum.
func volsyncSourceVolume(own map[string]any) map[string]any {
	volume := map[string]any{
		"copyMethod": "Snapshot", "capacity": "10Gi", "storageClassName": "fast",
		"accessModes": []any{"ReadWriteOnce"}, "volumeSnapshotClassName": "csi-snapshots",
	}
	maps.Copy(volume, own)
	return volume
}

// volsyncDestinationVolume is volsyncSourceVolume for a destination's mover.
func volsyncDestinationVolume(own map[string]any) map[string]any {
	return volsyncSourceVolume(withProperty(withProperty(own, "destinationPVC", "restored"), "cleanupTempPVC", true))
}

// replicationSourceFull is a value of every top-level field of a
// ReplicationSource's spec, and under each mover of every field of its type.
// The operator takes one mover per object; the kind leaves that to it, and the
// fixture sets all five and the external provider. Everything the policy reads
// stays inside ptStrictPolicy.
func replicationSourceFull() map[string]any {
	customCA := map[string]any{"secretName": "backup-ca", "key": "ca.crt"}
	return map[string]any{
		"sourcePVC": "data",
		"trigger":   map[string]any{"schedule": "0 3 * * *", "manual": "before-upgrade"},
		"rsync": volsyncSourceVolume(map[string]any{
			"sshKeys": "rsync-keys", "serviceType": "ClusterIP", "address": "backup.example.com", "port": 22,
			"path": "/", "sshUser": "root", "moverServiceAccount": "mover",
			"moverPodLabels": map[string]any{"backup": "nightly"},
			"moverResources": map[string]any{"limits": map[string]any{"cpu": "2", "memory": "1Gi"}},
		}),
		"rsyncTLS": volsyncSourceVolume(volsyncMoverConfig(map[string]any{
			"keySecret": "rsync-tls-key", "address": "backup.example.com", "port": 8000,
		})),
		"rclone": volsyncSourceVolume(volsyncMoverConfig(map[string]any{
			"rcloneConfigSection": "remote", "rcloneDestPath": "bucket/data", "rcloneConfig": "rclone-config", "customCA": customCA,
		})),
		"restic": volsyncSourceVolume(volsyncMoverConfig(map[string]any{
			"pruneIntervalDays": 14, "repository": "restic-repo", "customCA": customCA,
			"retain":        map[string]any{"hourly": 6, "daily": 7, "weekly": 4, "monthly": 0, "yearly": 1, "within": "3d", "last": "5"},
			"cacheCapacity": "1Gi", "cacheStorageClassName": "fast", "cacheAccessModes": []any{"ReadWriteOnce"},
			"unlock": "after-crash",
		})),
		"syncthing": volsyncMoverConfig(map[string]any{
			"peers":       []any{syncthingPeer(), withProperty(withProperty(syncthingPeer(), "address", "tcp://other.example.com:22000"), "introducer", true)},
			"serviceType": "ClusterIP", "configCapacity": "1Gi", "configStorageClassName": "fast",
			"configAccessModes": []any{"ReadWriteOnce"},
		}),
		"external": map[string]any{"provider": "example.com/replicator", "parameters": map[string]any{"mode": "async"}},
		"paused":   true,
	}
}

// replicationDestinationFull is replicationSourceFull for a
// ReplicationDestination, which has no Syncthing mover and names no source
// claim.
func replicationDestinationFull() map[string]any {
	customCA := map[string]any{"configMapName": "backup-ca", "key": "ca.crt"}
	annotations := map[string]any{"example.com/pool": "backup"}
	return map[string]any{
		"trigger": map[string]any{"schedule": "30 3 * * *", "manual": "restore-1"},
		"rsync": volsyncDestinationVolume(map[string]any{
			"volumeMode": "Filesystem", "sshKeys": "rsync-keys", "serviceType": "LoadBalancer",
			"serviceAnnotations": annotations, "address": "backup.example.com", "port": 22, "path": "/", "sshUser": "root",
			"moverServiceAccount": "mover", "moverPodLabels": map[string]any{"backup": "nightly"},
			"moverResources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "64Mi"}},
		}),
		"rsyncTLS": volsyncDestinationVolume(volsyncMoverConfig(map[string]any{
			"volumeMode": "Block", "keySecret": "rsync-tls-key", "serviceType": "ClusterIP", "serviceAnnotations": annotations,
		})),
		"rclone": volsyncDestinationVolume(volsyncMoverConfig(map[string]any{
			"rcloneConfigSection": "remote", "rcloneDestPath": "bucket/data", "rcloneConfig": "rclone-config", "customCA": customCA,
		})),
		"restic": volsyncDestinationVolume(volsyncMoverConfig(map[string]any{
			"repository": "restic-repo", "customCA": customCA,
			"cacheCapacity": "1Gi", "cacheStorageClassName": "fast", "cacheAccessModes": []any{"ReadWriteOnce"},
			"cleanupCachePVC": true, "previous": 0, "restoreAsOf": "2026-01-02T03:04:05Z", "enableFileDeletion": true,
		})),
		"external": map[string]any{"provider": "example.com/replicator", "parameters": map[string]any{"mode": "async"}},
		"paused":   true,
	}
}

// TestVolsyncKinds_Policy: what an author writes of a mover that the
// environment policy holds, on both kinds and on each mover that has the
// field: a capacity, which sizes a volume the operator provisions, to the
// storage maximum; the cpu and memory of moverResources, as a limit or as a
// request, to the maxima; and the pod-level hostProcess switch, refused unless
// the policy allows privileged workloads. Each violation names the component
// and the field. Inside the maxima, with no maximum set and with no policy
// given a capacity and the resources build, and a capacity left out is the
// operator's to choose. hostProcess is refused with no policy given too.
//
// And what an author chooses with the rsync-over-SSH mover: the capabilities
// the linked operator version adds to its container are held to the policy's
// container-capability lists, whatever the mover authors, and to nothing where
// the policy sets none or none is given. No other mover is held to them.
func TestVolsyncKinds_Policy(t *testing.T) {
	type policyCase struct {
		props  map[string]any
		policy oam.Policy
		want   string // "" when the component builds
	}
	hostProcess := func(mover string, value bool) map[string]any {
		return moverWith(mover, "moverSecurityContext", map[string]any{"windowsOptions": map[string]any{"hostProcess": value}})
	}
	resources := func(mover, list, name, value string) map[string]any {
		return moverWith(mover, "moverResources", map[string]any{list: map[string]any{name: value}})
	}
	const refused = ".moverSecurityContext.windowsOptions.hostProcess is not allowed by environment policy"
	const rsyncContainer = "rsync: the mover's container as VolSync v0.16.0 writes it: securityContext.capabilities.add: "
	rsyncMover := moverWith("rsync", "sshKeys", "rsync-keys")
	rsyncCapabilities := []string{"AUDIT_WRITE", "CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID", "SYS_CHROOT"}
	withoutString := func(list []string, drop string) []string {
		return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == drop })
	}
	for _, kind := range volsyncMoverKinds {
		cases := map[string]policyCase{
			"restic, cache capacity": {moverWith("restic", "cacheCapacity", "11Gi"), ptStrictPolicy(),
				`restic.cacheCapacity "11Gi" exceeds enforced maximum "10Gi"`},
			"restic, capacity inside and cache capacity over": {map[string]any{"restic": map[string]any{"capacity": "1Gi", "cacheCapacity": "1Ti"}}, ptStrictPolicy(),
				`restic.cacheCapacity "1Ti" exceeds enforced maximum "10Gi"`},
			"a capacity authored as a number": {moverWith("rclone", "capacity", 21474836480), ptStrictPolicy(),
				`rclone.capacity "21474836480" exceeds enforced maximum "10Gi"`},
			"a later mover": {map[string]any{"rsync": map[string]any{"capacity": "1Gi"}, "restic": map[string]any{"capacity": "20Gi"}}, ptStrictPolicy(),
				`restic.capacity "20Gi" exceeds enforced maximum "10Gi"`},
			"capacities at the maximum":      {map[string]any{"restic": map[string]any{"capacity": "10Gi", "cacheCapacity": "10Gi"}}, ptStrictPolicy(), ""},
			"no capacity authored":           {map[string]any{"restic": map[string]any{"repository": "restic-repo"}}, ptStrictPolicy(), ""},
			"a capacity with no maximum set": {moverWith("restic", "capacity", "1Ti"), &stubPolicy{}, ""},
			"a capacity under a policy that forbids explicit secrets and sets no maximum": {
				moverWith("restic", "capacity", "1Ti"), esForbidding(), ""},
			"a capacity with no policy given": {moverWith("restic", "capacity", "1Ti"), nil, ""},
			"resources at the maxima": {moverWith("rsync", "moverResources", map[string]any{
				"limits": map[string]any{"cpu": "2", "memory": "1Gi"}, "requests": map[string]any{"cpu": "2", "memory": "1Gi"},
			}), ptStrictPolicy(), ""},
			"a request over its limit, both inside": {moverWith("restic", "moverResources", map[string]any{
				"limits": map[string]any{"cpu": "100m"}, "requests": map[string]any{"cpu": "1"},
			}), ptStrictPolicy(), ""},
			"resources with no maximum set":   {resources("restic", "limits", "cpu", "64"), &stubPolicy{}, ""},
			"resources with no policy given":  {resources("restic", "limits", "cpu", "64"), nil, ""},
			"a mover with nothing held in it": {map[string]any{"external": map[string]any{"provider": "example.com/replicator"}}, ptStrictPolicy(), ""},
			// The parameters of an external provider are the provider's: no
			// policy reads them, one that forbids explicit secrets included.
			"external parameters under a policy that forbids explicit secrets": {map[string]any{"external": map[string]any{
				"provider": "example.com/replicator", "parameters": map[string]any{"password": "not-a-reference"},
			}}, esForbidding(), ""},
			// The rsync-over-SSH mover: the seven capabilities the linked
			// operator version adds to its container are held to the policy's
			// container-capability lists, as a container that adds them is on a
			// pod kind. The first of the seven the policy does not take is named.
			"rsync, one of its capabilities forbidden": {rsyncMover, &stubPolicy{forbiddenContainerCaps: []string{"SYS_CHROOT"}},
				rsyncContainer + `"SYS_CHROOT" is forbidden by environment policy`},
			"rsync, a forbidden capability spelled as the kernel does": {rsyncMover, &stubPolicy{forbiddenContainerCaps: []string{"cap_dac_override"}},
				rsyncContainer + `"DAC_OVERRIDE" is forbidden by environment policy`},
			"rsync, every capability forbidden": {rsyncMover, &stubPolicy{forbiddenContainerCaps: []string{"ALL"}},
				rsyncContainer + `"AUDIT_WRITE" is forbidden by environment policy (forbidden list contains ALL)`},
			"rsync, an allowed list without its capabilities": {rsyncMover, &stubPolicy{allowedContainerCaps: []string{"NET_BIND_SERVICE"}},
				rsyncContainer + `"AUDIT_WRITE" is not allowed by environment policy`},
			"rsync, an allowed list that lacks one": {rsyncMover, &stubPolicy{allowedContainerCaps: withoutString(rsyncCapabilities, "SETUID")},
				rsyncContainer + `"SETUID" is not allowed by environment policy`},
			"rsync, forbidden where privileged is allowed": {rsyncMover, &stubPolicy{allowPrivileged: true, forbiddenContainerCaps: []string{"CHOWN"}},
				rsyncContainer + `"CHOWN" is forbidden by environment policy`},
			"rsync, a later mover after it": {map[string]any{"restic": map[string]any{"repository": "restic-repo"}, "rsync": map[string]any{"sshKeys": "rsync-keys"}},
				&stubPolicy{forbiddenContainerCaps: []string{"FOWNER"}}, rsyncContainer + `"FOWNER" is forbidden by environment policy`},
			"rsync, an allowed list with its capabilities": {rsyncMover, &stubPolicy{allowedContainerCaps: rsyncCapabilities}, ""},
			"rsync, another capability forbidden":          {rsyncMover, ptStrictPolicy(), ""},
			"rsync under a policy that sets nothing":       {rsyncMover, &stubPolicy{}, ""},
			"rsync with no policy given":                   {rsyncMover, nil, ""},
			"rsync authored empty, a capability forbidden": {map[string]any{"rsync": map[string]any{}}, &stubPolicy{forbiddenContainerCaps: []string{"SETGID"}},
				rsyncContainer + `"SETGID" is forbidden by environment policy`},
		}
		// The other movers are not held to those lists: the linked operator
		// version runs them with added capabilities only where the namespace's
		// administrator allows it.
		for _, mover := range append(slices.Clone(kind.config), "external") {
			cases[mover+", every capability forbidden"] = policyCase{map[string]any{mover: map[string]any{}},
				&stubPolicy{forbiddenContainerCaps: []string{"ALL"}}, ""}
			cases[mover+", an allowed list of one"] = policyCase{map[string]any{mover: map[string]any{}},
				&stubPolicy{allowedContainerCaps: []string{"NET_BIND_SERVICE"}}, ""}
		}
		for _, mover := range kind.sized {
			cases[mover+", capacity"] = policyCase{moverWith(mover, "capacity", "20Gi"), ptStrictPolicy(),
				mover + `.capacity "20Gi" exceeds enforced maximum "10Gi"`}
			cases[mover+", cpu limit"] = policyCase{resources(mover, "limits", "cpu", "4"), ptStrictPolicy(),
				mover + `.moverResources: cpu limit "4" exceeds enforced maximum "2"`}
			cases[mover+", cpu request"] = policyCase{resources(mover, "requests", "cpu", "2500m"), ptStrictPolicy(),
				mover + `.moverResources: cpu request "2500m" exceeds enforced maximum "2"`}
			cases[mover+", memory limit"] = policyCase{resources(mover, "limits", "memory", "2Gi"), ptStrictPolicy(),
				mover + `.moverResources: memory limit "2Gi" exceeds enforced maximum "1Gi"`}
			cases[mover+", memory request"] = policyCase{resources(mover, "requests", "memory", "2Gi"), ptStrictPolicy(),
				mover + `.moverResources: memory request "2Gi" exceeds enforced maximum "1Gi"`}
		}
		for _, mover := range kind.config {
			cases[mover+", hostProcess"] = policyCase{hostProcess(mover, true), ptStrictPolicy(), mover + refused}
			cases[mover+", hostProcess under a policy that sets nothing"] = policyCase{hostProcess(mover, true), &stubPolicy{}, mover + refused}
			cases[mover+", hostProcess false"] = policyCase{hostProcess(mover, false), ptStrictPolicy(), ""}
			cases[mover+", hostProcess where privileged is allowed"] = policyCase{hostProcess(mover, true), &stubPolicy{allowPrivileged: true}, ""}
			// The transform holds a document with no policy to the one that
			// allows nothing privileged, as it does a workload.
			cases[mover+", hostProcess with no policy given"] = policyCase{hostProcess(mover, true), nil, mover + refused}
			cases[mover+", a security context without the switch"] = policyCase{
				moverWith(mover, "moverSecurityContext", map[string]any{"runAsUser": 0, "windowsOptions": map[string]any{"runAsUserName": "mover"}}),
				ptStrictPolicy(), ""}
		}
		if kind.component == "replicationsource" {
			cases["syncthing, config capacity"] = policyCase{moverWith("syncthing", "configCapacity", "20Gi"), ptStrictPolicy(),
				`syncthing.configCapacity "20Gi" exceeds enforced maximum "10Gi"`}
			cases["syncthing, memory limit"] = policyCase{resources("syncthing", "limits", "memory", "2Gi"), ptStrictPolicy(),
				`syncthing.moverResources: memory limit "2Gi" exceeds enforced maximum "1Gi"`}
			cases["syncthing, config capacity at the maximum"] = policyCase{moverWith("syncthing", "configCapacity", "10Gi"), ptStrictPolicy(), ""}
		}
		for name, tc := range cases {
			t.Run(kind.component+"/"+name, func(t *testing.T) {
				objs, err := pvTransform(kind.component, kind.handler, tc.props, tc.policy)
				if tc.want != "" {
					htWantViolation(t, err, `component "web": `+tc.want)
					return
				}
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if len(objs) != 1 || objs[0].GetName() != "web" {
					t.Fatalf("generated %v, want the one object web", objs)
				}
			})
		}
	}
}

// TestVolsyncKinds_PolicyFillsNoDefault: the policy's default requests, limits
// and storage size are a workload's. A mover takes none of them: the object
// built under a policy that sets them is the one built under none, for a mover
// that authors part of its resources, one that authors an empty block and one
// that authors no capacity.
func TestVolsyncKinds_PolicyFillsNoDefault(t *testing.T) {
	defaults := &stubPolicy{
		maxCPU: "2", maxMemory: "1Gi", maxStorageSize: "10Gi",
		defaultCPURequest: "50m", defaultMemoryRequest: "32Mi", defaultCPULimit: "1", defaultMemoryLimit: "512Mi",
		defaultStorageSize: "5Gi",
	}
	for _, kind := range volsyncMoverKinds {
		t.Run(kind.component, func(t *testing.T) {
			build := func(policy oam.Policy) map[string]any {
				props := map[string]any{
					"rsync":  map[string]any{"moverResources": map[string]any{"limits": map[string]any{"cpu": "100m"}}},
					"rclone": map[string]any{"moverResources": map[string]any{}},
					"restic": map[string]any{"repository": "restic-repo"},
				}
				objs, err := pvTransform(kind.component, kind.handler, props, policy)
				if err != nil || len(objs) != 1 {
					t.Fatalf("transform: %d objects, err %v", len(objs), err)
				}
				return policyFreeJSON(t, objs[0])
			}
			got, want := build(defaults), build(nil)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("under a policy with defaults the object is %v\nwant the one built under none: %v", got, want)
			}
			const spec = "map[rclone:map[customCA:map[] moverResources:map[]] restic:map[customCA:map[] repository:restic-repo] " +
				"rsync:map[moverResources:map[limits:map[cpu:100m]]]]"
			if text := fmt.Sprint(want["spec"]); text != spec {
				t.Errorf("spec = %s\nwant   %s", text, spec)
			}
		})
	}
}

// TestVolsyncKinds_AuthoredValuesArriveTyped reads a few authored values back
// from the typed objects: lists keep their order, an authored false and 0 are
// kept where the type can carry them, and a quantity authored as a number is
// that number.
func TestVolsyncKinds_AuthoredValuesArriveTyped(t *testing.T) {
	build := func(component string, props map[string]any) any {
		for _, kind := range policyFreeKinds {
			if kind.component == component {
				return kind.generate(t, "fast", props)
			}
		}
		t.Fatalf("%s is no kind of policyFreeKinds", component)
		return nil
	}

	source := build("replicationsource", replicationSourceFull()).(*volsyncv1alpha1.ReplicationSource)
	if source.Status != nil {
		t.Errorf("status = %+v, want none", source.Status)
	}
	restic, syncthing := source.Spec.Restic, source.Spec.Syncthing
	if restic == nil || syncthing == nil || source.Spec.Rsync == nil || source.Spec.RsyncTLS == nil || source.Spec.Rclone == nil || source.Spec.External == nil {
		t.Fatalf("spec = %+v, want the five authored movers and the external provider", source.Spec)
	}
	if got := restic.Retain; got == nil || got.Monthly == nil || *got.Monthly != 0 || got.Hourly == nil || *got.Hourly != 6 {
		t.Errorf("restic.retain = %+v, want the authored hourly 6 and monthly 0", got)
	}
	if got := restic.MoverResources.Requests.Memory().String(); got != "67108864" {
		t.Errorf("restic memory request = %s, want the authored number 67108864", got)
	}
	if got := restic.MoverSecurityContext; got == nil || got.RunAsUser == nil || *got.RunAsUser != 0 ||
		got.RunAsNonRoot == nil || *got.RunAsNonRoot || got.WindowsOptions == nil || got.WindowsOptions.HostProcess == nil || *got.WindowsOptions.HostProcess {
		t.Errorf("restic.moverSecurityContext = %+v, want the authored runAsUser 0, runAsNonRoot false and hostProcess false", got)
	}
	if len(restic.MoverVolumes) != 3 || restic.MoverVolumes[0].VolumeSource.Secret == nil || restic.MoverVolumes[1].VolumeSource.NFS == nil ||
		restic.MoverVolumes[2].VolumeSource.PersistentVolumeClaim == nil {
		t.Errorf("restic.moverVolumes = %+v, want the three authored volumes in order", restic.MoverVolumes)
	}
	if len(syncthing.Peers) != 2 || syncthing.Peers[0].Introducer || !syncthing.Peers[1].Introducer ||
		syncthing.Peers[1].Address != "tcp://other.example.com:22000" {
		t.Errorf("syncthing.peers = %+v, want the two authored peers in order, the first with the authored introducer false", syncthing.Peers)
	}
	if got := source.Spec.Trigger; got == nil || got.Schedule == nil || *got.Schedule != "0 3 * * *" || got.Manual != "before-upgrade" {
		t.Errorf("trigger = %+v, want the authored schedule and manual value", got)
	}

	destination := build("replicationdestination", replicationDestinationFull()).(*volsyncv1alpha1.ReplicationDestination)
	if destination.Status != nil {
		t.Errorf("status = %+v, want none", destination.Status)
	}
	if got := destination.Spec.Restic; got == nil || got.Previous == nil || *got.Previous != 0 || !got.EnableFileDeletion {
		t.Errorf("restic = %+v, want the authored previous 0 and enableFileDeletion true", got)
	}
	if got := destination.Spec.Rsync; got == nil || got.DestinationPVC == nil || *got.DestinationPVC != "restored" ||
		got.ServiceAnnotations == nil || (*got.ServiceAnnotations)["example.com/pool"] != "backup" {
		t.Errorf("rsync = %+v, want the authored destinationPVC and serviceAnnotations", got)
	}

	// A mover authored empty is the object's mover: it is in the object, with
	// the one field its type holds by value, and the movers left out are not.
	// Neither kind requires a mover: the operator refuses an object with none
	// when it reconciles, and the kind builds what was authored.
	for _, kind := range volsyncMoverKinds {
		spec := policyFreeJSON(t, build(kind.component, map[string]any{"restic": map[string]any{}}))["spec"]
		if got, want := fmt.Sprint(spec), "map[restic:map[customCA:map[]]]"; got != want {
			t.Errorf("%s: spec = %s, want %s", kind.component, got, want)
		}
		if none := policyFreeJSON(t, build(kind.component, map[string]any{}))["spec"]; fmt.Sprint(none) != "map[]" {
			t.Errorf("%s: spec = %v, want an empty one on an object that authors no mover", kind.component, none)
		}
	}
}
