package components_test

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests cover the upstream fields one level down that go-kure/launcher#790
// reads: a container's securityContext.windowsOptions, on every container of
// the pod, and an init container's own restartPolicy and restartPolicyRules,
// each held to upstream's validation (validateWindowsHostProcessPod,
// validateInitContainerRestartPolicy, validateContainerRestartPolicy).

// windowsOptions is a container windowsOptions object with every field set.
func windowsOptions(hostProcess bool) map[string]any {
	return map[string]any{
		"gmsaCredentialSpecName": "webapp",
		"gmsaCredentialSpec":     "{}",
		"runAsUserName":          "ContainerUser",
		"hostProcess":            hostProcess,
	}
}

func checkWindowsOptions(t *testing.T, what string, c corev1.Container, hostProcess bool) {
	t.Helper()
	if c.SecurityContext == nil || c.SecurityContext.WindowsOptions == nil {
		t.Fatalf("%s: no securityContext.windowsOptions rendered", what)
	}
	wo := c.SecurityContext.WindowsOptions
	if wo.GMSACredentialSpecName == nil || *wo.GMSACredentialSpecName != "webapp" ||
		wo.GMSACredentialSpec == nil || *wo.GMSACredentialSpec != "{}" ||
		wo.RunAsUserName == nil || *wo.RunAsUserName != "ContainerUser" ||
		wo.HostProcess == nil || *wo.HostProcess != hostProcess {
		t.Errorf("%s: windowsOptions = %+v, want every field as authored (hostProcess %t)", what, wo, hostProcess)
	}
}

// TestWorkloadKinds_ContainerWindowsOptionsRender: windowsOptions reaches the
// main container, an init container and, where the kind has them, a sidecar.
func TestWorkloadKinds_ContainerWindowsOptionsRender(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			sc := map[string]any{"windowsOptions": windowsOptions(false)}
			extra := map[string]any{
				"securityContext": sc,
				"initContainers":  []any{map[string]any{"name": "init", "image": "ghcr.io/org/init:v1", "securityContext": sc}},
			}
			if sidecarKinds[k.name] {
				extra["sidecars"] = []any{map[string]any{"name": "proxy", "image": "ghcr.io/org/proxy:v1", "securityContext": sc}}
			}
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, withProps(k.props, extra)))
			checkWindowsOptions(t, "main", ps.Containers[0], false)
			checkWindowsOptions(t, "init", containerNamed(t, ps.InitContainers, "init"), false)
			if sidecarKinds[k.name] {
				checkWindowsOptions(t, "sidecar", containerNamed(t, ps.Containers, "proxy"), false)
			}
		})
	}
}

// TestContainerWindowsOptions_HostProcessPodAccepted: a pod whose every
// container is HostProcess and that sets hostNetwork is accepted, whether the
// pod says so, each container does, or both agree.
func TestContainerWindowsOptions_HostProcessPodAccepted(t *testing.T) {
	hp := map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}
	cases := map[string]map[string]any{
		"every container": {
			"securityContext": hp,
			"initContainers":  []any{map[string]any{"name": "init", "image": "ghcr.io/org/init:v1", "securityContext": hp}},
		},
		"pod and containers agree": {
			"podSecurityContext": hp,
			"securityContext":    hp,
			"initContainers":     []any{map[string]any{"name": "init", "image": "ghcr.io/org/init:v1"}},
		},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			props := withProps(map[string]any{"image": "ghcr.io/org/app:v1", "hostNetwork": true}, extra)
			ps := podTemplateSpec(t, generateKind(t, &components.DeploymentHandler{}, "deployment", props))
			if !ps.HostNetwork {
				t.Error("hostNetwork not rendered")
			}
		})
	}
}

// TestContainerWindowsOptions_HostProcessRefusals: each of upstream's
// validateWindowsHostProcessPod rules, reached through a container's own
// windowsOptions.
func TestContainerWindowsOptions_HostProcessRefusals(t *testing.T) {
	hp := func(v bool) map[string]any { return map[string]any{"windowsOptions": map[string]any{"hostProcess": v}} }
	init := func(sc map[string]any) []any {
		m := map[string]any{"name": "init", "image": "ghcr.io/org/init:v1"}
		if sc != nil {
			m["securityContext"] = sc
		}
		return []any{m}
	}
	cases := []struct {
		name  string
		props map[string]any
		want  []string
	}{
		{
			name:  "container differs from the pod",
			props: map[string]any{"hostNetwork": true, "podSecurityContext": hp(true), "securityContext": hp(false)},
			want:  []string{"containers[0].securityContext.windowsOptions.hostProcess", "must equal podSecurityContext.windowsOptions.hostProcess (true)"},
		},
		{
			name:  "init container differs from the pod",
			props: map[string]any{"podSecurityContext": hp(false), "initContainers": init(hp(true))},
			want:  []string{"initContainers[0].securityContext.windowsOptions.hostProcess", "(false)"},
		},
		{
			name:  "main HostProcess, init container not",
			props: map[string]any{"hostNetwork": true, "securityContext": hp(true), "initContainers": init(nil)},
			want:  []string{"containers[0] is a HostProcess container and initContainers[0] is not", "only HostProcess containers"},
		},
		{
			name:  "init HostProcess, main container explicitly not",
			props: map[string]any{"hostNetwork": true, "securityContext": hp(false), "initContainers": init(hp(true))},
			want:  []string{"initContainers[0] is a HostProcess container and containers[0] is not"},
		},
		{
			name:  "HostProcess container without hostNetwork",
			props: map[string]any{"securityContext": hp(true)},
			want:  []string{"containers[0] is a HostProcess container: hostNetwork must be true"},
		},
		{
			name:  "windowsOptions not an object",
			props: map[string]any{"securityContext": map[string]any{"windowsOptions": "yes"}},
			want:  []string{"securityContext.windowsOptions"},
		},
		{
			name:  "windowsOptions unknown key",
			props: map[string]any{"securityContext": map[string]any{"windowsOptions": map[string]any{"hostprocess": true}}},
			want:  []string{"securityContext.windowsOptions", `"hostprocess"`},
		},
		{
			name:  "hostProcess not a boolean",
			props: map[string]any{"securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": "true"}}},
			want:  []string{"securityContext.windowsOptions.hostProcess"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := withProps(map[string]any{"image": "ghcr.io/org/app:v1"}, tc.props)
			err := deploymentBuildError(props)
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
	}
}

// windowsOptionsAt places a windowsOptions object at one level: the pod's
// podSecurityContext, the main container's securityContext, or an init
// container's.
func windowsOptionsAt(level string, wo map[string]any) map[string]any {
	sc := map[string]any{"windowsOptions": wo}
	switch level {
	case "pod":
		return map[string]any{"podSecurityContext": sc}
	case "init":
		return map[string]any{"initContainers": []any{map[string]any{"name": "init", "image": "ghcr.io/org/init:v1", "securityContext": sc}}}
	default:
		return map[string]any{"securityContext": sc}
	}
}

// TestWindowsOptions_StringRules: upstream's
// validateWindowsSecurityContextOptions rules on the three strings, at the pod
// level and on a container, each refusal beside a value upstream accepts. An
// empty string is read as absent, as everywhere in this package, so upstream's
// refusal of an empty value has no case here.
func TestWindowsOptions_StringRules(t *testing.T) {
	refused := []struct {
		name string
		wo   map[string]any
		want string
	}{
		{"credential spec name not a subdomain", map[string]any{"gmsaCredentialSpecName": "INVALID_NAME"}, `gmsaCredentialSpecName: invalid name "INVALID_NAME"`},
		{"credential spec over 64 KiB", map[string]any{"gmsaCredentialSpec": strings.Repeat("a", 64*1024+1)}, "gmsaCredentialSpec: size must be under 64 KiB"},
		{"control character", map[string]any{"runAsUserName": "user\tname"}, "must not contain control characters"},
		{"two backslashes", map[string]any{"runAsUserName": `a\b\c`}, "more than one backslash"},
		{"domain too long", map[string]any{"runAsUserName": strings.Repeat("a", 256) + `\user`}, "the domain must be under 256 characters"},
		{"domain neither NetBIOS nor DNS", map[string]any{"runAsUserName": `.bad-domain-over-15\user`}, "neither the NetBIOS nor the DNS format"},
		{"empty domain", map[string]any{"runAsUserName": `\user`}, "neither the NetBIOS nor the DNS format"},
		{"empty user", map[string]any{"runAsUserName": `CONTOSO\`}, "the user must not be empty"},
		{"user too long", map[string]any{"runAsUserName": strings.Repeat("u", 105)}, "the user must be at most 104 characters"},
		{"user only periods and spaces", map[string]any{"runAsUserName": ". ."}, "only of periods or spaces"},
		{"user with a slash", map[string]any{"runAsUserName": "bad/user"}, "the user must not contain any of"},
	}
	accepted := []struct {
		name string
		wo   map[string]any
	}{
		{"credential spec name a subdomain", map[string]any{"gmsaCredentialSpecName": "webapp.gmsa"}},
		{"credential spec of 64 KiB", map[string]any{"gmsaCredentialSpec": strings.Repeat("a", 64*1024)}},
		{"plain user", map[string]any{"runAsUserName": "ContainerUser"}},
		{"NetBIOS domain", map[string]any{"runAsUserName": `NT AUTHORITY\NETWORK SERVICE`}},
		{"DNS domain", map[string]any{"runAsUserName": `contoso.example.com\svc-app`}},
		{"user of 104 characters", map[string]any{"runAsUserName": strings.Repeat("u", 104)}},
	}
	levels := map[string]string{
		"pod":  "podSecurityContext.windowsOptions.",
		"main": "securityContext.windowsOptions.",
		"init": "securityContext.windowsOptions.",
	}
	for level, prefix := range levels {
		for _, tc := range refused {
			t.Run(level+"/"+tc.name, func(t *testing.T) {
				err := deploymentBuildError(withProps(map[string]any{"image": "ghcr.io/org/app:v1"}, windowsOptionsAt(level, tc.wo)))
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				if !strings.Contains(err.Error(), prefix) || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error %q does not contain %q and %q", err.Error(), prefix, tc.want)
				}
			})
		}
		for _, tc := range accepted {
			t.Run(level+"/accepted "+tc.name, func(t *testing.T) {
				if err := deploymentBuildError(withProps(map[string]any{"image": "ghcr.io/org/app:v1"}, windowsOptionsAt(level, tc.wo))); err != nil {
					t.Fatalf("unexpected error %v", err)
				}
			})
		}
	}
}

// deploymentBuildError parses and generates a deployment and returns the first
// error. The pod-wide HostProcess rules need the assembled pod, so they are
// refused when the pod is built, not when a property is parsed.
func deploymentBuildError(props map[string]any) error {
	cfg, err := (&components.DeploymentHandler{}).ToApplicationConfig(&oam.Component{Name: "app", Type: "deployment", Properties: props}, "default")
	if err != nil {
		return err
	}
	_, err = cfg.Generate(stack.NewApplication("app", "default", cfg))
	return err
}

// TestEnforcePrivileged_ContainerHostProcess: a container's own
// windowsOptions.hostProcess is gated by AllowPrivileged, on the main
// container, an init container and a sidecar. Only the container under test
// sets it, so each case reaches its own container's check.
func TestEnforcePrivileged_ContainerHostProcess(t *testing.T) {
	hp := map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}
	cases := map[string]struct {
		extra  map[string]any
		prefix string
	}{
		"main": {extra: map[string]any{"securityContext": hp, "hostNetwork": true}},
		"init": {
			extra: map[string]any{
				"hostNetwork":    true,
				"initContainers": []any{map[string]any{"name": "init", "image": "ghcr.io/org/init:v1", "securityContext": hp}},
			},
			prefix: `initContainers[0] "init"`,
		},
		"sidecar": {
			extra: map[string]any{
				"hostNetwork": true,
				"sidecars":    []any{map[string]any{"name": "proxy", "image": "ghcr.io/org/proxy:v1", "securityContext": hp}},
			},
			prefix: `sidecars[0] "proxy"`,
		},
	}
	const want = "securityContext.windowsOptions.hostProcess is not allowed by environment policy"
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			props := withProps(map[string]any{"image": "ghcr.io/org/app:v1"}, tc.extra)
			cfg := objects2Config(t, &components.DeploymentHandler{}, "deployment", props).(oam.Enforceable)
			err := cfg.ApplyPolicy(&hostNetworkOK{&stubPolicy{}})
			if err == nil || !strings.Contains(err.Error(), want) || !strings.HasPrefix(err.Error(), tc.prefix) {
				t.Fatalf("ApplyPolicy error = %v, want prefix %q and containing %q", err, tc.prefix, want)
			}
			allowed := objects2Config(t, &components.DeploymentHandler{}, "deployment", props).(oam.Enforceable)
			if err := allowed.ApplyPolicy(&hostNetworkOK{&stubPolicy{allowPrivileged: true}}); err != nil {
				t.Fatalf("ApplyPolicy with allowPrivileged: unexpected error %v", err)
			}
		})
	}
}

// restartRule is one restart rule as authored.
func restartRule(operator string, values ...any) map[string]any {
	return map[string]any{"action": "Restart", "exitCodes": map[string]any{"operator": operator, "values": values}}
}

// TestWorkloadKinds_InitContainerRestartRender: an init container's own
// restartPolicy and restartPolicyRules reach it on every kind, and a second
// init container that authors neither is left without them.
func TestWorkloadKinds_InitContainerRestartRender(t *testing.T) {
	for _, k := range workloadKinds {
		t.Run(k.name, func(t *testing.T) {
			props := withProps(k.props, map[string]any{"initContainers": []any{
				map[string]any{
					"name": "migrate", "image": "ghcr.io/org/migrate:v1",
					"restartPolicy":      "OnFailure",
					"restartPolicyRules": []any{restartRule("In", 42, 43), restartRule("NotIn")},
				},
				map[string]any{"name": "seed", "image": "ghcr.io/org/seed:v1", "restartPolicy": "Never"},
				map[string]any{"name": "plain", "image": "ghcr.io/org/plain:v1"},
			}})
			ps := podTemplateSpec(t, generateKind(t, k.handler, k.name, props))
			migrate := containerNamed(t, ps.InitContainers, "migrate")
			if migrate.RestartPolicy == nil || *migrate.RestartPolicy != corev1.ContainerRestartPolicyOnFailure {
				t.Errorf("migrate RestartPolicy = %v, want OnFailure", migrate.RestartPolicy)
			}
			want := []corev1.ContainerRestartRule{
				{Action: corev1.ContainerRestartRuleActionRestart, ExitCodes: &corev1.ContainerRestartRuleOnExitCodes{Operator: corev1.ContainerRestartRuleOnExitCodesOpIn, Values: []int32{42, 43}}},
				{Action: corev1.ContainerRestartRuleActionRestart, ExitCodes: &corev1.ContainerRestartRuleOnExitCodes{Operator: corev1.ContainerRestartRuleOnExitCodesOpNotIn, Values: []int32{}}},
			}
			if got := fmt.Sprintf("%+v", migrate.RestartPolicyRules); got != fmt.Sprintf("%+v", want) {
				t.Errorf("migrate RestartPolicyRules = %s, want %s", got, fmt.Sprintf("%+v", want))
			}
			seed := containerNamed(t, ps.InitContainers, "seed")
			if seed.RestartPolicy == nil || *seed.RestartPolicy != corev1.ContainerRestartPolicyNever || seed.RestartPolicyRules != nil {
				t.Errorf("seed RestartPolicy = %v, rules %v; want Never and no rules", seed.RestartPolicy, seed.RestartPolicyRules)
			}
			plain := containerNamed(t, ps.InitContainers, "plain")
			if plain.RestartPolicy != nil || plain.RestartPolicyRules != nil {
				t.Errorf("plain RestartPolicy = %v, rules %v; want neither", plain.RestartPolicy, plain.RestartPolicyRules)
			}
		})
	}
}

// TestInitContainerRestart_Refusals: each upstream refusal, and the shape
// checks around it, named on the init container that carries it.
func TestInitContainerRestart_Refusals(t *testing.T) {
	tooManyRules := make([]any, 21)
	for i := range tooManyRules {
		tooManyRules[i] = restartRule("In", i+1)
	}
	tooManyValues := make([]any, 256)
	for i := range tooManyValues {
		tooManyValues[i] = i
	}
	rules := func(r ...any) map[string]any {
		return map[string]any{"restartPolicy": "Never", "restartPolicyRules": r}
	}
	cases := []struct {
		name  string
		entry map[string]any
		want  []string
	}{
		{"policy Always", map[string]any{"restartPolicy": "Always"}, []string{"restartPolicy: Always is not accepted on an init container", "author a sidecar instead"}},
		{"policy unknown", map[string]any{"restartPolicy": "Sometimes"}, []string{`restartPolicy: invalid value "Sometimes"`, "Never", "OnFailure"}},
		{"policy not a string", map[string]any{"restartPolicy": 1}, []string{"restartPolicy"}},
		{"rules without a policy", map[string]any{"restartPolicyRules": []any{restartRule("In", 1)}}, []string{"restartPolicyRules: restartPolicy is required when restart rules are used"}},
		{"rules not an array", map[string]any{"restartPolicy": "Never", "restartPolicyRules": "Restart"}, []string{"restartPolicyRules"}},
		{"too many rules", map[string]any{"restartPolicy": "Never", "restartPolicyRules": tooManyRules}, []string{"restartPolicyRules: 21 rules, Kubernetes accepts at most 20"}},
		{"rule unknown key", rules(map[string]any{"action": "Restart", "exitCodes": map[string]any{"operator": "In"}, "when": "always"}), []string{"restartPolicyRules[0]", `"when"`}},
		{"action missing", rules(map[string]any{"exitCodes": map[string]any{"operator": "In"}}), []string{"restartPolicyRules[0]: action is required"}},
		{"action RestartAllContainers", rules(map[string]any{"action": "RestartAllContainers", "exitCodes": map[string]any{"operator": "In"}}), []string{`restartPolicyRules[0].action: invalid value "RestartAllContainers"`, "Restart"}},
		{"exitCodes missing", rules(map[string]any{"action": "Restart"}), []string{"restartPolicyRules[0]: exitCodes is required"}},
		{"exitCodes not an object", rules(map[string]any{"action": "Restart", "exitCodes": []any{1}}), []string{"restartPolicyRules[0].exitCodes"}},
		{"exitCodes unknown key", rules(map[string]any{"action": "Restart", "exitCodes": map[string]any{"operator": "In", "codes": []any{1}}}), []string{"restartPolicyRules[0].exitCodes", `"codes"`}},
		{"operator missing", rules(map[string]any{"action": "Restart", "exitCodes": map[string]any{"values": []any{1}}}), []string{"restartPolicyRules[0].exitCodes: operator is required"}},
		{"operator unknown", rules(restartRule("Exists")), []string{`restartPolicyRules[0].exitCodes.operator: invalid value "Exists"`, "In", "NotIn"}},
		{"values not an array", rules(map[string]any{"action": "Restart", "exitCodes": map[string]any{"operator": "In", "values": 1}}), []string{"restartPolicyRules[0].exitCodes.values: must be an array"}},
		{"too many values", rules(restartRule("In", tooManyValues...)), []string{"restartPolicyRules[0].exitCodes.values: 256 values, Kubernetes accepts at most 255"}},
		{"value not an integer", rules(restartRule("In", "1")), []string{"restartPolicyRules[0].exitCodes.values[0]"}},
		{"value fractional", rules(restartRule("In", 1.5)), []string{"restartPolicyRules[0].exitCodes.values[0]"}},
		{"value beyond int32", rules(restartRule("In", int64(1)<<31)), []string{"restartPolicyRules[0].exitCodes.values[0]", "2147483648"}},
		{"value repeated", rules(restartRule("In", 1, 2, 1)), []string{"restartPolicyRules[0].exitCodes.values[2]: duplicate value 1"}},
		{"second rule refused", rules(restartRule("In", 1), restartRule("Exists")), []string{"restartPolicyRules[1].exitCodes.operator"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{"name": "init", "image": "ghcr.io/org/init:v1"}
			for k, v := range tc.entry {
				entry[k] = v
			}
			props := map[string]any{"image": "ghcr.io/org/app:v1", "initContainers": []any{entry}}
			_, err := (&components.DeploymentHandler{}).ToApplicationConfig(&oam.Component{Name: "app", Type: "deployment", Properties: props}, "default")
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			for _, w := range append([]string{`initContainers[0] "init"`}, tc.want...) {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
	}
}

// TestInitContainerRestart_NullIsAbsent: an explicit null for either key is
// the key unauthored, so null rules need no policy.
func TestInitContainerRestart_NullIsAbsent(t *testing.T) {
	props := map[string]any{"image": "ghcr.io/org/app:v1", "initContainers": []any{map[string]any{
		"name": "init", "image": "ghcr.io/org/init:v1", "restartPolicy": nil, "restartPolicyRules": nil,
	}}}
	ps := podTemplateSpec(t, generateKind(t, &components.DeploymentHandler{}, "deployment", props))
	if ic := ps.InitContainers[0]; ic.RestartPolicy != nil || ic.RestartPolicyRules != nil {
		t.Errorf("RestartPolicy = %v, rules %v; want neither", ic.RestartPolicy, ic.RestartPolicyRules)
	}
}

// TestInitContainerRestart_AuthoredCheckAccepts: the authored-property check
// `kurel build` runs accepts both keys on an init entry.
func TestInitContainerRestart_AuthoredCheckAccepts(t *testing.T) {
	app := authoredApp(map[string]any{"image": "ghcr.io/org/app:v1", "initContainers": []any{map[string]any{
		"name": "init", "image": "ghcr.io/org/init:v1",
		"restartPolicy": "OnFailure", "restartPolicyRules": []any{restartRule("In", 1)},
	}}})
	if err := authoredTransformer().ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("restartPolicy and restartPolicyRules on an init entry must be accepted, got: %v", err)
	}
}

// TestInitContainerRestart_RenderingTwiceIsUnaffectedByEditingTheFirstRender:
// the policy and the rules are copied into each render, so editing one leaves
// the next as authored.
func TestInitContainerRestart_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	props := map[string]any{"image": "ghcr.io/org/app:v1", "initContainers": []any{map[string]any{
		"name": "init", "image": "ghcr.io/org/init:v1",
		"restartPolicy": "OnFailure", "restartPolicyRules": []any{restartRule("In", 1)},
	}}}
	second := renderTwice(t, &components.DeploymentHandler{}, "deployment", props, func(objects []*client.Object) {
		ic := &podTemplateSpec(t, objects).InitContainers[0]
		*ic.RestartPolicy = corev1.ContainerRestartPolicyAlways
		ic.RestartPolicyRules[0].Action = "Edited"
		ic.RestartPolicyRules[0].ExitCodes.Values[0] = 99
	})
	ic := podTemplateSpec(t, second).InitContainers[0]
	if *ic.RestartPolicy != corev1.ContainerRestartPolicyOnFailure ||
		ic.RestartPolicyRules[0].Action != corev1.ContainerRestartRuleActionRestart ||
		ic.RestartPolicyRules[0].ExitCodes.Values[0] != 1 {
		t.Errorf("second render = %v / %+v, want OnFailure and the authored rule", *ic.RestartPolicy, ic.RestartPolicyRules)
	}
}
