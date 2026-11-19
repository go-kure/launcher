package registry_test

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

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
}

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
)

// boundsStrict is the policy every row starts from: each limit set, nothing
// allowed.
func boundsStrict(knobs ...knob) *boundsPolicy {
	p := &boundsPolicy{
		maxReplicas: i32(3), maxCPU: "2", maxMemory: "1Gi", maxStorage: "10Gi",
		registries: []string{"registry.example"}, forbiddenCaps: []string{"NET_ADMIN"},
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

// sharedKind is one of the seven types built from the shared container and pod
// fragments (the `deployment` kind is not one since go-kure/launcher#1629: it
// holds the upstream pod template, and its rows are podRows on `template`). sizedVolumes says that a `pvc` volume of the type takes a `size`
// and generates its claim.
type sharedKind struct {
	typ                        string
	sidecars, replicas, claims bool
	sizedVolumes               bool
	extra                      map[string]any
}

var sharedKinds = []sharedKind{
	{typ: "role-deployment", sidecars: true, replicas: true},
	{typ: "webservice", sidecars: true, replicas: true, sizedVolumes: true},
	{typ: "worker", sidecars: true, replicas: true, sizedVolumes: true},
	{typ: "statefulset", sidecars: true, replicas: true, claims: true},
	{typ: "daemonset", sidecars: true},
	{typ: "job"},
	{typ: "cronjob", extra: map[string]any{"schedule": "0 2 * * *"}},
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
	if k.claims {
		long := row("volumeClaimTemplates[].resources.requests.storage", oam.RefusalStorageMaximum, anyStorage,
			set{at: "volumeClaimTemplates[].resources.requests.storage", value: "1Ti"})
		long.with = []set{{at: "volumeClaimTemplates[].size", drop: true}, {at: "volumeClaimTemplates[].resources.requests.storage", value: "1Gi"}}
		rows = append(rows,
			row("volumeClaimTemplates[].size", oam.RefusalStorageMaximum, anyStorage, set{at: "volumeClaimTemplates[].size", value: "1Ti"}),
			long,
		)
	}
	return rows
}

// podRows are the bounds of a pod written as the API type: at the top of a pod
// component with prefix "", and below the open template of the four kinds
// that hold one (podtemplate, replicaset, replicationcontroller and deployment), where every class is declared on that one node.
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

// boundRows lists every tested bound. TestBoundedBy_DeclaredAndTested holds it
// to what the registered schemas declare, both ways.
func boundRows() []boundRow {
	var rows []boundRow
	for _, k := range sharedKinds {
		rows = append(rows, sharedKindRows(k)...)
	}
	rows = append(rows, podRows("pod", "", "")...)
	for _, typ := range []string{"podtemplate", "replicaset", "replicationcontroller", "deployment"} {
		rows = append(rows, podRows(typ, "template", "template.spec.")...)
	}
	for _, typ := range []string{"replicaset", "replicationcontroller", "deployment"} {
		rows = append(rows, boundRow{typ: typ, path: "replicas", class: oam.RefusalReplicaMaximum,
			change: []set{{at: "replicas", value: 9}}, relax: anyReplicas})
	}
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
	case "podtemplate":
		props = map[string]any{"template": template()}
	case "replicaset", "deployment":
		props = map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}}, "template": template()}
	case "replicationcontroller":
		props = map[string]any{"selector": map[string]any{"tier": "web"}, "template": template()}
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
		if k.claims {
			props["volumeClaimTemplates"] = []any{map[string]any{"name": "store", "size": "1Gi", "mountPath": "/store"}}
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
// RefusalExplicitSecret and RefusalUnreadableObject are raised by types that
// declare no bound yet: the first type to declare one adds it here, with its
// row.
var boundedClasses = []oam.RefusalClass{
	oam.RefusalHostNamespace, oam.RefusalPrivileged, oam.RefusalHostPath, oam.RefusalContainerCapability,
	oam.RefusalRegistry, oam.RefusalResourceMaximum, oam.RefusalResourceDefaultLimit,
	oam.RefusalStorageMaximum, oam.RefusalReplicaMaximum,
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
			if r.relax == nil {
				t.Fatal("the row relaxes nothing")
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
