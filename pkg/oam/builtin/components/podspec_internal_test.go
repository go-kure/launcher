package components

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestPodSpecSchemaMatchesParser pins schemaPodSpec's key set to
// podSpecPropertyKeys: every key the parser reads is published, nothing the
// parser ignores is published, and no rejected key is.
func TestPodSpecSchemaMatchesParser(t *testing.T) {
	keys := make([]string, 0, len(podSpecPropertyKeys))
	for k := range schemaPodSpec(false) {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := slices.Clone(podSpecPropertyKeys)
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Fatalf("schemaPodSpec(false) keys = %v\nwant %v", keys, want)
	}
	for k := range podSpecRejectedKeys {
		if _, ok := schemaPodSpec(false)[k]; ok {
			t.Errorf("schemaPodSpec publishes rejected key %q", k)
		}
	}
}

// TestPodSpecSchema_EveryKeyDescribed walks the schema recursively: every
// property, nested property and array item carries a Description.
func TestPodSpecSchema_EveryKeyDescribed(t *testing.T) {
	var walk func(path string, s oam.PropertySchema)
	walk = func(path string, s oam.PropertySchema) {
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("%s: missing Description", path)
		}
		for k, sub := range s.Properties {
			walk(path+"."+k, sub)
		}
		if s.Items != nil {
			walk(path+"[]", *s.Items)
		}
	}
	for k, s := range schemaPodSpec(false) {
		walk(k, s)
	}
}

// TestPodSpecSchema_ReservedFlagPropagates checks the platform-reserved
// variant marks every top-level key, so a reserved-composition caller cannot
// leak an authorable key by accident.
func TestPodSpecSchema_ReservedFlagPropagates(t *testing.T) {
	for k, s := range schemaPodSpec(true) {
		if !s.PlatformReserved {
			t.Errorf("schemaPodSpec(true)[%q].PlatformReserved = false", k)
		}
	}
	for k, s := range schemaPodSpec(false) {
		if s.PlatformReserved {
			t.Errorf("schemaPodSpec(false)[%q].PlatformReserved = true", k)
		}
	}
}

// TestPodSpecSchema_NoCollisionWithHandlerKeys guards the maps.Copy in each
// handler's PropertySchema: a pod-level key that also exists among a
// handler's own keys would silently overwrite it, so the merged size must be
// exactly own + pod-level + kind-level.
func TestPodSpecSchema_NoCollisionWithHandlerKeys(t *testing.T) {
	cases := []struct {
		name    string
		schema  map[string]oam.PropertySchema
		ownKeys int
		// specKeys is the handler's kind-level fragment (schemaDeploymentSpec
		// and friends), merged by the same maps.Copy. Held as the parser's own
		// key list rather than a literal so adding a kind-level property does
		// not require editing a magic number here — which would turn this
		// collision guard into a number the author reconciles by bumping.
		specKeys []string
	}{
		// 20 and 18, not 17 and 16: go-kure/launcher#787 added the object name
		// properties as own keys (deploymentObjectName and
		// serviceAccountObjectName on both, serviceObjectName on the
		// webservice). None is in a shared list, so the merged count rises by
		// as many. 21 and 19 since go-kure/launcher#1216 gave both `podMetadata`,
		// in no shared list. 23 and 21 since go-kure/launcher#1473 gave both
		// `tolerations` and `topologySpreadConstraints`, in neither shared list.
		{"webservice", WebserviceRule{}.PropertySchema(), 23, deploymentSpecPropertyKeys},
		{"worker", WorkerRule{}.PropertySchema(), 21, deploymentSpecPropertyKeys},
		// The reshaped deployment parser is the role member, which keeps this
		// count (go-kure/launcher#1629). The kind-named `deployment`, `daemonset`,
		// `statefulset`, `job` and `cronjob` take the upstream spec and are not
		// merged from these fragments.
		// 17, not 14: go-kure/launcher#412 added affinity, tolerations and
		// topologySpreadConstraints as own keys on this kind. Verified a real
		// own-key increase and not the collision this guard exists to catch —
		// none of the three appears in podSpecPropertyKeys or
		// deploymentSpecPropertyKeys, which is exactly why the merged count
		// rises by three rather than staying flat. 18 since go-kure/launcher#280
		// added the main container's `ports`, likewise in neither shared list.
		{roleDeploymentMember, (&RoleDeploymentHandler{}).PropertySchema(), 19, deploymentSpecPropertyKeys},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			podKeys := len(podSpecPropertyKeys)
			// containerFieldKeys is the fourth fragment every kind copies in
			// (schemaContainerFields, go-kure/launcher#790), held as the
			// parser's list for the same reason specKeys is.
			if got, want := len(tc.schema), tc.ownKeys+podKeys+len(tc.specKeys)+len(containerFieldKeys); got != want {
				t.Fatalf("PropertySchema() has %d keys, want %d (%d own + %d pod-level + %d kind-level + %d container fields); a smaller count means a key collision", got, want, tc.ownKeys, podKeys, len(tc.specKeys), len(containerFieldKeys))
			}
			for _, k := range containerFieldKeys {
				if _, ok := tc.schema[k]; !ok {
					t.Errorf("PropertySchema() lacks container field %q", k)
				}
			}
			for _, k := range podSpecPropertyKeys {
				if _, ok := tc.schema[k]; !ok {
					t.Errorf("PropertySchema() lacks pod-level key %q", k)
				}
			}
			for _, k := range tc.specKeys {
				if _, ok := tc.schema[k]; !ok {
					t.Errorf("PropertySchema() lacks kind-level key %q", k)
				}
			}
		})
	}
}

func i64(v int64) *int64 { return &v }
func boolp(v bool) *bool { return &v }

// TestParsePodSpec_RoundTrip authors every accepted pod-level key once and
// checks the corev1.PodSpec fields it lands in.
func TestParsePodSpec_RoundTrip(t *testing.T) {
	props := map[string]any{
		"terminationGracePeriodSeconds": 45,
		"dnsPolicy":                     "None",
		"nodeSelector":                  map[string]any{"kubernetes.io/os": "linux", "tier": "batch"},
		"serviceAccountName":            "custom-sa",
		"automountServiceAccountToken":  true,
		// nodeName is checked separately below: admission forbids it together
		// with schedulingGates, which this fixture also authors.
		"hostNetwork":           false,
		"hostPID":               true,
		"hostIPC":               true,
		"shareProcessNamespace": false,
		"podSecurityContext": map[string]any{
			"runAsUser":                1000,
			"runAsGroup":               3000,
			"runAsNonRoot":             true,
			"fsGroup":                  2000,
			"fsGroupChangePolicy":      "OnRootMismatch",
			"supplementalGroups":       []any{4000, 5000},
			"supplementalGroupsPolicy": "Strict",
			"seccompProfile":           map[string]any{"type": "RuntimeDefault"},
			"seLinuxChangePolicy":      "Recursive",
			"sysctls":                  []any{map[string]any{"name": "net.core.somaxconn", "value": "1024"}},
		},
		"imagePullSecrets": []any{map[string]any{"name": "regcred"}},
		"hostname":         "web-0",
		"subdomain":        "web",
		"schedulerName":    "custom-scheduler",
		"hostAliases": []any{
			map[string]any{"ip": "10.0.0.5", "hostnames": []any{"db.local", "cache.local"}},
		},
		"priorityClassName": "high-priority",
		"dnsConfig": map[string]any{
			"nameservers": []any{"10.96.0.10"},
			"searches":    []any{"svc.cluster.local"},
			"options":     []any{map[string]any{"name": "ndots", "value": "2"}, map[string]any{"name": "edns0"}},
		},
		"readinessGates":     []any{map[string]any{"conditionType": "example.com/ready"}},
		"runtimeClassName":   "gvisor",
		"enableServiceLinks": false,
		"preemptionPolicy":   "Never",
		"setHostnameAsFQDN":  false,
		"os":                 map[string]any{"name": "linux"},
		// hostUsers must be true here: the fixture also authors hostPID/hostIPC,
		// and validateHostUsers forbids those when the host user namespace is
		// disabled. The false case is pinned separately below.
		"hostUsers":       true,
		"schedulingGates": []any{map[string]any{"name": "example.com/quota"}},
		"resourceClaims": []any{
			map[string]any{"name": "gpu", "resourceClaimName": "shared-gpu"},
			map[string]any{"name": "scratch", "resourceClaimTemplateName": "scratch-template"},
		},
		"podResources": map[string]any{
			"requests": map[string]any{"cpu": "500m", "memory": "1Gi"},
			"limits":   map[string]any{"memory": "2Gi", "hugepages-2Mi": "100Mi"},
		},
		"hostnameOverride": "override.example",
		"schedulingGroup":  map[string]any{"podGroupName": "batch-group"},
	}
	for _, k := range podSpecPropertyKeys {
		if _, ok := props[k]; !ok && k != "nodeName" {
			t.Fatalf("round-trip fixture lacks key %q", k)
		}
	}
	if pinned, err := parsePodSpec(map[string]any{"nodeName": "node-a"}); err != nil || pinned.NodeName != "node-a" {
		t.Fatalf("nodeName round-trip = %q, %v; want node-a", pinned.NodeName, err)
	}
	// hostUsers: false round-trips on its own — it is only the combination with
	// hostPID/hostIPC that validateHostUsers forbids. hostNetwork stays
	// authorable alongside it: upstream forbids that pair only on a cluster
	// without user-namespace host-network support.
	if pinned, err := parsePodSpec(map[string]any{"hostUsers": false, "hostNetwork": true}); err != nil ||
		pinned.HostUsers == nil || *pinned.HostUsers || !pinned.HostNetwork {
		t.Fatalf("hostUsers round-trip = %v/%v, %v; want false/true", pinned.HostUsers, pinned.HostNetwork, err)
	}

	cfg, err := parsePodSpec(props)
	if err != nil {
		t.Fatalf("parsePodSpec: %v", err)
	}
	ps := cfg.PodSpec

	check := func(name string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	check("TerminationGracePeriodSeconds", ps.TerminationGracePeriodSeconds, i64(45))
	check("DNSPolicy", ps.DNSPolicy, corev1.DNSNone)
	check("NodeSelector", ps.NodeSelector, map[string]string{"kubernetes.io/os": "linux", "tier": "batch"})
	check("ServiceAccountName", ps.ServiceAccountName, "custom-sa")
	check("AutomountServiceAccountToken", ps.AutomountServiceAccountToken, boolp(true))
	check("HostNetwork", ps.HostNetwork, false)
	check("HostPID", ps.HostPID, true)
	check("HostIPC", ps.HostIPC, true)
	check("ShareProcessNamespace", ps.ShareProcessNamespace, boolp(false))
	if ps.SecurityContext == nil {
		t.Fatal("SecurityContext = nil")
	}
	check("SecurityContext.RunAsUser", ps.SecurityContext.RunAsUser, i64(1000))
	check("SecurityContext.RunAsGroup", ps.SecurityContext.RunAsGroup, i64(3000))
	check("SecurityContext.RunAsNonRoot", ps.SecurityContext.RunAsNonRoot, boolp(true))
	check("SecurityContext.FSGroup", ps.SecurityContext.FSGroup, i64(2000))
	check("SecurityContext.SupplementalGroups", ps.SecurityContext.SupplementalGroups, []int64{4000, 5000})
	if got := ps.SecurityContext.FSGroupChangePolicy; got == nil || *got != corev1.FSGroupChangeOnRootMismatch {
		t.Errorf("SecurityContext.FSGroupChangePolicy = %v, want OnRootMismatch", got)
	}
	if got := ps.SecurityContext.SupplementalGroupsPolicy; got == nil || *got != corev1.SupplementalGroupsPolicyStrict {
		t.Errorf("SecurityContext.SupplementalGroupsPolicy = %v, want Strict", got)
	}
	if got := ps.SecurityContext.SELinuxChangePolicy; got == nil || *got != corev1.SELinuxChangePolicyRecursive {
		t.Errorf("SecurityContext.SELinuxChangePolicy = %v, want Recursive", got)
	}
	if got := ps.SecurityContext.SeccompProfile; got == nil || got.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("SecurityContext.SeccompProfile = %v, want RuntimeDefault", got)
	}
	check("SecurityContext.Sysctls", ps.SecurityContext.Sysctls, []corev1.Sysctl{{Name: "net.core.somaxconn", Value: "1024"}})
	check("ImagePullSecrets", ps.ImagePullSecrets, []corev1.LocalObjectReference{{Name: "regcred"}})
	check("Hostname", ps.Hostname, "web-0")
	check("Subdomain", ps.Subdomain, "web")
	check("SchedulerName", ps.SchedulerName, "custom-scheduler")
	check("HostAliases", ps.HostAliases, []corev1.HostAlias{{IP: "10.0.0.5", Hostnames: []string{"db.local", "cache.local"}}})
	check("PriorityClassName", ps.PriorityClassName, "high-priority")
	if ps.DNSConfig == nil {
		t.Fatal("DNSConfig = nil")
	}
	check("DNSConfig.Nameservers", ps.DNSConfig.Nameservers, []string{"10.96.0.10"})
	check("DNSConfig.Searches", ps.DNSConfig.Searches, []string{"svc.cluster.local"})
	if got := len(ps.DNSConfig.Options); got != 2 {
		t.Fatalf("DNSConfig.Options has %d entries, want 2", got)
	}
	check("DNSConfig.Options[0].Name", ps.DNSConfig.Options[0].Name, "ndots")
	if v := ps.DNSConfig.Options[0].Value; v == nil || *v != "2" {
		t.Errorf("DNSConfig.Options[0].Value = %v, want \"2\"", v)
	}
	if v := ps.DNSConfig.Options[1].Value; v != nil {
		t.Errorf("DNSConfig.Options[1].Value = %q, want nil (value-less option)", *v)
	}
	check("ReadinessGates", ps.ReadinessGates, []corev1.PodReadinessGate{{ConditionType: "example.com/ready"}})
	if ps.RuntimeClassName == nil || *ps.RuntimeClassName != "gvisor" {
		t.Errorf("RuntimeClassName = %v, want gvisor", ps.RuntimeClassName)
	}
	check("EnableServiceLinks", ps.EnableServiceLinks, boolp(false))
	if ps.PreemptionPolicy == nil || *ps.PreemptionPolicy != corev1.PreemptNever {
		t.Errorf("PreemptionPolicy = %v, want Never", ps.PreemptionPolicy)
	}
	check("SetHostnameAsFQDN", ps.SetHostnameAsFQDN, boolp(false))
	check("OS", ps.OS, &corev1.PodOS{Name: corev1.Linux})
	check("HostUsers", ps.HostUsers, boolp(true))
	check("SchedulingGates", ps.SchedulingGates, []corev1.PodSchedulingGate{{Name: "example.com/quota"}})
	if got := len(ps.ResourceClaims); got != 2 {
		t.Fatalf("ResourceClaims has %d entries, want 2", got)
	}
	check("ResourceClaims[0].Name", ps.ResourceClaims[0].Name, "gpu")
	if v := ps.ResourceClaims[0].ResourceClaimName; v == nil || *v != "shared-gpu" {
		t.Errorf("ResourceClaims[0].ResourceClaimName = %v, want shared-gpu", v)
	}
	if v := ps.ResourceClaims[1].ResourceClaimTemplateName; v == nil || *v != "scratch-template" {
		t.Errorf("ResourceClaims[1].ResourceClaimTemplateName = %v, want scratch-template", v)
	}
	if ps.Resources == nil {
		t.Fatal("Resources = nil")
	}
	if got := ps.Resources.Requests[corev1.ResourceCPU]; got.String() != "500m" {
		t.Errorf("Resources.Requests[cpu] = %s, want 500m", got.String())
	}
	if got := ps.Resources.Limits["hugepages-2Mi"]; got.String() != "100Mi" {
		t.Errorf("Resources.Limits[hugepages-2Mi] = %s, want 100Mi", got.String())
	}
	if ps.HostnameOverride == nil || *ps.HostnameOverride != "override.example" {
		t.Errorf("HostnameOverride = %v, want override.example", ps.HostnameOverride)
	}
	if ps.SchedulingGroup == nil || ps.SchedulingGroup.PodGroupName == nil || *ps.SchedulingGroup.PodGroupName != "batch-group" {
		t.Errorf("SchedulingGroup = %#v, want podGroupName batch-group", ps.SchedulingGroup)
	}
}

// TestParsePodSpec_Empty: no pod-level keys leaves the zero PodSpec, so the
// handlers' goldens stay byte-identical for documents that author none.
func TestParsePodSpec_Empty(t *testing.T) {
	cfg, err := parsePodSpec(map[string]any{"image": "ghcr.io/org/app:v1"})
	if err != nil {
		t.Fatalf("parsePodSpec: %v", err)
	}
	if !reflect.DeepEqual(cfg.PodSpec, corev1.PodSpec{}) {
		t.Errorf("parsePodSpec with no pod keys = %#v, want zero PodSpec", cfg.PodSpec)
	}
}

func TestParsePodSpec_Errors(t *testing.T) {
	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"ephemeralContainers rejected", map[string]any{"ephemeralContainers": []any{}}, "ephemeralContainers: not supported"},
		// The reason is pinned too: admission refuses only a value that differs
		// from the derived one, not every authored value (go-kure/launcher#658).
		{"priority rejected", map[string]any{"priority": 1000}, "priority: not authorable — the Priority admission controller (enabled by default) derives it from priorityClassName and rejects a pod whose value differs"},
		{"overhead rejected", map[string]any{"overhead": map[string]any{}}, "overhead: not authorable — the RuntimeClass admission controller (enabled by default) derives it from the RuntimeClass and rejects a pod whose value differs"},
		{"serviceAccount alias rejected", map[string]any{"serviceAccount": "x"}, "serviceAccount: deprecated alias"},
		// The key is refused whatever its value, so no range is read.
		{"podActiveDeadlineSeconds refused", map[string]any{"podActiveDeadlineSeconds": 10}, "only Job pods may set activeDeadlineSeconds"},
		{"podActiveDeadlineSeconds zero refused", map[string]any{"podActiveDeadlineSeconds": 0}, "only Job pods may set activeDeadlineSeconds"},
		{"terminationGracePeriodSeconds negative", map[string]any{"terminationGracePeriodSeconds": -1}, "must not be negative"},
		{"terminationGracePeriodSeconds not integer", map[string]any{"terminationGracePeriodSeconds": "30"}, "terminationGracePeriodSeconds"},
		{"dnsPolicy enum", map[string]any{"dnsPolicy": "Sometimes"}, "dnsPolicy: invalid value"},
		{"dnsPolicy None without nameservers", map[string]any{"dnsPolicy": "None"}, "None requires dnsConfig.nameservers"},
		{"dnsPolicy None with empty nameservers", map[string]any{"dnsPolicy": "None", "dnsConfig": map[string]any{"searches": []any{"a.b"}}}, "None requires dnsConfig.nameservers"},
		{"dnsConfig too many nameservers", map[string]any{"dnsConfig": map[string]any{"nameservers": []any{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"}}}, "at most 3 nameservers"},
		{"dnsConfig bad nameserver", map[string]any{"dnsConfig": map[string]any{"nameservers": []any{"not-an-ip"}}}, "invalid IP address"},
		{"dnsConfig unknown key", map[string]any{"dnsConfig": map[string]any{"servers": []any{}}}, `dnsConfig: unrecognized key "servers"`},
		{"nodeSelector bad value", map[string]any{"nodeSelector": map[string]any{"tier": "has space"}}, "invalid label value"},
		{"nodeSelector non-string value", map[string]any{"nodeSelector": map[string]any{"tier": 1}}, "must be a string"},
		{"serviceAccountName invalid", map[string]any{"serviceAccountName": "Not_Valid"}, "serviceAccountName: invalid name"},
		{"hostNetwork not bool", map[string]any{"hostNetwork": "yes"}, "hostNetwork: must be a boolean"},
		{"shareProcessNamespace with hostPID", map[string]any{"shareProcessNamespace": true, "hostPID": true}, "shareProcessNamespace and hostPID cannot both be true"},
		{"podSecurityContext unknown key", map[string]any{"podSecurityContext": map[string]any{"privileged": true}}, `podSecurityContext: unrecognized key "privileged"`},
		{"hostPID with hostUsers false", map[string]any{"hostUsers": false, "hostPID": true}, "hostPID: must not be true when hostUsers is false"},
		{"hostIPC with hostUsers false", map[string]any{"hostUsers": false, "hostIPC": true}, "hostIPC: must not be true when hostUsers is false"},
		{
			"hostProcess without hostNetwork",
			map[string]any{"podSecurityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}},
			"hostNetwork must be true when hostProcess is true",
		},
		{
			"hostAlias zone-scoped IPv6",
			map[string]any{"hostAliases": []any{map[string]any{"ip": "fe80::1%eth0", "hostnames": []any{"db.local"}}}},
			"zone-scoped address is not accepted",
		},
		{
			"nameserver zone-scoped IPv6",
			map[string]any{"dnsConfig": map[string]any{"nameservers": []any{"fe80::1%eth0"}}},
			"zone-scoped address is not accepted",
		},
		{"podSecurityContext negative fsGroup", map[string]any{"podSecurityContext": map[string]any{"fsGroup": -1}}, "must not be negative"},
		{"imagePullSecrets missing name", map[string]any{"imagePullSecrets": []any{map[string]any{}}}, "imagePullSecrets[0].name: required"},
		{"imagePullSecrets not array", map[string]any{"imagePullSecrets": "regcred"}, "must be an array"},
		{"hostname not a label", map[string]any{"hostname": "web.0"}, "hostname: invalid value"},
		{"subdomain not a label", map[string]any{"subdomain": "Web"}, "subdomain: invalid value"},
		{"hostAliases missing ip", map[string]any{"hostAliases": []any{map[string]any{"hostnames": []any{"a"}}}}, "hostAliases[0].ip: required"},
		{"hostAliases bad ip", map[string]any{"hostAliases": []any{map[string]any{"ip": "300.1.1.1", "hostnames": []any{"a"}}}}, "invalid IP address"},
		{"hostAliases no hostnames", map[string]any{"hostAliases": []any{map[string]any{"ip": "10.0.0.1"}}}, "at least one hostname"},
		{"priorityClassName invalid", map[string]any{"priorityClassName": "High"}, "priorityClassName: invalid name"},
		{"readinessGates unknown key", map[string]any{"readinessGates": []any{map[string]any{"type": "x"}}}, `readinessGates[0]: unrecognized key "type"`},
		{"readinessGates bad conditionType", map[string]any{"readinessGates": []any{map[string]any{"conditionType": "bad value"}}}, "conditionType: invalid value"},
		{"runtimeClassName invalid", map[string]any{"runtimeClassName": "gVisor"}, "runtimeClassName: invalid name"},
		{"preemptionPolicy enum", map[string]any{"preemptionPolicy": "Always"}, "preemptionPolicy: invalid value"},
		{"os enum", map[string]any{"os": map[string]any{"name": "darwin"}}, "os.name: invalid value"},
		{"os unknown key", map[string]any{"os": map[string]any{"name": "linux", "version": "6"}}, `os: unrecognized key "version"`},
		{"os name missing", map[string]any{"os": map[string]any{}}, "os.name: required"},
		{"os name not string", map[string]any{"os": map[string]any{"name": 123}}, "os.name: must be a string"},
		{"nodeName with schedulingGates", map[string]any{"nodeName": "n1", "schedulingGates": []any{map[string]any{"name": "g"}}}, "nodeName: cannot be set together with schedulingGates"},
		{"several rejected keys report the first alphabetically", map[string]any{"serviceAccount": "x", "priority": 1, "overhead": map[string]any{}}, "overhead: not authorable"},
		{"schedulingGates duplicate", map[string]any{"schedulingGates": []any{map[string]any{"name": "g"}, map[string]any{"name": "g"}}}, "duplicate scheduling gate"},
		{"resourceClaims duplicate", map[string]any{"resourceClaims": []any{map[string]any{"name": "c", "resourceClaimName": "x"}, map[string]any{"name": "c", "resourceClaimName": "y"}}}, "duplicate resource claim"},
		{"resourceClaims neither source", map[string]any{"resourceClaims": []any{map[string]any{"name": "c"}}}, "exactly one of resourceClaimName or resourceClaimTemplateName"},
		{"resourceClaims both sources", map[string]any{"resourceClaims": []any{map[string]any{"name": "c", "resourceClaimName": "x", "resourceClaimTemplateName": "y"}}}, "exactly one of resourceClaimName or resourceClaimTemplateName"},
		{"podResources ephemeral-storage", map[string]any{"podResources": map[string]any{"requests": map[string]any{"ephemeral-storage": "1Gi"}}}, "pod-level resources support only cpu, memory, and hugepages"},
		{"podResources extended resource", map[string]any{"podResources": map[string]any{"limits": map[string]any{"nvidia.com/gpu": "1"}}}, "pod-level resources support only cpu, memory, and hugepages"},
		{"podResources hugepages without cpu or memory", map[string]any{"podResources": map[string]any{"limits": map[string]any{"hugepages-2Mi": "4Mi"}}}, "podResources: hugepages require cpu or memory"},
		{"podResources bad quantity", map[string]any{"podResources": map[string]any{"limits": map[string]any{"cpu": "lots"}}}, "invalid podResources configuration"},
		{"podResources unknown key", map[string]any{"podResources": map[string]any{"requestz": map[string]any{"cpu": "1"}}}, `podResources: unrecognized key "requestz"`},
		{"podResources requests not object", map[string]any{"podResources": map[string]any{"requests": "1"}}, "podResources.requests: must be an object"},
		{"os windows with hostPID", map[string]any{"os": map[string]any{"name": "windows"}, "hostPID": true}, "os.name windows: hostPID must be unset"},
		{"os windows with hostUsers false", map[string]any{"os": map[string]any{"name": "windows"}, "hostUsers": false}, "os.name windows: hostUsers must be unset"},
		{"os windows with shareProcessNamespace", map[string]any{"os": map[string]any{"name": "windows"}, "shareProcessNamespace": false}, "os.name windows: shareProcessNamespace must be unset"},
		{"os windows with podResources", map[string]any{"os": map[string]any{"name": "windows"}, "podResources": map[string]any{"requests": map[string]any{"cpu": "1"}}}, "os.name windows: podResources must be unset"},
		{"os windows with fsGroup", map[string]any{"os": map[string]any{"name": "windows"}, "podSecurityContext": map[string]any{"fsGroup": 1}}, "os.name windows: podSecurityContext.fsGroup must be unset"},
		{"os windows with seccompProfile", map[string]any{"os": map[string]any{"name": "windows"}, "podSecurityContext": map[string]any{"seccompProfile": map[string]any{"type": "RuntimeDefault"}}}, "os.name windows: podSecurityContext.seccompProfile must be unset"},
		{"os linux with windowsOptions", map[string]any{"os": map[string]any{"name": "linux"}, "podSecurityContext": map[string]any{"windowsOptions": map[string]any{"runAsUserName": "ContainerUser"}}}, "os.name linux: podSecurityContext.windowsOptions must be unset"},
		{"hostnameOverride with hostNetwork", map[string]any{"hostnameOverride": "h.example", "hostNetwork": true}, "cannot be set when hostNetwork is true"},
		{"hostnameOverride with setHostnameAsFQDN", map[string]any{"hostnameOverride": "h.example", "setHostnameAsFQDN": true}, "cannot be set when setHostnameAsFQDN is true"},
		{"hostnameOverride too long", map[string]any{"hostnameOverride": strings.Repeat("a", 65)}, "at most 64 characters"},
		{"hostnameOverride uppercase", map[string]any{"hostnameOverride": "Host.Example"}, "hostnameOverride: invalid value"},
		{"schedulingGroup unknown key", map[string]any{"schedulingGroup": map[string]any{"name": "g"}}, `schedulingGroup: unrecognized key "name"`},
		{"schedulingGroup missing podGroupName", map[string]any{"schedulingGroup": map[string]any{}}, "schedulingGroup.podGroupName: required"},
		{"nodeName not a DNS subdomain", map[string]any{"nodeName": "Node_1"}, `nodeName: invalid name "Node_1"`},
		{"nodeName too long", map[string]any{"nodeName": strings.Repeat("a", 254)}, "nodeName: invalid name"},
		{"dnsConfig too many searches", map[string]any{"dnsConfig": map[string]any{"searches": manySearches(33, "a")}}, "at most 32 search paths"},
		{"dnsConfig searches over the 2048-character list limit", map[string]any{"dnsConfig": map[string]any{"searches": manySearches(32, strings.Repeat("b", 60))}}, "at most 2048 characters"},
		{"sysctls invalid name", map[string]any{"podSecurityContext": map[string]any{"sysctls": []any{map[string]any{"name": "Net.Core.Somaxconn", "value": "1024"}}}}, `invalid sysctl name "Net.Core.Somaxconn"`},
		{"sysctls name too long", map[string]any{"podSecurityContext": map[string]any{"sysctls": []any{map[string]any{"name": strings.Repeat("a", 254), "value": "1"}}}}, "invalid sysctl name"},
		{"sysctls duplicate name", map[string]any{"podSecurityContext": map[string]any{"sysctls": []any{map[string]any{"name": "net.core.somaxconn", "value": "1024"}, map[string]any{"name": "net.core.somaxconn", "value": "2048"}}}}, `duplicate sysctl "net.core.somaxconn"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePodSpec(tc.props)
			if err == nil {
				t.Fatalf("parsePodSpec(%v) succeeded, want error containing %q", tc.props, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// manySearches builds n distinct, individually valid DNS-1123 subdomain search
// paths prefixed with base. With a long base the aggregate list exceeds
// MaxDNSSearchListChars (2048) while every entry stays valid on its own, which
// is the only way to reach the joined-length check.
func manySearches(n int, base string) []any {
	out := make([]any, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("%s%d.example.com", base, i))
	}
	return out
}

// TestParsePodSpec_HostnameOverrideAllowed: the exclusivity checks must not
// fire on an explicit false.
func TestParsePodSpec_HostnameOverrideAllowed(t *testing.T) {
	cfg, err := parsePodSpec(map[string]any{
		"hostnameOverride":  "h.example",
		"hostNetwork":       false,
		"setHostnameAsFQDN": false,
	})
	if err != nil {
		t.Fatalf("parsePodSpec: %v", err)
	}
	if cfg.HostnameOverride == nil || *cfg.HostnameOverride != "h.example" {
		t.Errorf("HostnameOverride = %v, want h.example", cfg.HostnameOverride)
	}
}

// TestBuildPodSpec_LayersOnAuthoredFields: the shared builder starts from the
// authored PodSpec and appends the containers/volumes the kind supplies,
// defaults ServiceAccountName only when unauthored, and leaves Affinity and
// RestartPolicy alone when the kind passes none.
func TestBuildPodSpec_LayersOnAuthoredFields(t *testing.T) {
	cfg, err := parsePodSpec(map[string]any{
		"serviceAccountName":            "custom-sa",
		"terminationGracePeriodSeconds": 5,
		"nodeSelector":                  map[string]any{"tier": "web"},
	})
	if err != nil {
		t.Fatalf("parsePodSpec: %v", err)
	}
	main, err := buildMainContainer("api", mainContainerInput{Image: "ghcr.io/org/api:v1"})
	if err != nil {
		t.Fatalf("buildMainContainer: %v", err)
	}
	ps, err := buildPodSpec(podSpecInput{
		Config:        cfg,
		MainContainer: main,
		Volumes:       []corev1.Volume{{Name: "data"}},
		Tolerations:   []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}},
	})
	if err != nil {
		t.Fatalf("buildPodSpec: %v", err)
	}
	if ps.ServiceAccountName != "custom-sa" {
		t.Errorf("ServiceAccountName = %q, want authored custom-sa", ps.ServiceAccountName)
	}
	if ps.AutomountServiceAccountToken != nil {
		t.Errorf("AutomountServiceAccountToken = %v, want unset beside an authored account", *ps.AutomountServiceAccountToken)
	}
	if ps.TerminationGracePeriodSeconds == nil || *ps.TerminationGracePeriodSeconds != 5 {
		t.Errorf("TerminationGracePeriodSeconds = %v, want 5", ps.TerminationGracePeriodSeconds)
	}
	if ps.NodeSelector["tier"] != "web" {
		t.Errorf("NodeSelector = %v, want tier=web", ps.NodeSelector)
	}
	if len(ps.Containers) != 1 || ps.Containers[0].Name != "api" {
		t.Errorf("Containers = %v, want the single main container", ps.Containers)
	}
	if len(ps.Volumes) != 1 || len(ps.Tolerations) != 1 {
		t.Errorf("Volumes/Tolerations not carried: %v / %v", ps.Volumes, ps.Tolerations)
	}
	if ps.Affinity != nil || ps.RestartPolicy != "" {
		t.Errorf("Affinity/RestartPolicy set without input: %v / %q", ps.Affinity, ps.RestartPolicy)
	}

	if _, err := buildPodSpec(podSpecInput{}); err == nil {
		t.Error("buildPodSpec without a main container succeeded, want error")
	}
}

// TestBuildPodSpec_AutomountDefault: no kind generates a ServiceAccount
// (go-kure/launcher#702), so a pod without serviceAccountName runs as the
// namespace's `default` account and keeps its token unmounted unless
// automountServiceAccountToken is authored. Beside an authored account the
// field stays as authored, unset included.
func TestBuildPodSpec_AutomountDefault(t *testing.T) {
	cases := []struct {
		name      string
		props     map[string]any
		wantSA    string
		wantMount *bool
	}{
		{"nothing authored", map[string]any{}, "", boolp(false)},
		{"automount authored true", map[string]any{"automountServiceAccountToken": true}, "", boolp(true)},
		{"automount authored false", map[string]any{"automountServiceAccountToken": false}, "", boolp(false)},
		{"account authored", map[string]any{"serviceAccountName": "custom-sa"}, "custom-sa", nil},
		{"account and automount authored", map[string]any{"serviceAccountName": "custom-sa", "automountServiceAccountToken": true}, "custom-sa", boolp(true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parsePodSpec(tc.props)
			if err != nil {
				t.Fatalf("parsePodSpec: %v", err)
			}
			main, err := buildMainContainer("api", mainContainerInput{Image: "ghcr.io/org/api:v1"})
			if err != nil {
				t.Fatalf("buildMainContainer: %v", err)
			}
			ps, err := buildPodSpec(podSpecInput{Config: cfg, MainContainer: main})
			if err != nil {
				t.Fatalf("buildPodSpec: %v", err)
			}
			if ps.ServiceAccountName != tc.wantSA {
				t.Errorf("ServiceAccountName = %q, want %q", ps.ServiceAccountName, tc.wantSA)
			}
			if !reflect.DeepEqual(ps.AutomountServiceAccountToken, tc.wantMount) {
				t.Errorf("AutomountServiceAccountToken = %v, want %v", ps.AutomountServiceAccountToken, tc.wantMount)
			}
		})
	}
}

// TestBuildPodSpec_ContainerOSFields: the container-level half of the
// PodSpec.OS contract is checked once the containers are assembled.
func TestBuildPodSpec_ContainerOSFields(t *testing.T) {
	cases := []struct {
		name    string
		os      string
		sc      *corev1.SecurityContext
		wantErr string
	}{
		{"windows with privileged", "windows", &corev1.SecurityContext{Privileged: boolp(true)}, "os.name windows: containers[0].securityContext.privileged must be unset"},
		{"windows with capabilities", "windows", &corev1.SecurityContext{Capabilities: &corev1.Capabilities{}}, "os.name windows: containers[0].securityContext.capabilities must be unset"},
		{"linux with windowsOptions", "linux", &corev1.SecurityContext{WindowsOptions: &corev1.WindowsSecurityContextOptions{}}, "os.name linux: containers[0].securityContext.windowsOptions must be unset"},
		{"windows with windowsOptions ok", "windows", &corev1.SecurityContext{WindowsOptions: &corev1.WindowsSecurityContextOptions{}}, ""},
		{"linux with privileged ok", "linux", &corev1.SecurityContext{Privileged: boolp(true)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parsePodSpec(map[string]any{"os": map[string]any{"name": tc.os}})
			if err != nil {
				t.Fatalf("parsePodSpec: %v", err)
			}
			main, err := buildMainContainer("api", mainContainerInput{Image: "ghcr.io/org/api:v1", SecurityContext: tc.sc})
			if err != nil {
				t.Fatalf("buildMainContainer: %v", err)
			}
			_, err = buildPodSpec(podSpecInput{Config: cfg, MainContainer: main})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("buildPodSpec: unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("buildPodSpec error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

type hostNSPolicy struct {
	oam.Policy
	network, pid, ipc bool
}

func (p hostNSPolicy) AllowHostNetwork() bool { return p.network }
func (p hostNSPolicy) AllowHostPID() bool     { return p.pid }
func (p hostNSPolicy) AllowHostIPC() bool     { return p.ipc }

func TestEnforceHostNamespaces(t *testing.T) {
	cases := []struct {
		name    string
		props   map[string]any
		policy  hostNSPolicy
		wantErr string
	}{
		{"nothing authored, all denied", map[string]any{}, hostNSPolicy{}, ""},
		{"hostNetwork denied", map[string]any{"hostNetwork": true}, hostNSPolicy{}, "hostNetwork is not allowed"},
		{"hostNetwork allowed", map[string]any{"hostNetwork": true}, hostNSPolicy{network: true}, ""},
		{"hostPID denied", map[string]any{"hostPID": true}, hostNSPolicy{network: true}, "hostPID is not allowed"},
		{"hostPID allowed", map[string]any{"hostPID": true}, hostNSPolicy{pid: true}, ""},
		{"hostIPC denied", map[string]any{"hostIPC": true}, hostNSPolicy{network: true, pid: true}, "hostIPC is not allowed"},
		{"hostIPC allowed", map[string]any{"hostIPC": true}, hostNSPolicy{ipc: true}, ""},
		{"explicit false never denied", map[string]any{"hostNetwork": false, "hostPID": false, "hostIPC": false}, hostNSPolicy{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parsePodSpec(tc.props)
			if err != nil {
				t.Fatalf("parsePodSpec: %v", err)
			}
			err = enforceHostNamespaces(cfg, tc.policy)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("enforceHostNamespaces: unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("enforceHostNamespaces error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}
