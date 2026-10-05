package components_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// A refusal by the environment policy carries a class a consumer reads from the
// violation, without matching its text (go-kure/launcher#849). These tests hold
// each class on every path its refusal can come from: a component's own
// ApplyPolicy, the rendered-object check of template delivery, passthrough at
// the transform and at generation, and a manifests source, inline at the
// transform and fetched at generation. A trait sub-application's path is held
// in the traits package, the trait-capability class in pkg/oam.

// rcPolicy builds the policy of a case; extra is the hosts a path needs listed
// beside the policy's own registries, the address of its test server.
type rcPolicy func(extra ...string) oam.Policy

// rcStrict is ptStrictPolicy with extra added to its registries.
func rcStrict(extra ...string) oam.Policy {
	p := ptStrictPolicy()
	p.allowedRegistries = append(p.allowedRegistries, extra...)
	return p
}

// rcNoSecrets forbids explicit secrets and restricts nothing else.
func rcNoSecrets(...string) oam.Policy { return esForbidding() }

// rcWith is rcStrict changed by set.
func rcWith(set func(p *stubPolicy)) rcPolicy {
	return func(extra ...string) oam.Policy {
		p := ptStrictPolicy()
		p.allowedRegistries = append(p.allowedRegistries, extra...)
		set(p)
		return p
	}
}

// rcWantClass fails unless err is the violation of component web with the
// given class, and, for a class other than the unclassified value, unless the
// cause chain holds the refusal of that class with its text in the error.
func rcWantClass(t *testing.T, err error, class oam.RefusalClass) {
	t.Helper()
	htWantViolation(t, err)
	var v *oam.ViolationError
	if !errors.As(err, &v) {
		return
	}
	if v.Class != class {
		t.Errorf("violation has class %q, want %q: %v", v.Class, class, err)
	}
	var r *oam.PolicyRefusal
	switch found := errors.As(err, &r); {
	case class == oam.RefusalUnclassified && found:
		t.Errorf("an error that is no refusal by the policy carries a refusal of class %q: %v", r.Class, err)
	case class != oam.RefusalUnclassified && !found:
		t.Errorf("the cause chain holds no *oam.PolicyRefusal: %v", err)
	case found && !strings.Contains(err.Error(), r.Message):
		t.Errorf("the refusal's message %q is not in the error %q", r.Message, err)
	}
}

// rcObjectPaths are the five ways a build meets an object written elsewhere.
// Each builds the object of doc under the policy and returns the error.
var rcObjectPaths = []struct {
	name string
	// rendered marks template delivery, which does not refuse a Secret a chart
	// renders (enforceExplicitSecretObject).
	rendered bool
	build    func(t *testing.T, doc string, policy rcPolicy) error
}{
	{
		name: "template delivery", rendered: true,
		build: func(t *testing.T, doc string, policy rcPolicy) error {
			srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", map[string]string{"object.yaml": doc})
			_, err := htTransform(srvURL, policy(htServerHost(t, srvURL)))
			return err
		},
	},
	{
		name: "passthrough at the transform",
		build: func(t *testing.T, doc string, policy rcPolicy) error {
			_, err := ptTransform(ptObject(t, doc), policy())
			return err
		},
	},
	{
		// Object is exported: the object is replaced after the policy passed
		// another, and Generate holds what it emits to the policy again.
		name: "passthrough at generation",
		build: func(t *testing.T, doc string, policy rcPolicy) error {
			cfg, err := (&components.PassthroughHandler{}).ToApplicationConfig(&oam.Component{
				Name: "web", Type: "passthrough",
				Properties: map[string]any{"object": ptObject(t, ptDeployment(htPlainPod))},
			}, "demo")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			if err := cfg.(oam.Enforceable).ApplyPolicy(policy()); err != nil {
				t.Fatalf("ApplyPolicy on the allowed object: %v", err)
			}
			cfg.(*components.PassthroughConfig).Object = ptObject(t, doc)
			_, err = cfg.Generate(nil)
			return err
		},
	},
	{
		name: "manifests, inline at the transform",
		build: func(_ *testing.T, doc string, policy rcPolicy) error {
			_, err := mfTransformer().Transform(mfApp("manifests", mfInline(mfNamespaced(doc))),
				oam.TransformContext{Namespace: "demo", Policy: policy()})
			return err
		},
	},
	{
		name: "manifests, a url source at generation",
		build: func(t *testing.T, doc string, policy rcPolicy) error {
			srv, _ := mfServe(t, mfNamespaced(doc))
			cluster, err := mfTransformer().Transform(mfApp("manifests", map[string]any{"url": srv.URL + "/manifests.yaml"}),
				oam.TransformContext{Namespace: "demo", Policy: policy(htServerHost(t, srv.URL))})
			if err != nil {
				t.Fatalf("the transform of a url source failed, want the refusal left to generation: %v", err)
			}
			_, err = oam.GenerateApplications(cluster)
			return err
		},
	},
}

const (
	rcPrivileged = "containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      privileged: true\n"
	rcHostPath   = "volumes:\n  - name: host\n    hostPath:\n      path: /etc\n" + htPlainPod
	rcNetAdmin   = "containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\n"
	rcOtherImage = "containers:\n  - name: app\n    image: other.example/team/app:1.2.3\n"
	rcCPULimit   = "containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    resources:\n      limits:\n        cpu: \"8\"\n"
	rcClaim      = "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: thing\nspec:\n  resources:\n    requests:\n      storage: 1Ti\n"
	rcHPAv1      = "apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: thing\nspec:\n" +
		"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n"
)

// rcReplicas is doc with spec.replicas set to n.
func rcReplicas(doc, n string) string {
	return strings.Replace(doc, "spec:\n", "spec:\n  replicas: "+n+"\n", 1)
}

// TestPolicyRefusalClass_ObjectPaths: an object written elsewhere that the
// policy refuses yields the component's violation with the class of the
// refusal, whichever of the five paths the object arrives on.
func TestPolicyRefusalClass_ObjectPaths(t *testing.T) {
	cases := []struct {
		class  oam.RefusalClass
		object string
		policy rcPolicy
		// notRendered marks a refusal template delivery does not raise.
		notRendered bool
	}{
		{class: oam.RefusalHostNamespace, object: ptDeployment("hostNetwork: true\n" + htPlainPod)},
		{class: oam.RefusalPrivileged, object: ptDeployment(rcPrivileged)},
		{class: oam.RefusalHostPath, object: ptDeployment(rcHostPath)},
		{class: oam.RefusalContainerCapability, object: ptDeployment(rcNetAdmin)},
		{class: oam.RefusalRegistry, object: ptDeployment(rcOtherImage)},
		{class: oam.RefusalResourceMaximum, object: ptDeployment(rcCPULimit)},
		{class: oam.RefusalStorageMaximum, object: rcClaim},
		{class: oam.RefusalReplicaMaximum, object: rcReplicas(ptDeployment(htPlainPod), "5")},
		{class: oam.RefusalUnreadableObject, object: ptWorkload("CronJob", "batch/v1beta1", "spec.jobTemplate.spec.template.spec", "restartPolicy: Never\n"+htPlainPod)},
		{class: oam.RefusalExplicitSecret, object: esSecret("stringData:\n  password: " + esSentinel + "\n"), policy: rcNoSecrets, notRendered: true},
	}
	covered := map[oam.RefusalClass]bool{}
	for _, tc := range cases {
		covered[tc.class] = true
		policy := tc.policy
		if policy == nil {
			policy = rcStrict
		}
		for _, path := range rcObjectPaths {
			if path.rendered && tc.notRendered {
				continue
			}
			t.Run(string(tc.class)+"/"+path.name, func(t *testing.T) {
				rcWantClass(t, path.build(t, tc.object, policy), tc.class)
			})
		}
	}
	// The trait-capability class is the transform's own (pkg/oam): no object
	// raises it.
	for _, class := range oam.RefusalClasses() {
		if !covered[class] && class != oam.RefusalTraitCapability {
			t.Errorf("no object case holds the class %q", class)
		}
	}
}

// TestPolicyRefusalClass_ObjectLeaves: the refusals of the shared object check
// that the per-path cases do not reach, each through an inline manifests
// source.
func TestPolicyRefusalClass_ObjectLeaves(t *testing.T) {
	cases := []struct {
		name   string
		class  oam.RefusalClass
		object string
		policy rcPolicy
	}{
		{name: "host PID namespace", class: oam.RefusalHostNamespace, object: ptDeployment("hostPID: true\n" + htPlainPod)},
		{name: "host IPC namespace", class: oam.RefusalHostNamespace, object: ptDeployment("hostIPC: true\n" + htPlainPod)},
		{
			name: "HostProcess container", class: oam.RefusalPrivileged,
			object: ptDeployment("containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    securityContext:\n      windowsOptions:\n        hostProcess: true\n"),
		},
		{name: "HostProcess pod", class: oam.RefusalPrivileged, object: ptDeployment("securityContext:\n  windowsOptions:\n    hostProcess: true\n" + htPlainPod)},
		{name: "PersistentVolume with a hostPath source", class: oam.RefusalHostPath, object: pvDoc("v1", pvHostPath)},
		{name: "PersistentVolume with a local source", class: oam.RefusalHostPath, object: pvDoc("v1", pvLocal)},
		{
			name: "capability outside the allowed list", class: oam.RefusalContainerCapability, object: ptDeployment(rcNetAdmin),
			policy: rcWith(func(p *stubPolicy) { p.forbiddenContainerCaps, p.allowedContainerCaps = nil, []string{"CHOWN"} }),
		},
		{
			name: "capability under a forbidden ALL", class: oam.RefusalContainerCapability, object: ptDeployment(rcNetAdmin),
			policy: rcWith(func(p *stubPolicy) { p.forbiddenContainerCaps = []string{"ALL"} }),
		},
		{
			name: "image of an init container", class: oam.RefusalRegistry,
			object: ptDeployment("initContainers:\n  - name: setup\n    image: other.example/team/setup:1.0.0\n" + htPlainPod),
		},
		{
			name: "memory limit", class: oam.RefusalResourceMaximum,
			object: ptDeployment("containers:\n  - name: app\n    image: registry.example/team/app:1.2.3\n    resources:\n      limits:\n        memory: 8Gi\n"),
		},
		{
			name: "pod-level resources", class: oam.RefusalResourceMaximum,
			object: ptDeployment("resources:\n  limits:\n    cpu: \"8\"\n" + htPlainPod),
		},
		{
			name: "StatefulSet claim template", class: oam.RefusalStorageMaximum,
			object: "apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: thing\nspec:\n" +
				"  volumeClaimTemplates:\n    - metadata:\n        name: data\n      spec:\n        resources:\n          requests:\n            storage: 1Ti\n" +
				"  template:\n    spec:\n" + htIndent(htPlainPod, "      "),
		},
		{
			name: "generic ephemeral volume", class: oam.RefusalStorageMaximum,
			object: ptDeployment("volumes:\n  - name: scratch\n    ephemeral:\n      volumeClaimTemplate:\n        spec:\n          resources:\n            requests:\n              storage: 1Ti\n" + htPlainPod),
		},
		{name: "PersistentVolume capacity", class: oam.RefusalStorageMaximum, object: pvDoc("v1", pvOverMax)},
		{
			name: "HorizontalPodAutoscaler", class: oam.RefusalReplicaMaximum,
			object: strings.Replace(rcHPAv1, "autoscaling/v1", "autoscaling/v2", 1) + "  minReplicas: 1\n  maxReplicas: 9\n",
		},
		{
			name: "HorizontalPodAutoscaler in an unregistered API version", class: oam.RefusalReplicaMaximum,
			object: mfNamespaced(rcHPAv1 + "  maxReplicas: 9\n"),
		},
		{
			name: "HorizontalPodAutoscaler with a maximum that is no integer", class: oam.RefusalUnreadableObject,
			object: mfNamespaced(rcHPAv1 + "  maxReplicas: \"9\"\n"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := tc.policy
			if policy == nil {
				policy = rcStrict
			}
			_, err := mfTransform("manifests", mfInline(tc.object), policy())
			rcWantClass(t, err, tc.class)
		})
	}
}

// TestPolicyRefusalClass_PassthroughUnreadable: an object of a registered kind
// that does not decode as its kind is refused as unreadable, at the transform
// and at generation; only passthrough carries an object as a map.
func TestPolicyRefusalClass_PassthroughUnreadable(t *testing.T) {
	doc := rcReplicas(ptDeployment(htPlainPod), "three")
	for _, path := range rcObjectPaths {
		if !strings.HasPrefix(path.name, "passthrough") {
			continue
		}
		t.Run(path.name, func(t *testing.T) {
			err := path.build(t, doc, rcStrict)
			rcWantClass(t, err, oam.RefusalUnreadableObject)
			if !strings.Contains(err.Error(), "the object cannot be read, so it cannot be checked against environment policy: ") {
				t.Errorf("the refusal lost the text of what failed to decode: %v", err)
			}
		})
	}
}

// TestPolicyRefusalClass_ComponentApplyPolicy: a component whose own
// ApplyPolicy refuses yields its violation with the class of the refusal.
func TestPolicyRefusalClass_ComponentApplyPolicy(t *testing.T) {
	pod := func(spec string) func(*testing.T) map[string]any {
		return func(t *testing.T) map[string]any { return ptObject(t, spec) }
	}
	props := func(p map[string]any) func(*testing.T) map[string]any {
		return func(*testing.T) map[string]any { return p }
	}
	type componentCase struct {
		name    string
		class   oam.RefusalClass
		typ     string
		handler oam.ComponentHandler
		props   func(t *testing.T) map[string]any
		policy  rcPolicy
	}
	cases := []componentCase{
		{name: "pod, host network", class: oam.RefusalHostNamespace, typ: "pod", handler: &components.PodHandler{}, props: pod("hostNetwork: true\n" + htPlainPod)},
		{name: "pod, privileged container", class: oam.RefusalPrivileged, typ: "pod", handler: &components.PodHandler{}, props: pod(rcPrivileged)},
		{name: "pod, hostPath volume", class: oam.RefusalHostPath, typ: "pod", handler: &components.PodHandler{}, props: pod(rcHostPath)},
		{name: "persistentvolume, hostPath source", class: oam.RefusalHostPath, typ: "persistentvolume", handler: &components.PersistentVolumeHandler{}, props: pod(pvHostPath)},
		{name: "pod, forbidden capability", class: oam.RefusalContainerCapability, typ: "pod", handler: &components.PodHandler{}, props: pod(rcNetAdmin)},
		{name: "pod, image registry", class: oam.RefusalRegistry, typ: "pod", handler: &components.PodHandler{}, props: pod(rcOtherImage)},
		{
			name: "manifests, source host", class: oam.RefusalRegistry, typ: "manifests", handler: &components.ManifestsHandler{},
			props: props(map[string]any{"url": "https://other.example/manifests.yaml"}),
		},
		{
			name: "gitrepository, source host", class: oam.RefusalRegistry, typ: "gitrepository", handler: &components.GitRepositoryHandler{},
			props: props(map[string]any{"url": "https://git.other.example/org/repo"}),
		},
		{
			name: "helmrepository, source host", class: oam.RefusalRegistry, typ: "helmrepository", handler: &components.HelmRepositoryHandler{},
			props: props(map[string]any{"url": "https://charts.other.example/stable"}),
		},
		{
			name: "ocirepository, registry not named explicitly", class: oam.RefusalRegistry, typ: "ocirepository", handler: &components.OCIRepositoryHandler{},
			props: props(map[string]any{"url": "oci://registry/app"}),
		},
		{
			name: "bucket, source host", class: oam.RefusalRegistry, typ: "bucket", handler: &components.BucketHandler{},
			props: props(map[string]any{"bucketName": "manifests", "endpoint": "minio.other.example:9000"}),
		},
		{
			name: "bucket, Amazon S3 endpoint", class: oam.RefusalRegistry, typ: "bucket", handler: &components.BucketHandler{},
			props: props(map[string]any{"bucketName": "manifests", "endpoint": "s3.amazonaws.com", "provider": "aws"}),
		},
		{
			name: "helmtemplate, chart source host", class: oam.RefusalRegistry, typ: "helmtemplate", handler: &components.HelmTemplateHandler{},
			props: props(map[string]any{"chart": "testchart", "version": "0.1.0", "source": map[string]any{"url": "https://charts.other.example"}}),
		},
		{name: "pod, cpu limit", class: oam.RefusalResourceMaximum, typ: "pod", handler: &components.PodHandler{}, props: pod(rcCPULimit)},
		{name: "persistentvolume, capacity", class: oam.RefusalStorageMaximum, typ: "persistentvolume", handler: &components.PersistentVolumeHandler{}, props: pod(pvOverMax)},
		{
			name: "persistentvolumeclaim, size", class: oam.RefusalStorageMaximum, typ: "persistentvolumeclaim", handler: &components.PersistentVolumeClaimHandler{},
			props: props(map[string]any{"size": "1Ti"}),
		},
		{
			name: "replicaset, replicas", class: oam.RefusalReplicaMaximum, typ: "replicaset", handler: &components.ReplicaSetHandler{},
			props: func(t *testing.T) map[string]any {
				p := ptObject(t, rsPlain(htPlainPod))
				p["replicas"] = 4
				return p
			},
		},
		{
			name: "cnpg-cluster, instances", class: oam.RefusalReplicaMaximum, typ: "cnpg-cluster", handler: &components.CnpgClusterHandler{},
			props: props(map[string]any{"instances": 5, "storage": map[string]any{"size": "1Gi"}}),
		},
		{
			name: "cnpg-cluster, storage size", class: oam.RefusalStorageMaximum, typ: "cnpg-cluster", handler: &components.CnpgClusterHandler{},
			props: props(map[string]any{"instances": 1, "storage": map[string]any{"size": "1Ti"}}),
		},
		{
			name: "secret", class: oam.RefusalExplicitSecret, typ: "secret", handler: &components.SecretHandler{},
			props: props(map[string]any{"stringData": map[string]any{"password": esSentinel}}), policy: rcNoSecrets,
		},
		{
			name: "helmtemplate, secretValues", class: oam.RefusalExplicitSecret, typ: "helmtemplate", handler: &components.HelmTemplateHandler{},
			props: props(map[string]any{
				"chart": "testchart", "version": "0.1.0", "source": map[string]any{"url": "https://charts.other.example"},
				"secretValues": map[string]any{"password": esSentinel},
			}),
			policy: rcNoSecrets,
		},
		{
			name: "certificate, keystore password", class: oam.RefusalExplicitSecret, typ: "certificate", handler: &components.CertificateHandler{},
			props:  props(certificateWith("keystores", map[string]any{"pkcs12": map[string]any{"create": true, "password": esSentinel}})),
			policy: rcNoSecrets,
		},
	}
	// The pod of an ACME HTTP01 solver is held to the workload maxima on both
	// kinds an Issuer's spec builds and on both ways a solver answers.
	for _, kind := range certManagerIssuers {
		for _, way := range []string{"ingress", "gatewayHTTPRoute"} {
			cases = append(cases, componentCase{
				name: kind.component + ", solver pod over the cpu maximum, " + way, class: oam.RefusalResourceMaximum, typ: kind.component, handler: kind.handler,
				props: props(acmeIssuer(http01Solver(way, map[string]any{"limits": map[string]any{"cpu": "4"}}))),
			})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := tc.policy
			if policy == nil {
				policy = rcStrict
			}
			_, err := pvTransform(tc.typ, tc.handler, tc.props(t), policy())
			rcWantClass(t, err, tc.class)
		})
	}
}

// TestPolicyRefusalClass_Unclassified: an ApplyPolicy error that is no refusal
// by the policy is the component's violation with the unclassified value, and
// its cause chain holds no refusal: the library's own rules on an object, and
// a maximum of the policy that does not parse.
func TestPolicyRefusalClass_Unclassified(t *testing.T) {
	cases := []struct {
		name   string
		object string
		policy rcPolicy
		want   string
	}{
		{
			name:   "ephemeral container",
			object: ptDeployment(htPlainPod + "ephemeralContainers:\n  - name: debug\n    image: registry.example/team/debug:1.0.0\n"),
			want:   "ephemeralContainers: not supported",
		},
		{
			name:   "image without a tag or digest",
			object: ptDeployment("containers:\n  - name: app\n    image: registry.example/team/app\n"),
			want:   "no tag or digest specified",
		},
		{
			name:   "a maximum of the policy that does not parse",
			object: ptDeployment(rcCPULimit),
			policy: rcWith(func(p *stubPolicy) { p.maxCPU = "plenty" }),
			want:   `invalid enforced max cpu limit value "plenty"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := tc.policy
			if policy == nil {
				policy = rcStrict
			}
			_, err := mfTransform("manifests", mfInline(tc.object), policy())
			rcWantClass(t, err, oam.RefusalUnclassified)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q: the case did not reach the failure it names", err, tc.want)
			}
		})
	}
}
