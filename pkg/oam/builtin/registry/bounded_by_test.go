package registry_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/probe"
)

// These tests hold every boundedBy a built-in schema publishes
// (go-kure/launcher#1580). A declared class is shown to refuse a value at that
// property: from a document that passes, the value at or below the property's
// path is changed, with the keys the changed document needs beside it, the
// build is refused with that class, and it is refused no more once the one
// policy setting behind the class is relaxed. The test runs one way: a property
// that is bounded and declares nothing is not found here.

// boundsPolicy is a Policy with the limits a test sets; every other accessor
// is NoopPolicy's.
type boundsPolicy struct {
	oam.NoopPolicy
	maxReplicas                                     *int32
	maxCPU, maxMemory, maxStorage                   string
	registries, forbiddenCaps                       []string
	cpuLimit, memoryLimit                           string
	hostNetwork, hostPID, hostIPC, privileged, host bool
	// forbidSecrets makes the policy forbid explicit secrets; content is the
	// source of the bodies a manifests url names (boundsContent).
	forbidSecrets bool
	content       boundsContent
}

// boundsContent is a ContentSource that serves a manifests url from a stored
// body, so a row needs no request: the body of the url it names, else the one
// kept under "". A chart is served as boundsChart, whatever its url. Every body
// is recorded as fetched from registry.example.
type boundsContent map[string]string

func (c boundsContent) Lookup(_ context.Context, key oam.ContentKey) (oam.Content, bool, error) {
	if key.Kind == "chart" {
		return oam.Content{Data: boundsChart(), Hosts: []string{"registry.example"}}, true, nil
	}
	body, ok := c[key.URL]
	if !ok {
		body, ok = c[""]
	}
	return oam.Content{Data: []byte(body), Hosts: []string{"registry.example"}}, ok, nil
}

func (boundsContent) Store(context.Context, oam.ContentKey, oam.Content) error { return nil }

func (p *boundsPolicy) MaxReplicas() *int32                      { return p.maxReplicas }
func (p *boundsPolicy) MaxCPU() string                           { return p.maxCPU }
func (p *boundsPolicy) MaxMemory() string                        { return p.maxMemory }
func (p *boundsPolicy) MaxStorageSize() string                   { return p.maxStorage }
func (p *boundsPolicy) AllowedRegistries() []string              { return p.registries }
func (p *boundsPolicy) ForbiddenContainerCapabilities() []string { return p.forbiddenCaps }
func (p *boundsPolicy) DefaultCPULimit() string                  { return p.cpuLimit }
func (p *boundsPolicy) DefaultMemoryLimit() string               { return p.memoryLimit }
func (p *boundsPolicy) AllowHostNetwork() bool                   { return p.hostNetwork }
func (p *boundsPolicy) AllowHostPID() bool                       { return p.hostPID }
func (p *boundsPolicy) AllowHostIPC() bool                       { return p.hostIPC }
func (p *boundsPolicy) AllowPrivileged() bool                    { return p.privileged }
func (p *boundsPolicy) AllowHostPathVolumes() bool               { return p.host }
func (p *boundsPolicy) AllowExplicitSecrets() bool               { return !p.forbidSecrets }
func (p *boundsPolicy) ContentSource() oam.ContentSource         { return p.content }

// knob changes one setting of a policy.
type knob func(*boundsPolicy)

// The settings a row relaxes, one per way a class is held.
var (
	anyReplicas     knob = func(p *boundsPolicy) { p.maxReplicas = nil }
	anyCPU          knob = func(p *boundsPolicy) { p.maxCPU = "" }
	anyMemory       knob = func(p *boundsPolicy) { p.maxMemory = "" }
	anyStorage      knob = func(p *boundsPolicy) { p.maxStorage = "" }
	anyRegistry     knob = func(p *boundsPolicy) { p.registries = nil }
	anyCapability   knob = func(p *boundsPolicy) { p.forbiddenCaps = nil }
	noCPULimit      knob = func(p *boundsPolicy) { p.cpuLimit = "" }
	noMemoryLimit   knob = func(p *boundsPolicy) { p.memoryLimit = "" }
	withCPULimit    knob = func(p *boundsPolicy) { p.cpuLimit = "1" }
	withMemoryLimit knob = func(p *boundsPolicy) { p.memoryLimit = "512Mi" }
	allowNetwork    knob = func(p *boundsPolicy) { p.hostNetwork = true }
	allowPID        knob = func(p *boundsPolicy) { p.hostPID = true }
	allowIPC        knob = func(p *boundsPolicy) { p.hostIPC = true }
	allowPrivileged knob = func(p *boundsPolicy) { p.privileged = true }
	allowHostPath   knob = func(p *boundsPolicy) { p.host = true }
	forbidSecrets   knob = func(p *boundsPolicy) { p.forbidSecrets = true }
	allowSecrets    knob = func(p *boundsPolicy) { p.forbidSecrets = false }
)

// boundsStrict is the policy every row starts from: each limit set, nothing
// allowed.
func boundsStrict(knobs ...knob) *boundsPolicy {
	content := boundsContent{"": manifestsConfigMap, crdURL: crdBody, crdForeignURL: crdBody}
	for _, d := range documents {
		content[d.url()] = d.body
	}
	p := &boundsPolicy{
		maxReplicas: i32(3), maxCPU: "2", maxMemory: "1Gi", maxStorage: "10Gi",
		registries: []string{"registry.example"}, forbiddenCaps: []string{"NET_ADMIN"},
		content: content,
	}
	for _, k := range knobs {
		if k != nil {
			k(p)
		}
	}
	return p
}

// set writes value at a path of a property map, or removes the key with drop.
// The path is written as a declared one: dotted, "[]" after an array's key for
// its first element, which is made when the array has none.
type set struct {
	at    string
	value any
	drop  bool
}

func (s set) apply(t *testing.T, props map[string]any) {
	t.Helper()
	segs := strings.Split(s.at, ".")
	node := props
	for i, seg := range segs {
		key, element := strings.CutSuffix(seg, "[]")
		last := i == len(segs)-1
		if !element {
			switch {
			case last && s.drop:
				delete(node, key)
			case last:
				node[key] = s.value
			default:
				next, ok := node[key].(map[string]any)
				if !ok {
					next = map[string]any{}
					node[key] = next
				}
				node = next
			}
			continue
		}
		list, _ := node[key].([]any)
		if len(list) == 0 {
			list = []any{map[string]any{}}
			node[key] = list
		}
		if last {
			list[0] = s.value
			continue
		}
		next, ok := list[0].(map[string]any)
		if !ok {
			t.Fatalf("set %q: the first element of %q is no object", s.at, key)
		}
		node = next
	}
}

// boundRow is one tested bound: the class a schema declares at a path, and a
// change that shows it.
type boundRow struct {
	// trait is false for a component type.
	trait bool
	typ   string
	// path is where the class is declared; class is the declared class.
	path  string
	class oam.RefusalClass
	// with prepares the passing document for the row, where the one the type
	// starts from cannot take the change as it is.
	with []set
	// change is the one authored value that is refused, first, and the keys
	// that value cannot be written without. On an open node it lies below path.
	change []set
	// strict adds a setting the class needs to the strict policy; relax is the
	// one setting that lifts the refusal.
	strict, relax knob
	// unconditional is set where the refusal holds whenever a policy is applied,
	// oam.NoopPolicy included, so that no policy setting lifts it: the row has no
	// relax, and asserts the refusal under a policy that limits nothing.
	unconditional bool
	// invalidOnceAllowed is set where the changed document is no valid object
	// even under the relaxed policy: the text of the error it then fails with,
	// which is no refusal by the policy.
	invalidOnceAllowed string
}

func (r boundRow) key() string {
	kind := "component"
	if r.trait {
		kind = "trait"
	}
	return kind + " " + r.typ + " " + r.path + " " + string(r.class)
}

const (
	boundImage   = "registry.example/team/app:1.2.3"
	foreignImage = "elsewhere.example/team/app:1.2.3"
)

// The documents a row of a type that holds a document works with: the value of
// `inline` or `object`, or what a `url` names. manifestsConfigMap is the one a
// row starts from, inline or served at the url, or as the object of a
// passthrough. The others are what a row changes it to (documents): a core
// Secret, and an object the policy check cannot read, an autoscaling/v1
// HorizontalPodAutoscaler (a version kure's scheme does not register) whose
// maxReplicas is no integer, which is refused only under a replica maximum.
const (
	manifestsConfigMap  = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  key: value\n"
	manifestsSecret     = "apiVersion: v1\nkind: Secret\nmetadata:\n  name: creds\nstringData:\n  key: value\n"
	manifestsUnreadable = "apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: scale\n  namespace: demo\nspec:\n" +
		"  scaleTargetRef:\n    apiVersion: apps/v1\n    kind: Deployment\n    name: web\n  maxReplicas: many\n"

	// The url of the passing document, under the one host the strict policy lists.
	passingURL = "https://registry.example/settings.yaml"

	// A `crd` row serves a CustomResourceDefinition at the url it passes and at
	// the url it changes it to: the default body is a ConfigMap, which the crd
	// type refuses as no CRD, with no refusal by the policy.
	crdBody       = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n  scope: Namespaced\n  names:\n    kind: Widget\n    plural: widgets\n  versions:\n  - name: v1\n    served: true\n    storage: true\n    schema:\n      openAPIV3Schema:\n        type: object\n"
	crdURL        = "https://registry.example/crds.yaml"
	crdForeignURL = "https://elsewhere.example/crds.yaml"
)

// document is a document that breaks one class of the check on the objects of a
// document (enforceRenderedObjectPolicy, with the Secret rule before it): a
// manifests source, inline or served at its url, and a passthrough object are
// held to it alike, so each class has one document and three rows. strict is
// the setting the class needs on the strict policy, relax the one that lifts it.
type document struct {
	class         oam.RefusalClass
	body          string
	strict, relax knob
}

// url is where the strict policy's content source serves the document.
func (d document) url() string { return "https://registry.example/" + string(d.class) + ".yaml" }

// object is the document as the map a passthrough takes.
func (d document) object() map[string]any {
	var obj map[string]any
	if err := yaml.Unmarshal([]byte(d.body), &obj); err != nil {
		panic("the document of " + string(d.class) + " does not parse: " + err.Error())
	}
	return obj
}

// documents holds the ten classes of the check, one document each. The pod ones
// differ from the passing container by the one field the class refuses.
var documents = func() []document {
	pod := func(container, rest string) string {
		return "apiVersion: v1\nkind: Pod\nmetadata:\n  name: web\nspec:\n  containers:\n  - name: app\n    image: " + container + "\n" + rest
	}
	return []document{
		{class: oam.RefusalHostNamespace, body: pod(boundImage, "  hostNetwork: true\n"), relax: allowNetwork},
		{class: oam.RefusalPrivileged, body: pod(boundImage, "    securityContext:\n      privileged: true\n"), relax: allowPrivileged},
		{class: oam.RefusalHostPath, body: pod(boundImage, "  volumes:\n  - name: etc\n    hostPath:\n      path: /etc\n"), relax: allowHostPath},
		{class: oam.RefusalContainerCapability, body: pod(boundImage, "    securityContext:\n      capabilities:\n        add: [NET_ADMIN]\n"), relax: anyCapability},
		{class: oam.RefusalRegistry, body: pod(foreignImage, ""), relax: anyRegistry},
		{class: oam.RefusalResourceMaximum, body: pod(boundImage, "    resources:\n      limits:\n        cpu: \"8\"\n"), relax: anyCPU},
		{class: oam.RefusalStorageMaximum, body: "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\nspec:\n  accessModes: [ReadWriteOnce]\n" +
			"  resources:\n    requests:\n      storage: 1Ti\n", relax: anyStorage},
		{class: oam.RefusalReplicaMaximum, body: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  replicas: 9\n  selector:\n    matchLabels:\n      tier: web\n" +
			"  template:\n    metadata:\n      labels:\n        tier: web\n    spec:\n      containers:\n      - name: app\n        image: " + boundImage + "\n", relax: anyReplicas},
		{class: oam.RefusalExplicitSecret, body: manifestsSecret, strict: forbidSecrets, relax: allowSecrets},
		{class: oam.RefusalUnreadableObject, body: manifestsUnreadable, relax: anyReplicas},
	}
}()

// boundsChart is the chart a helmtemplate row renders, as the archive the
// content source serves: one ConfigMap, which the strict policy passes.
var boundsChart = sync.OnceValue(func() []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{
		{"app/Chart.yaml", "apiVersion: v2\nname: app\nversion: 1.0.0\n"},
		{"app/templates/settings.yaml", manifestsConfigMap},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body))}); err != nil {
			panic(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			panic(err)
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	if err := gz.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
})

// manifestsFromURL makes the passing document of a manifests row read its
// source from passingURL, in place of the inline one.
var manifestsFromURL = []set{{at: "inline", drop: true}, {at: "url", value: passingURL}}

// onlyHostProcess is the rule a HostProcess container breaks in a pod whose
// other containers are not HostProcess ones: the object is refused as invalid
// once the policy allows privileged containers.
const onlyHostProcess = "a pod with one must have only HostProcess containers"

// containerRows are the bounds of one container: the main one with prefix "",
// an init container or a sidecar with its list's path.
func containerRows(typ, prefix string) []boundRow {
	at := func(p string) string { return prefix + p }
	row := func(path string, class oam.RefusalClass, value any, relax knob) boundRow {
		return boundRow{typ: typ, path: at(path), class: class, change: []set{{at: at(path), value: value}}, relax: relax}
	}
	defaulted := func(path string, value any, strict, relax knob) boundRow {
		r := row(path, oam.RefusalResourceDefaultLimit, value, relax)
		r.strict = strict
		return r
	}
	hostProcess := row("securityContext.windowsOptions.hostProcess", oam.RefusalPrivileged, true, allowPrivileged)
	hostProcess.invalidOnceAllowed = onlyHostProcess
	return []boundRow{
		row("image", oam.RefusalRegistry, foreignImage, anyRegistry),
		row("resources.requests.cpu", oam.RefusalResourceMaximum, "8", anyCPU),
		row("resources.requests.memory", oam.RefusalResourceMaximum, "8Gi", anyMemory),
		row("resources.limits.cpu", oam.RefusalResourceMaximum, "8", anyCPU),
		row("resources.limits.memory", oam.RefusalResourceMaximum, "8Gi", anyMemory),
		defaulted("resources.requests.cpu", "1500m", withCPULimit, noCPULimit),
		defaulted("resources.requests.memory", "768Mi", withMemoryLimit, noMemoryLimit),
		row("securityContext.privileged", oam.RefusalPrivileged, true, allowPrivileged),
		hostProcess,
		row("securityContext.capabilities.add", oam.RefusalContainerCapability, []any{"NET_ADMIN"}, anyCapability),
	}
}

// overSizedClaim is an ephemeral volume source that asks for more storage than
// the strict policy allows.
func overSizedClaim() map[string]any {
	return map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{
		"accessModes": []any{"ReadWriteOnce"},
		"resources":   map[string]any{"requests": map[string]any{"storage": "1Ti"}},
	}}}
}

// sharedKind is one of the three types built from the shared container and pod
// fragments (the `deployment`, `daemonset`, `statefulset`, `job` and `cronjob`
// kinds are not among them since go-kure/launcher#1629: they hold the upstream
// pod template, and their rows are podRows on `template`, or on `jobTemplate`
// for a cronjob). sizedVolumes says that a `pvc` volume of the type takes a
// `size` and generates its claim.
type sharedKind struct {
	typ                string
	sidecars, replicas bool
	sizedVolumes       bool
	extra              map[string]any
}

var sharedKinds = []sharedKind{
	{typ: "role-deployment", sidecars: true, replicas: true},
	{typ: "webservice", sidecars: true, replicas: true, sizedVolumes: true},
	{typ: "worker", sidecars: true, replicas: true, sizedVolumes: true},
}

func sharedKindRows(k sharedKind) []boundRow {
	row := func(path string, class oam.RefusalClass, relax knob, change ...set) boundRow {
		return boundRow{typ: k.typ, path: path, class: class, change: change, relax: relax}
	}
	volume := func(path string, class oam.RefusalClass, relax knob, typ string, change ...set) boundRow {
		change = append(change, set{at: "volumes[].name", value: "extra"}, set{at: "volumes[].mountPath", value: "/extra"})
		if path != "volumes[].type" {
			change = append(change, set{at: "volumes[].type", value: typ})
		}
		return row(path, class, relax, change...)
	}
	// A HostProcess pod must use the host network, so the row writes that too
	// and starts from a policy that allows it: the refusal left is the
	// privileged one.
	podHostProcess := row("podSecurityContext.windowsOptions.hostProcess", oam.RefusalPrivileged, allowPrivileged,
		set{at: "podSecurityContext.windowsOptions.hostProcess", value: true}, set{at: "hostNetwork", value: true})
	podHostProcess.strict = allowNetwork

	rows := containerRows(k.typ, "")
	rows = append(rows, containerRows(k.typ, "initContainers[].")...)
	if k.sidecars {
		rows = append(rows, containerRows(k.typ, "sidecars[].")...)
	}
	rows = append(rows,
		row("hostNetwork", oam.RefusalHostNamespace, allowNetwork, set{at: "hostNetwork", value: true}),
		row("hostPID", oam.RefusalHostNamespace, allowPID, set{at: "hostPID", value: true}),
		row("hostIPC", oam.RefusalHostNamespace, allowIPC, set{at: "hostIPC", value: true}),
		podHostProcess,
		row("podResources.requests.cpu", oam.RefusalResourceMaximum, anyCPU, set{at: "podResources.requests.cpu", value: "8"}),
		row("podResources.requests.memory", oam.RefusalResourceMaximum, anyMemory, set{at: "podResources.requests.memory", value: "8Gi"}),
		row("podResources.limits.cpu", oam.RefusalResourceMaximum, anyCPU, set{at: "podResources.limits.cpu", value: "8"}),
		row("podResources.limits.memory", oam.RefusalResourceMaximum, anyMemory, set{at: "podResources.limits.memory", value: "8Gi"}),
		volume("volumes[].type", oam.RefusalHostPath, allowHostPath, "",
			set{at: "volumes[].type", value: "hostPath"}, set{at: "volumes[].path", value: "/etc"}),
		volume("volumes[].ephemeral", oam.RefusalStorageMaximum, anyStorage, "ephemeral",
			set{at: "volumes[].ephemeral", value: overSizedClaim()}),
		volume("volumes[].image", oam.RefusalRegistry, anyRegistry, "image",
			set{at: "volumes[].image", value: map[string]any{"reference": foreignImage}}),
	)
	if k.replicas {
		rows = append(rows, row("replicas", oam.RefusalReplicaMaximum, anyReplicas, set{at: "replicas", value: 9}))
	}
	if k.sizedVolumes {
		// The schema declares no `size` on a volume, so the bound is on the
		// volume itself, an open node.
		rows = append(rows, volume("volumes[]", oam.RefusalStorageMaximum, anyStorage, "pvc",
			set{at: "volumes[].size", value: "1Ti"}))
	}
	return rows
}

// podRows are the bounds of a pod written as the API type: at the top of a pod
// component with prefix "", and below the open template of the eight kinds
// that hold one (podtemplate, replicaset, replicationcontroller, deployment,
// daemonset, statefulset, job and cronjob), where every class is declared on
// that one node.
func podRows(typ, node, prefix string) []boundRow {
	row := func(declared string, class oam.RefusalClass, relax knob, change ...set) boundRow {
		for i := range change {
			change[i].at = prefix + change[i].at
		}
		if node != "" {
			declared = node
		}
		return boundRow{typ: typ, path: declared, class: class, change: change, relax: relax}
	}
	var rows []boundRow
	for _, list := range []string{"containers", "initContainers"} {
		rows = append(rows,
			row(list, oam.RefusalRegistry, anyRegistry, set{at: list + "[].image", value: foreignImage}),
			row(list, oam.RefusalResourceMaximum, anyCPU, set{at: list + "[].resources.limits.cpu", value: "8"}),
			row(list, oam.RefusalResourceMaximum, anyMemory, set{at: list + "[].resources.limits.memory", value: "8Gi"}),
			row(list, oam.RefusalPrivileged, allowPrivileged, set{at: list + "[].securityContext.privileged", value: true}),
			row(list, oam.RefusalContainerCapability, anyCapability, set{at: list + "[].securityContext.capabilities.add", value: []any{"NET_ADMIN"}}),
		)
	}
	volume := func(class oam.RefusalClass, relax knob, source string, value any) boundRow {
		return row("volumes", class, relax, set{at: "volumes[]." + source, value: value}, set{at: "volumes[].name", value: "extra"})
	}
	return append(rows,
		row("hostNetwork", oam.RefusalHostNamespace, allowNetwork, set{at: "hostNetwork", value: true}),
		row("hostPID", oam.RefusalHostNamespace, allowPID, set{at: "hostPID", value: true}),
		row("hostIPC", oam.RefusalHostNamespace, allowIPC, set{at: "hostIPC", value: true}),
		row("securityContext", oam.RefusalPrivileged, allowPrivileged, set{at: "securityContext.windowsOptions.hostProcess", value: true}),
		row("resources", oam.RefusalResourceMaximum, anyCPU, set{at: "resources.limits.cpu", value: "8"}),
		row("resources", oam.RefusalResourceMaximum, anyMemory, set{at: "resources.limits.memory", value: "8Gi"}),
		volume(oam.RefusalHostPath, allowHostPath, "hostPath", map[string]any{"path": "/etc"}),
		volume(oam.RefusalStorageMaximum, anyStorage, "ephemeral", overSizedClaim()),
		volume(oam.RefusalRegistry, anyRegistry, "image", map[string]any{"reference": foreignImage}),
	)
}

// fluxAndManifestsRows are the bounds of the types that hold a source: the host
// of the url of `helm` (under either delivery), `gitrepository` and `manifests`.
func fluxAndManifestsRows() []boundRow {
	return []boundRow{
		{typ: "gitrepository", path: "url", class: oam.RefusalRegistry, relax: anyRegistry,
			change: []set{{at: "url", value: "https://elsewhere.example/org/repo"}}},
		{typ: "helm", path: "source.url", class: oam.RefusalRegistry, relax: anyRegistry,
			change: []set{{at: "source.url", value: "https://elsewhere.example/charts"}}},
		// Under template delivery helm lowers to helmtemplate, which raises the
		// refusal when it holds the host of source.url.
		{typ: "helm", path: "source.url", class: oam.RefusalRegistry, relax: anyRegistry, with: []set{{at: "delivery", value: "template"}},
			change: []set{{at: "source.url", value: "https://elsewhere.example/charts"}}},
		{typ: "manifests", path: "url", class: oam.RefusalRegistry, relax: anyRegistry, with: manifestsFromURL,
			change: []set{{at: "url", value: "https://elsewhere.example/settings.yaml"}}},
	}
}

// documentRows are the bounds of the properties that hold a document the handler
// reads object by object: `inline` and `url` of `manifests`, and the `object` of
// a passthrough, an open node. Each carries every class of documents, as one
// check holds the objects of all three. RefusalObjectKind is the transform's,
// on every application, and no handler raises it; the classes a chart's rendered
// objects raise are not declared on helmtemplate, whose render comes from source
// and values together.
func documentRows() []boundRow {
	var rows []boundRow
	for _, d := range documents {
		rows = append(rows,
			boundRow{typ: "manifests", path: "inline", class: d.class, strict: d.strict, relax: d.relax,
				change: []set{{at: "inline", value: d.body}}},
			boundRow{typ: "manifests", path: "url", class: d.class, strict: d.strict, relax: d.relax, with: manifestsFromURL,
				change: []set{{at: "url", value: d.url()}}},
			boundRow{typ: "passthrough", path: "object", class: d.class, strict: d.strict, relax: d.relax,
				change: []set{{at: "object", value: d.object()}}},
		)
	}
	return rows
}

// helmTemplateRows are the bounds of helmtemplate that sit on a property: the
// host of source.url, held before any request, and secretValues. The chart is
// served from the content source, so a row needs no request.
func helmTemplateRows() []boundRow {
	return []boundRow{
		{typ: "helmtemplate", path: "source.url", class: oam.RefusalRegistry, relax: anyRegistry,
			change: []set{{at: "source.url", value: "https://elsewhere.example/charts"}}},
		{typ: "helmtemplate", path: "secretValues", class: oam.RefusalExplicitSecret, strict: forbidSecrets, relax: allowSecrets,
			change: []set{{at: "secretValues", value: map[string]any{"password": "hunter2"}}}},
	}
}

// helmBucketSource makes the passing document of a helm row read its chart from
// an inline Bucket on an allowed endpoint, in place of the HelmRepository url. A
// Bucket source takes no version.
var helmBucketSource = []set{
	{at: "source.url", drop: true}, {at: "version", drop: true},
	{at: "source.kind", value: "Bucket"}, {at: "source.endpoint", value: "registry.example"}, {at: "source.bucketName", value: "charts"},
}

// ociChartTemplate makes the passing document of a helmrelease row take its chart
// from an OCIRepository on an allowed registry: the one chart template that
// names a host.
var ociChartTemplate = []set{
	{at: "chart.kind", value: "OCIRepository"}, {at: "chart.spec.url", value: "oci://registry.example/charts/app"},
}

// fluxSourceRows are the bounds of the Flux source family: where a type is
// fetched from (registry, on the property that holds the address; for a Bucket
// on the endpoint and, for the gcp provider, on the provider, whose host Flux
// does not read from the endpoint), and the one address that is a credential.
// The refusals the same properties can also raise in another form (an oci:// url
// that does not name its registry, an Amazon S3 endpoint) have the class and
// the property of the host form and no row of their own.
func fluxSourceRows() []boundRow {
	registry := func(typ, path, value string, with ...set) boundRow {
		return boundRow{typ: typ, path: path, class: oam.RefusalRegistry, relax: anyRegistry, with: with,
			change: []set{{at: path, value: value}}}
	}
	foreignOCI := "oci://elsewhere.example/org/app"
	return []boundRow{
		registry("bucket", "endpoint", "elsewhere.example"),
		registry("bucket", "provider", "gcp"),
		registry("helmrepository", "url", "https://elsewhere.example/charts"),
		registry("ocirepository", "url", foreignOCI),
		registry("oci", "source.url", foreignOCI),
		// The chart is an open node: the bound is on the url below it.
		{typ: "helmrelease", path: "chart", class: oam.RefusalRegistry, relax: anyRegistry, with: ociChartTemplate,
			change: []set{{at: "chart.spec.url", value: "oci://elsewhere.example/charts/app"}}},
		registry("imagerepository", "image", "elsewhere.example/org/app"),
		registry("resourcesetinputprovider", "url", foreignOCI),
		registry("helm", "source.endpoint", "elsewhere.example", helmBucketSource...),
		registry("helm", "source.provider", "gcp", helmBucketSource...),
		{typ: "fluxcd-provider", path: "address", class: oam.RefusalExplicitSecret, strict: forbidSecrets, relax: allowSecrets,
			change: []set{{at: "address", value: "https://hooks.example/services/credential"}}},
	}
}

// postgresqlRows are the bounds of postgresql, which lowers onto a cnpg-cluster
// and, with a pooler, a cnpg-pooler: the instance count, the image the Cluster
// runs (the composed default of `version` included, which is refused as an
// authored one is), the Cluster's resources and storage, and the PgBouncer image.
// The unset pooler image and the 1Gi storage fallback have the class and the
// property of the authored form and no row of their own.
func postgresqlRows() []boundRow {
	row := func(path string, class oam.RefusalClass, relax knob, change ...set) boundRow {
		return boundRow{typ: "postgresql", path: path, class: class, change: change, relax: relax}
	}
	// The pooler is an open node: the bound is on the image below it, and a
	// pooler that names no image is refused under a list.
	pooler := row("pooler", oam.RefusalRegistry, anyRegistry, set{at: "pooler.image", value: foreignImage})
	pooler.with = []set{{at: "pooler.enabled", value: true}, {at: "pooler.image", value: boundImage}}
	return []boundRow{
		row("replicas", oam.RefusalReplicaMaximum, anyReplicas, set{at: "replicas", value: 9}),
		row("imageName", oam.RefusalRegistry, anyRegistry, set{at: "imageName", value: foreignImage}),
		pooler,
		row("resources.requests.cpu", oam.RefusalResourceMaximum, anyCPU, set{at: "resources.requests.cpu", value: "8"}),
		row("resources.requests.memory", oam.RefusalResourceMaximum, anyMemory, set{at: "resources.requests.memory", value: "8Gi"}),
		row("resources.limits.cpu", oam.RefusalResourceMaximum, anyCPU, set{at: "resources.limits.cpu", value: "8"}),
		row("resources.limits.memory", oam.RefusalResourceMaximum, anyMemory, set{at: "resources.limits.memory", value: "8Gi"}),
		row("storageSize", oam.RefusalStorageMaximum, anyStorage, set{at: "storageSize", value: "1Ti"}),
	}
}

// cnpgClusterRows are the bounds of cnpg-cluster. Its resources, the volumes it
// asks for, the security contexts and the extensions are open nodes: each bound
// is on the value below the declared path.
func cnpgClusterRows() []boundRow {
	row := func(path string, class oam.RefusalClass, relax knob, change ...set) boundRow {
		return boundRow{typ: "cnpg-cluster", path: path, class: class, change: change, relax: relax}
	}
	return []boundRow{
		row("instances", oam.RefusalReplicaMaximum, anyReplicas, set{at: "instances", value: 9}),
		row("resources", oam.RefusalResourceMaximum, anyCPU, set{at: "resources.requests.cpu", value: "8"}),
		row("resources", oam.RefusalResourceMaximum, anyMemory, set{at: "resources.requests.memory", value: "8Gi"}),
		row("resources", oam.RefusalResourceMaximum, anyCPU, set{at: "resources.limits.cpu", value: "8"}),
		row("resources", oam.RefusalResourceMaximum, anyMemory, set{at: "resources.limits.memory", value: "8Gi"}),
		row("storage", oam.RefusalStorageMaximum, anyStorage, set{at: "storage.size", value: "1Ti"}),
		row("walStorage", oam.RefusalStorageMaximum, anyStorage, set{at: "walStorage.size", value: "1Ti"}),
		row("tablespaces", oam.RefusalStorageMaximum, anyStorage,
			set{at: "tablespaces[].storage.size", value: "1Ti"}, set{at: "tablespaces[].name", value: "scratch"}),
		row("ephemeralVolumeSource", oam.RefusalStorageMaximum, anyStorage, set{at: "ephemeralVolumeSource", value: overSizedClaim()}),
		row("imageName", oam.RefusalRegistry, anyRegistry, set{at: "imageName", value: foreignImage}),
		row("postgresql", oam.RefusalRegistry, anyRegistry,
			set{at: "postgresql.extensions[].image.reference", value: foreignImage}, set{at: "postgresql.extensions[].name", value: "ext"}),
		row("securityContext", oam.RefusalPrivileged, allowPrivileged, set{at: "securityContext.privileged", value: true}),
		row("securityContext", oam.RefusalContainerCapability, anyCapability, set{at: "securityContext.capabilities.add", value: []any{"NET_ADMIN"}}),
		row("podSecurityContext", oam.RefusalPrivileged, allowPrivileged, set{at: "podSecurityContext.windowsOptions.hostProcess", value: true}),
	}
}

// cnpgOtherRows are the bounds of the Pooler, the ObjectStore and the two image
// catalogs. The Pooler's pod template holds every class of a pod, on that one
// node, as the pod template kinds do; its instance count is not policed.
func cnpgOtherRows() []boundRow {
	rows := []boundRow{
		{typ: "cnpg-pooler", path: "pgbouncer", class: oam.RefusalRegistry, relax: anyRegistry,
			change: []set{{at: "pgbouncer.image", value: foreignImage}}},
	}
	rows = append(rows, podRows("cnpg-pooler", "template", "template.spec.")...)
	sidecar := func(at, value string, relax knob) boundRow {
		return boundRow{typ: "cnpg-objectstore", path: "instanceSidecarConfiguration", class: oam.RefusalResourceMaximum, relax: relax,
			change: []set{{at: "instanceSidecarConfiguration.resources." + at, value: value}}}
	}
	rows = append(rows,
		sidecar("requests.cpu", "8", anyCPU), sidecar("requests.memory", "8Gi", anyMemory),
		sidecar("limits.cpu", "8", anyCPU), sidecar("limits.memory", "8Gi", anyMemory),
	)
	// The three fields of a catalog that name an image: an image, the image
	// volume of one of its extensions, and a component image.
	for _, typ := range []string{"cnpg-imagecatalog", "cnpg-clusterimagecatalog"} {
		rows = append(rows,
			boundRow{typ: typ, path: "images", class: oam.RefusalRegistry, relax: anyRegistry,
				change: []set{{at: "images[].image", value: foreignImage}}},
			boundRow{typ: typ, path: "images", class: oam.RefusalRegistry, relax: anyRegistry,
				change: []set{{at: "images[].extensions[].image.reference", value: foreignImage}, {at: "images[].extensions[].name", value: "ext"}}},
			boundRow{typ: typ, path: "componentImages", class: oam.RefusalRegistry, relax: anyRegistry,
				change: []set{{at: "componentImages[].image", value: foreignImage}, {at: "componentImages[].key", value: "pgbouncer"}}},
		)
	}
	return rows
}

// coreKindRows are the bounds of the kinds that hold one object by policy: the
// url a `crd` is fetched from, the replica maximum of a HorizontalPodAutoscaler,
// the node path and the capacity of a PersistentVolume, and the size of a claim.
// A `crd` inline document is no bound: a CustomResourceDefinition passes the
// check on the objects of a document. The capacity is an open object, so the
// bound is on the one key below it that the policy reads, `storage`.
func coreKindRows() []boundRow {
	volume := func(path string, class oam.RefusalClass, relax knob, change set) boundRow {
		return boundRow{typ: "persistentvolume", path: path, class: class, change: []set{change}, relax: relax}
	}
	return []boundRow{
		{typ: "crd", path: "url", class: oam.RefusalRegistry, relax: anyRegistry,
			change: []set{{at: "url", value: crdForeignURL}}},
		{typ: "horizontalpodautoscaler", path: "maxReplicas", class: oam.RefusalReplicaMaximum, relax: anyReplicas,
			change: []set{{at: "maxReplicas", value: 9}}},
		volume("hostPath", oam.RefusalHostPath, allowHostPath, set{at: "hostPath", value: map[string]any{"path": "/data"}}),
		volume("local", oam.RefusalHostPath, allowHostPath, set{at: "local", value: map[string]any{"path": "/mnt/disk"}}),
		volume("capacity", oam.RefusalStorageMaximum, anyStorage, set{at: "capacity.storage", value: "1Ti"}),
		{typ: "persistentvolumeclaim", path: "size", class: oam.RefusalStorageMaximum, relax: anyStorage,
			change: []set{{at: "size", value: "1Ti"}}},
	}
}

// providerKindRows are the bounds of the kinds whose handler holds one decoded
// spec to the policy for what it carries: the BGP session password, the
// keystore passwords of a certificate, the cpu and memory of an ACME HTTP01
// solver's pod, the credentials a secret store holds in the clear, and the object
// an ExternalSecret has the operator write. Each is an open node (or, for
// `password`, a string), so the row changes a value below it. A union of a store
// is shown once, as the seven of them and the fake provider's data are each held
// by the handler's own tests.
func providerKindRows() []boundRow {
	secret := func(typ, path string, with []set, change ...set) boundRow {
		return boundRow{typ: typ, path: path, class: oam.RefusalExplicitSecret, with: with, change: change,
			strict: forbidSecrets, relax: allowSecrets}
	}
	password := "hunter2"
	rows := []boundRow{
		secret("metallb-bgppeer", "password", nil, set{at: "password", value: password}),
		secret("certificate", "keystores", nil,
			set{at: "keystores.jks.create", value: true}, set{at: "keystores.jks.password", value: password}),
		secret("certificate", "keystores", nil,
			set{at: "keystores.pkcs12.create", value: true}, set{at: "keystores.pkcs12.password", value: password}),
	}
	// The solver of an issuer is held in the two places it can carry a pod
	// template, each with a cpu and a memory row.
	for _, typ := range []string{"issuer", "clusterissuer"} {
		for _, route := range []string{"ingress", "gatewayHTTPRoute"} {
			resources := "acme.solvers[].http01." + route + ".podTemplate.spec.resources."
			rows = append(rows,
				boundRow{typ: typ, path: "acme", class: oam.RefusalResourceMaximum, relax: anyCPU,
					change: []set{{at: resources + "limits.cpu", value: "8"}}},
				boundRow{typ: typ, path: "acme", class: oam.RefusalResourceMaximum, relax: anyMemory,
					change: []set{{at: resources + "requests.memory", value: "8Gi"}}},
			)
		}
	}
	// A store holds the one provider it configures: the passing document names
	// the Scaleway provider with a secretRef, and the row writes the secret key
	// as a value in its place.
	// A row writes into the maps its `with` holds, so each row builds its own.
	scaleway := func() []set {
		return []set{{at: "provider", value: map[string]any{"scaleway": map[string]any{
			"region": "fr-par", "projectId": "project",
			"accessKey": map[string]any{"value": "access"},
			"secretKey": map[string]any{"secretRef": map[string]any{"name": "credentials", "key": "secret"}},
		}}}}
	}
	for _, typ := range []string{"secretstore", "clustersecretstore"} {
		rows = append(rows,
			secret(typ, "provider", nil, set{at: "provider.fake.data", value: []any{map[string]any{"key": "/db/password", "value": password}}}),
			secret(typ, "provider", scaleway(), set{at: "provider.scaleway.secretKey", value: map[string]any{"value": password}}),
		)
	}
	// What the operator would write instead of a Secret is an object the build
	// cannot see: a core Secret is refused under a policy that forbids explicit
	// secrets, and a kind the policy reads (a workload, a claim, an autoscaler) is
	// refused under any applied policy, no limit lifting it.
	for _, target := range []struct{ typ, at string }{
		{"externalsecret", "target"},
		{"clusterexternalsecret", "externalSecretSpec.target"},
	} {
		path, _, _ := strings.Cut(target.at, ".")
		manifest := target.at + ".manifest"
		rows = append(rows,
			secret(target.typ, path, nil, set{at: manifest, value: map[string]any{"apiVersion": "v1", "kind": "Secret"}}),
			boundRow{typ: target.typ, path: path, class: oam.RefusalUnreadableObject, unconditional: true,
				change: []set{{at: manifest, value: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment"}}}},
		)
	}
	return rows
}

// monitoringKindRows are the bounds of prometheus, alertmanager and thanosruler,
// whose handlers map one decoded spec onto the workload the operator runs
// (enforceMonitoringWorkloadPolicy). Their structured properties are open nodes,
// so a class sits on the node and a value below it raises it. The passing
// document names every image the operator would otherwise choose: the image, and
// a patch of each container the operator generates (boundDocument).
//
// The operator-filled memory request (alertmanager and thanosruler, 200Mi where
// `resources` names none) is a refusal with no authored value; it is declared on
// `resources`, which decides it, and the row shows the authored form only.
// A pod-level windowsOptions.hostProcess of a thanosruler is no row: it needs
// hostNetwork, which a ThanosRuler has no field for, so the build refuses it as
// invalid before the policy and no class is raised.
func monitoringKindRows() []boundRow {
	var rows []boundRow
	for _, typ := range []string{"prometheus", "alertmanager", "thanosruler"} {
		row := func(path string, class oam.RefusalClass, relax knob, change ...set) boundRow {
			return boundRow{typ: typ, path: path, class: class, change: change, relax: relax}
		}
		rows = append(rows,
			row("replicas", oam.RefusalReplicaMaximum, anyReplicas, set{at: "replicas", value: 4}),
			row("image", oam.RefusalRegistry, anyRegistry, set{at: "image", value: foreignImage}),
			// Unset, the operator chooses the image, which no allowlist can hold.
			row("image", oam.RefusalRegistry, anyRegistry, set{at: "image", drop: true}),
			row("storage", oam.RefusalStorageMaximum, anyStorage,
				set{at: "storage.volumeClaimTemplate.spec.resources.requests.storage", value: "1Ti"}),
			row("storage", oam.RefusalStorageMaximum, anyStorage,
				set{at: "storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage", value: "1Ti"},
				set{at: "storage.ephemeral.volumeClaimTemplate.spec.accessModes", value: []any{"ReadWriteOnce"}}),
			row("resources", oam.RefusalResourceMaximum, anyCPU, set{at: "resources.limits.cpu", value: "8"}),
			row("resources", oam.RefusalResourceMaximum, anyMemory, set{at: "resources.requests.memory", value: "8Gi"}),
			row("volumes", oam.RefusalHostPath, allowHostPath,
				set{at: "volumes[].hostPath", value: map[string]any{"path": "/data"}}, set{at: "volumes[].name", value: "host"}),
			row("volumes", oam.RefusalRegistry, anyRegistry,
				set{at: "volumes[].image", value: map[string]any{"reference": foreignImage}}, set{at: "volumes[].name", value: "img"}),
			row("volumes", oam.RefusalStorageMaximum, anyStorage,
				set{at: "volumes[].ephemeral", value: map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{
					"accessModes": []any{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]any{"storage": "1Ti"}}}}}},
				set{at: "volumes[].name", value: "scratch"}),
		)
		for _, list := range []string{"containers", "initContainers"} {
			if typ == "thanosruler" && list == "initContainers" {
				// No init container is generated: the row adds one, named so.
				add := func(class oam.RefusalClass, relax knob, image string, change ...set) boundRow {
					return row(list, class, relax, append(change, set{at: list + "[].name", value: "extra"}, set{at: list + "[].image", value: image})...)
				}
				rows = append(rows,
					add(oam.RefusalRegistry, anyRegistry, foreignImage, set{at: list + "[].image", value: foreignImage}),
					add(oam.RefusalResourceMaximum, anyCPU, boundImage, set{at: list + "[].resources.limits.cpu", value: "8"}),
					add(oam.RefusalPrivileged, allowPrivileged, boundImage, set{at: list + "[].securityContext.privileged", value: true}),
					add(oam.RefusalContainerCapability, anyCapability, boundImage,
						set{at: list + "[].securityContext.capabilities.add", value: []any{"NET_ADMIN"}}),
				)
				continue
			}
			rows = append(rows,
				row(list, oam.RefusalRegistry, anyRegistry, set{at: list + "[].image", value: foreignImage}),
				// Unpatched, the operator's own reloader image runs, which no allowlist can hold.
				row(list, oam.RefusalRegistry, anyRegistry, set{at: list, value: []any{}}),
				row(list, oam.RefusalResourceMaximum, anyCPU, set{at: list + "[].resources.limits.cpu", value: "8"}),
				row(list, oam.RefusalPrivileged, allowPrivileged, set{at: list + "[].securityContext.privileged", value: true}),
				row(list, oam.RefusalContainerCapability, anyCapability,
					set{at: list + "[].securityContext.capabilities.add", value: []any{"NET_ADMIN"}}),
			)
		}
		if typ != "thanosruler" {
			secContext := row("securityContext", oam.RefusalPrivileged, allowPrivileged,
				set{at: "securityContext.windowsOptions.hostProcess", value: true})
			// A HostProcess pod needs the host network, which the strict policy refuses first.
			secContext.with = []set{{at: "hostNetwork", value: true}}
			secContext.strict = allowNetwork
			rows = append(rows, secContext, row("hostNetwork", oam.RefusalHostNamespace, allowNetwork, set{at: "hostNetwork", value: true}))
		}
		secret := func(path string, change ...set) boundRow {
			r := row(path, oam.RefusalExplicitSecret, allowSecrets, change...)
			r.strict = forbidSecrets
			return r
		}
		if typ != "alertmanager" {
			rows = append(rows, secret("remoteWrite",
				set{at: "remoteWrite[].bearerToken", value: "hunter2"}, set{at: "remoteWrite[].url", value: "https://metrics.example/write"}))
		}
		if typ == "prometheus" {
			rows = append(rows,
				row("shards", oam.RefusalReplicaMaximum, anyReplicas, set{at: "shards", value: 4}),
				secret("remoteRead", set{at: "remoteRead[].bearerToken", value: "hunter2"}, set{at: "remoteRead[].url", value: "https://metrics.example/read"}),
				secret("apiserverConfig", set{at: "apiserverConfig.bearerToken", value: "hunter2"}, set{at: "apiserverConfig.host", value: "https://api.example"}),
				// The Thanos sidecar is another container the operator generates.
				boundRow{typ: typ, path: "thanos", class: oam.RefusalRegistry, relax: anyRegistry,
					with:   []set{{at: "thanos.version", value: "v0.39.2"}, {at: "thanos.image", value: boundImage}},
					change: []set{{at: "thanos.image", value: foreignImage}}},
				boundRow{typ: typ, path: "thanos", class: oam.RefusalResourceMaximum, relax: anyCPU,
					with:   []set{{at: "thanos.version", value: "v0.39.2"}, {at: "thanos.image", value: boundImage}},
					change: []set{{at: "thanos.resources.limits.cpu", value: "8"}}},
			)
		}
	}
	return rows
}

// boundRows lists every tested bound. TestBoundedBy_DeclaredAndTested holds it
// to what the registered schemas declare, both ways.
func boundRows() []boundRow {
	var rows []boundRow
	for _, k := range sharedKinds {
		rows = append(rows, sharedKindRows(k)...)
	}
	rows = append(rows, podRows("pod", "", "")...)
	for _, typ := range []string{"podtemplate", "replicaset", "replicationcontroller", "deployment", "daemonset", "statefulset", "job"} {
		rows = append(rows, podRows(typ, "template", "template.spec.")...)
	}
	// A CronJob holds its pod below the job template.
	rows = append(rows, podRows("cronjob", "jobTemplate", "jobTemplate.spec.template.spec.")...)
	for _, typ := range []string{"replicaset", "replicationcontroller", "deployment", "statefulset"} {
		rows = append(rows, boundRow{typ: typ, path: "replicas", class: oam.RefusalReplicaMaximum,
			change: []set{{at: "replicas", value: 9}}, relax: anyReplicas})
	}
	// The claim templates of a StatefulSet are an open node: the bound is on
	// each one's storage request, below the declared path.
	rows = append(rows, boundRow{typ: "statefulset", path: "volumeClaimTemplates", class: oam.RefusalStorageMaximum,
		change: []set{{at: "volumeClaimTemplates[].spec.resources.requests.storage", value: "1Ti"}}, relax: anyStorage})
	rows = append(rows, fluxAndManifestsRows()...)
	rows = append(rows, documentRows()...)
	rows = append(rows, helmTemplateRows()...)
	rows = append(rows, fluxSourceRows()...)
	rows = append(rows, postgresqlRows()...)
	rows = append(rows, cnpgClusterRows()...)
	rows = append(rows, cnpgOtherRows()...)
	rows = append(rows, coreKindRows()...)
	rows = append(rows, providerKindRows()...)
	rows = append(rows, monitoringKindRows()...)
	rows = append(rows,
		boundRow{trait: true, typ: "scaler", path: "maxReplicas", class: oam.RefusalReplicaMaximum,
			change: []set{{at: "maxReplicas", value: 9}}, relax: anyReplicas},
		// The trait's storage request is authored as size only.
		boundRow{trait: true, typ: "pvc", path: "size", class: oam.RefusalStorageMaximum,
			change: []set{{at: "size", value: "1Ti"}}, relax: anyStorage},
	)
	return rows
}

// boundDocument is the passing document a row of typ starts from, and the
// property map its change is written to: the component's, or the trait's on a
// deployment.
func boundDocument(t *testing.T, trait bool, typ string) (oam.Component, map[string]any) {
	t.Helper()
	container := func(name string) map[string]any { return map[string]any{"name": name, "image": boundImage} }
	if trait {
		props := map[string]map[string]any{
			"scaler": {"minReplicas": 1, "maxReplicas": 2},
			"pvc":    {"name": "data", "size": "1Gi"},
		}[typ]
		if props == nil {
			t.Fatalf("no passing document for trait %q", typ)
		}
		comp := oam.Component{Name: "app", Type: "deployment", Properties: rawDeploymentProps("app", boundImage, nil)}
		comp.Traits = []oam.Trait{{Type: typ, Properties: props}}
		return comp, props
	}
	pod := func() map[string]any {
		return map[string]any{"containers": []any{container("app")}, "initContainers": []any{container("init")}}
	}
	template := func() map[string]any {
		return map[string]any{"metadata": map[string]any{"labels": map[string]any{"tier": "web"}}, "spec": pod()}
	}
	var props map[string]any
	switch typ {
	case "pod":
		props = pod()
	case "podtemplate", "job":
		props = map[string]any{"template": template()}
	case "cronjob":
		props = map[string]any{"schedule": "0 2 * * *", "jobTemplate": map[string]any{"spec": map[string]any{"template": template()}}}
	case "replicaset", "deployment", "daemonset":
		props = map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}}, "template": template()}
	case "statefulset":
		// One claim template that passes, so the claim row changes its storage.
		props = map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}}, "template": template(),
			"volumeClaimTemplates": []any{map[string]any{
				"metadata": map[string]any{"name": "store"},
				"spec": map[string]any{
					"accessModes": []any{"ReadWriteOnce"},
					"resources":   map[string]any{"requests": map[string]any{"storage": "1Gi"}},
				},
			}}}
	case "replicationcontroller":
		props = map[string]any{"selector": map[string]any{"tier": "web"}, "template": template()}
	case "gitrepository":
		props = map[string]any{"url": "https://registry.example/org/repo", "ref": map[string]any{"branch": "main"}}
	case "helm":
		props = map[string]any{"chart": "app", "version": "1.0.0", "source": map[string]any{"url": "https://registry.example/charts"}}
	case "manifests":
		props = map[string]any{"inline": manifestsConfigMap}
	case "passthrough":
		props = map[string]any{"object": (document{body: manifestsConfigMap}).object()}
	case "helmtemplate":
		props = map[string]any{"chart": "app", "version": "1.0.0", "source": map[string]any{"url": "https://registry.example/charts"}}
	case "bucket":
		props = map[string]any{"bucketName": "charts", "endpoint": "registry.example"}
	case "helmrepository":
		props = map[string]any{"url": "https://registry.example/charts"}
	case "ocirepository":
		props = map[string]any{"url": "oci://registry.example/org/app"}
	case "oci":
		props = map[string]any{"source": map[string]any{"url": "oci://registry.example/org/app"}, "version": "1.0.0"}
	case "helmrelease":
		// The chart is written by the row (ociChartTemplate).
		props = map[string]any{}
	case "imagerepository":
		props = map[string]any{"image": "registry.example/org/app", "interval": "5m"}
	case "resourcesetinputprovider":
		props = map[string]any{"type": "OCIArtifactTag", "url": "oci://registry.example/org/app"}
	case "fluxcd-provider":
		props = map[string]any{"type": "generic"}
	case "crd":
		props = map[string]any{"url": crdURL}
	case "horizontalpodautoscaler":
		props = map[string]any{"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}, "maxReplicas": 2}
	case "persistentvolume":
		props = map[string]any{"capacity": map[string]any{"storage": "1Gi"}, "accessModes": []any{"ReadWriteOnce"}}
	case "persistentvolumeclaim":
		props = map[string]any{"size": "1Gi"}
	case "metallb-bgppeer":
		props = map[string]any{"myASN": 64500}
	case "certificate":
		props = map[string]any{"secretName": "tls", "issuerRef": map[string]any{"name": "issuer"}}
	case "issuer", "clusterissuer":
		props = map[string]any{"acme": map[string]any{"server": "https://acme.example/directory", "privateKeySecretRef": map[string]any{"name": "account"}}}
	case "secretstore", "clustersecretstore":
		props = map[string]any{"provider": map[string]any{"fake": map[string]any{"data": []any{}}}}
	case "externalsecret":
		props = map[string]any{}
	case "clusterexternalsecret":
		props = map[string]any{"externalSecretSpec": map[string]any{}}
	case "prometheus", "alertmanager", "thanosruler":
		// Every image the operator would choose is named, so the strict policy passes:
		// the image, and a patch of each generated container with one.
		props = map[string]any{"image": boundImage, "containers": []any{container("config-reloader")}}
		switch typ {
		case "prometheus":
			props["version"] = "v3.14.0"
			props["initContainers"] = []any{container("init-config-reloader")}
		case "alertmanager":
			props["version"] = "v0.30.0"
			props["initContainers"] = []any{container("init-config-reloader")}
		}
	case "postgresql", "cnpg-cluster":
		props = map[string]any{"imageName": boundImage}
	case "cnpg-pooler":
		props = map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{"image": boundImage},
			"template": map[string]any{"spec": pod()}}
	case "cnpg-objectstore":
		props = map[string]any{"configuration": map[string]any{"destinationPath": "s3://bucket/db/"}}
	case "cnpg-imagecatalog", "cnpg-clusterimagecatalog":
		props = map[string]any{"images": []any{map[string]any{"image": boundImage, "major": 16}}}
	default:
		i := slices.IndexFunc(sharedKinds, func(k sharedKind) bool { return k.typ == typ })
		if i < 0 {
			t.Fatalf("no passing document for component %q", typ)
		}
		k := sharedKinds[i]
		props = map[string]any{"image": boundImage, "initContainers": []any{container("init")}}
		if k.sidecars {
			props["sidecars"] = []any{container("side")}
		}
		for key, v := range k.extra {
			props[key] = v
		}
	}
	return oam.Component{Name: "app", Type: typ, Properties: props}, props
}

// declaredBounds returns the key of every class a registered schema declares.
func declaredBounds() []string {
	var keys []string
	var walk func(owner, path string, s oam.PropertySchema)
	walk = func(owner, path string, s oam.PropertySchema) {
		for _, c := range s.BoundedBy {
			keys = append(keys, owner+" "+path+" "+string(c))
		}
		for key, child := range s.Properties {
			walk(owner, path+"."+key, child)
		}
		if s.Items != nil {
			walk(owner, path+"[]", *s.Items)
		}
	}
	schemas := newTransformer().HandlerSchemas()
	for kind, set := range map[string]map[string]map[string]oam.PropertySchema{"component": schemas.Components, "trait": schemas.Traits, "policy": schemas.Policies} {
		for typ, props := range set {
			for key, s := range props {
				walk(kind+" "+typ, key, s)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// TestBoundedBy_DeclaredAndTested: every class a registered schema declares has
// a row, and every row is declared. A type with no row declares nothing.
func TestBoundedBy_DeclaredAndTested(t *testing.T) {
	tested := map[string]bool{}
	for _, r := range boundRows() {
		tested[r.key()] = true
	}
	if len(tested) == 0 {
		t.Fatal("no row: the test would hold nothing")
	}
	declared := map[string]bool{}
	for _, key := range declaredBounds() {
		declared[key] = true
		if !tested[key] {
			t.Errorf("declared and not tested: %s", key)
		}
	}
	var missing []string
	for key := range tested {
		if !declared[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		t.Errorf("tested and not declared: %s", key)
	}
}

// boundedClasses are the classes the declaring types can cause from a property.
// RefusalTraitCapability and RefusalObjectKind are about a type as a whole.
var boundedClasses = []oam.RefusalClass{
	oam.RefusalHostNamespace, oam.RefusalPrivileged, oam.RefusalHostPath, oam.RefusalContainerCapability,
	oam.RefusalRegistry, oam.RefusalResourceMaximum, oam.RefusalResourceDefaultLimit,
	oam.RefusalStorageMaximum, oam.RefusalReplicaMaximum,
	oam.RefusalExplicitSecret, oam.RefusalUnreadableObject,
}

// TestBoundedBy_Classes: a declared class is one of boundedClasses, and a
// property lists its classes once each, in RefusalClasses order.
func TestBoundedBy_Classes(t *testing.T) {
	order := oam.RefusalClasses()
	for _, c := range boundedClasses {
		if !slices.Contains(order, c) {
			t.Errorf("boundedClasses holds %q, which is no class of oam.RefusalClasses", c)
		}
	}
	var walk func(where string, s oam.PropertySchema)
	walk = func(where string, s oam.PropertySchema) {
		last := -1
		for _, c := range s.BoundedBy {
			if !slices.Contains(boundedClasses, c) {
				t.Errorf("%s declares %q, which is not in boundedClasses", where, c)
			}
			if i := slices.Index(order, c); i <= last {
				t.Errorf("%s lists %v, not once each in oam.RefusalClasses order", where, s.BoundedBy)
			} else {
				last = i
			}
		}
		for key, child := range s.Properties {
			walk(where+"."+key, child)
		}
		if s.Items != nil {
			walk(where+"[]", *s.Items)
		}
	}
	schemas := newTransformer().HandlerSchemas()
	for kind, set := range map[string]map[string]map[string]oam.PropertySchema{"component": schemas.Components, "trait": schemas.Traits, "policy": schemas.Policies} {
		for typ, props := range set {
			for key, s := range props {
				walk(kind+" "+typ+" "+key, s)
			}
		}
	}
}

// TestBoundedBy_Rows runs every row: the document passes, the one change is
// refused with the row's class, and the refusal is gone once the row's one
// setting is relaxed. The last step tells apart the properties that share a
// class, as hostNetwork, hostPID and hostIPC do.
func TestBoundedBy_Rows(t *testing.T) {
	for _, r := range boundRows() {
		t.Run(r.key()+" by "+r.change[0].at, func(t *testing.T) {
			run := func(p oam.Policy, changed bool) error {
				comp, props := boundDocument(t, r.trait, r.typ)
				for _, s := range r.with {
					s.apply(t, props)
				}
				if changed {
					for _, s := range r.change {
						s.apply(t, props)
					}
				}
				_, _, err := probe.Run(newTransformer(), []oam.Component{comp}, p)
				return err
			}
			if r.unconditional == (r.relax != nil) {
				t.Fatal("a row has a relax or is unconditional, never both and never neither")
			}
			// The value the row changes is the one at its declared path, or one
			// below it where the path is an open node.
			if at := r.change[0].at; at != r.path && !strings.HasPrefix(at, r.path+".") && !strings.HasPrefix(at, r.path+"[].") {
				t.Fatalf("the row changes %s, which is not at or below its path %s", at, r.path)
			}
			if err := run(boundsStrict(r.strict), false); err != nil {
				t.Fatalf("the document does not pass before the change: %v", err)
			}

			err := run(boundsStrict(r.strict), true)
			var v *oam.ViolationError
			var refusal *oam.PolicyRefusal
			switch {
			case err == nil:
				t.Fatalf("the change is not refused")
			case !errors.As(err, &v) || !errors.As(err, &refusal):
				t.Fatalf("the change fails with no refusal by the policy: %v", err)
			case v.Class != r.class:
				t.Fatalf("the change is refused with class %q, want %q: %v", v.Class, r.class, err)
			}

			if r.unconditional {
				// No setting lifts the refusal: it holds under a policy that limits nothing.
				var still *oam.ViolationError
				var stillRefusal *oam.PolicyRefusal
				err = run(&oam.NoopPolicy{}, true)
				if !errors.As(err, &still) || !errors.As(err, &stillRefusal) || still.Class != r.class {
					t.Errorf("under a policy that limits nothing: %v, want a refusal of class %q", err, r.class)
				}
				return
			}
			err = run(boundsStrict(r.strict, r.relax), true)
			refusal = nil
			switch {
			case r.invalidOnceAllowed == "" && err != nil:
				t.Errorf("the change still fails with the policy relaxed: %v", err)
			case r.invalidOnceAllowed != "" && (err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), r.invalidOnceAllowed)):
				t.Errorf("with the policy relaxed: %v, want an error that is no refusal by the policy and says %q", err, r.invalidOnceAllowed)
			}
		})
	}
}
