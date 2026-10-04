package components_test

import (
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// podTemplatePolicyKind is a kind component whose object holds a pod template.
// objectPath is where the pod spec sits in the object, propsPath where it sits
// in the component's properties, and props the smallest component running the
// given pod spec.
type podTemplatePolicyKind struct {
	typ, kind, apiVersion string
	handler               oam.ComponentHandler
	objectPath, propsPath string
	props                 func(podSpec string) string
}

// object is the YAML of the kind's object running podSpec, as a chart, a
// passthrough component or a manifests source would carry it.
func (k podTemplatePolicyKind) object(podSpec string) string {
	return ptWorkload(k.kind, k.apiVersion, k.objectPath, podSpec)
}

var podTemplatePolicyKinds = []podTemplatePolicyKind{
	{
		typ: "replicaset", kind: "ReplicaSet", apiVersion: "apps/v1",
		handler:    &components.ReplicaSetHandler{},
		objectPath: "spec.template.spec", propsPath: "template.spec",
		props: rsPlain,
	},
	{
		typ: "replicationcontroller", kind: "ReplicationController", apiVersion: "v1",
		handler:    &components.ReplicationControllerHandler{},
		objectPath: "spec.template.spec", propsPath: "template.spec",
		props: rcPlain,
	},
}

// podTemplatePolicyPaths are the four ways a build produces an object of a
// podTemplatePolicyKinds kind: the kind component, and the three components
// that emit an object written elsewhere. rendered is how the three name the
// object's origin in a refusal, before its kind.
var podTemplatePolicyPaths = []struct {
	name, rendered string
	build          func(t *testing.T, k podTemplatePolicyKind, spec string, policy *stubPolicy) ([]client.Object, error)
}{
	{
		name: "kind",
		build: func(t *testing.T, k podTemplatePolicyKind, spec string, policy *stubPolicy) ([]client.Object, error) {
			return pvTransform(k.typ, k.handler, ptObject(t, k.props(spec)), pvPolicy(policy))
		},
	},
	{
		name: "template delivery", rendered: "helmtemplate: rendered ",
		build: func(t *testing.T, k podTemplatePolicyKind, spec string, policy *stubPolicy) ([]client.Object, error) {
			srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"object.yaml": k.object(spec)})
			if policy != nil {
				withHost := *policy
				withHost.allowedRegistries = append(slices.Clone(policy.allowedRegistries), htServerHost(t, srvURL))
				policy = &withHost
			}
			return htTransform(srvURL, pvPolicy(policy))
		},
	},
	{
		name: "passthrough", rendered: "passthrough: object ",
		build: func(t *testing.T, k podTemplatePolicyKind, spec string, policy *stubPolicy) ([]client.Object, error) {
			return ptTransform(ptObject(t, k.object(spec)), pvPolicy(policy))
		},
	},
	{
		name: "manifests", rendered: "manifest source: object ",
		build: func(_ *testing.T, k podTemplatePolicyKind, spec string, policy *stubPolicy) ([]client.Object, error) {
			return mfTransform("manifests", mfInline(k.object(spec)), pvPolicy(policy))
		},
	},
}

// TestPodTemplateKindsPolicy_RefusedOnEveryPath: what the environment policy
// refuses on a pod is refused on the pod template of each kind, whichever of
// the four paths produces the object, by the same check, with a violation
// naming the component and the field. The kind names the field by its path in
// the properties; the other three by its path in the object. With no policy
// passed the transform applies its default, which allows no host namespace.
func TestPodTemplateKindsPolicy_RefusedOnEveryPath(t *testing.T) {
	cases := []struct {
		name   string
		spec   string
		policy *stubPolicy
		// sep joins the pod spec's path to field: ": " for a refusal of the pod,
		// "." for one that starts with a field path.
		sep, field string
	}{
		{"host network", htPlainPod + "hostNetwork: true\n", ptStrictPolicy(), ": ", "hostNetwork is not allowed by environment policy"},
		{"host network with no policy passed", htPlainPod + "hostNetwork: true\n", nil, ": ", "hostNetwork is not allowed by environment policy"},
		{"hostPath volume", htPlainPod + "volumes:\n  - name: host\n    hostPath:\n      path: /var/lib/data\n", ptStrictPolicy(), ": ",
			`volume "host": hostPath volumes are not allowed by environment policy`},
		{"registry not allowed", "containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n", ptStrictPolicy(), ".",
			`containers[0] "app": image "other.example/team/app:1.2.3" is not from an allowed registry`},
		{"cpu over the maximum", htPlainPod + "    resources:\n      limits:\n        cpu: 4\n", ptStrictPolicy(), ".",
			`containers[0] "app": cpu limit "4" exceeds enforced maximum "2"`},
		{"privileged init container", htPlainPod + "initContainers:\n  - name: init\n    image: registry.example/team/init:1.0.0\n    securityContext:\n      privileged: true\n",
			ptStrictPolicy(), ".", `initContainers[0] "init": securityContext.privileged is not allowed by environment policy`},
	}
	for _, k := range podTemplatePolicyKinds {
		for _, path := range podTemplatePolicyPaths {
			for _, tc := range cases {
				t.Run(k.typ+"/"+path.name+"/"+tc.name, func(t *testing.T) {
					_, err := path.build(t, k, tc.spec, tc.policy)
					if path.rendered != "" {
						htWantViolation(t, err, path.rendered+k.kind, k.objectPath+tc.sep+tc.field)
						return
					}
					htWantViolation(t, err, `component "web": `+k.propsPath+tc.sep+tc.field)
				})
			}
		}
	}
}

// TestPodTemplateKindsPolicy_AllowedOnEveryPath: a pod template the policy
// does not refuse builds on each of the four paths.
func TestPodTemplateKindsPolicy_AllowedOnEveryPath(t *testing.T) {
	hostPathAllowed := ptStrictPolicy()
	hostPathAllowed.allowHostPathVols = true
	cases := []struct {
		name   string
		spec   string
		policy *stubPolicy
	}{
		{"plain pod", htPlainPod, ptStrictPolicy()},
		{"plain pod with no policy passed", htPlainPod, nil},
		{"cpu at the maximum", htPlainPod + "    resources:\n      limits:\n        cpu: 2\n", ptStrictPolicy()},
		{"hostPath volume where the policy allows it", htPlainPod + "volumes:\n  - name: host\n    hostPath:\n      path: /var/lib/data\n", hostPathAllowed},
		{"any registry with no policy passed", "containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n", nil},
	}
	for _, k := range podTemplatePolicyKinds {
		for _, path := range podTemplatePolicyPaths {
			for _, tc := range cases {
				t.Run(k.typ+"/"+path.name+"/"+tc.name, func(t *testing.T) {
					objs, err := path.build(t, k, tc.spec, tc.policy)
					if err != nil {
						t.Fatalf("transform: %v", err)
					}
					if len(objs) != 1 || objs[0].GetObjectKind().GroupVersionKind().Kind != k.kind {
						t.Fatalf("generated %v, want the one %s", objs, k.kind)
					}
				})
			}
		}
	}
}

// TestPodTemplateKindsPolicy_ReadTimeRefusalsAndTheRenderedPaths pins how the
// four paths differ on what the kind refuses when it is read. An untagged
// image and ephemeral containers are refused on every path. priority, overhead
// and, on a kind whose controller keeps its pods running, activeDeadlineSeconds
// are refused by the kind only: the three paths that take an object written
// elsewhere pass them, and the API server decides.
func TestPodTemplateKindsPolicy_ReadTimeRefusalsAndTheRenderedPaths(t *testing.T) {
	cases := []struct {
		name     string
		spec     string
		want     string
		rendered bool // refused on the three rendered paths too
	}{
		{"untagged image", "containers:\n  - name: app\n    image: registry.example/team/app\n", `containers[0] "app": image "registry.example/team/app" rejected`, true},
		{"ephemeral containers", htPlainPod + "ephemeralContainers:\n  - name: debug\n    image: registry.example/team/debug:1.0.0\n", "ephemeralContainers: not supported", true},
		{"priority", htPlainPod + "priority: 1000\n", "priority: not authorable", false},
		{"overhead", htPlainPod + "overhead:\n  cpu: 100m\n", "overhead: not authorable", false},
		{"active deadline", htPlainPod + "activeDeadlineSeconds: 600\n", "activeDeadlineSeconds: not supported", false},
	}
	for _, k := range podTemplatePolicyKinds {
		for _, path := range podTemplatePolicyPaths {
			for _, tc := range cases {
				t.Run(k.typ+"/"+path.name+"/"+tc.name, func(t *testing.T) {
					_, err := path.build(t, k, tc.spec, ptStrictPolicy())
					switch {
					case path.rendered == "" || tc.rendered:
						if err == nil || !strings.Contains(err.Error(), tc.want) {
							t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
						}
					case err != nil:
						t.Fatalf("refused: %v; the rendered paths do not refuse this field (update the README's stated difference if they now do)", err)
					}
				})
			}
		}
	}
}
