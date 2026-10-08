package components

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// workloadPolicy is an environment policy that allows what NoopPolicy denies
// and holds nothing else, until a field says otherwise.
type workloadPolicy struct {
	oam.NoopPolicy
	maxReplicas                             *int32
	maxCPU, maxStorage                      string
	allowed                                 []string
	noHostNetwork, noPrivileged, noHostPath bool
	forbidExplicitSecrets                   bool
}

func (p *workloadPolicy) MaxReplicas() *int32         { return p.maxReplicas }
func (p *workloadPolicy) MaxCPU() string              { return p.maxCPU }
func (p *workloadPolicy) MaxStorageSize() string      { return p.maxStorage }
func (p *workloadPolicy) AllowedRegistries() []string { return p.allowed }
func (p *workloadPolicy) AllowHostNetwork() bool      { return !p.noHostNetwork }
func (p *workloadPolicy) AllowHostPID() bool          { return true }
func (p *workloadPolicy) AllowHostIPC() bool          { return true }
func (p *workloadPolicy) AllowPrivileged() bool       { return !p.noPrivileged }
func (p *workloadPolicy) AllowHostPathVolumes() bool  { return !p.noHostPath }
func (p *workloadPolicy) AllowExplicitSecrets() bool  { return !p.forbidExplicitSecrets }

// heldField is the proof that one property is held to the environment policy:
// properties that build and pass under a policy that holds nothing, and are
// refused with the class under policy.
type heldField struct {
	props  map[string]any
	policy *workloadPolicy
	class  oam.RefusalClass
}

// monitoringWorkloadKind is one kind component of the Prometheus operator's
// API whose object makes the operator run pods, with its answer for every
// field the tests below derive from its spec type.
type monitoringWorkloadKind struct {
	monitoringKindRow
	handler oam.ComponentHandler

	// rulesLeft is the kind's list of the expression rules the API states on
	// the types the spec reaches, by the json path of the value each is stated
	// on ("" for the spec itself).
	rulesLeft map[string]string

	// podCopies maps each string of the operator's own types that the operator
	// copies, as written, to a pod field the API server defaults, to that
	// field's path in the pod kinds' list (podSpecDefaultedZeros), as the kind
	// cites where it lists it.
	podCopies map[string]string

	// around is the required fields an entry of a top-level list needs, by the
	// list's json name, so that a document authoring a defaulted field inside
	// that entry (TestMonitoringWorkloadKinds_DefaultedZeros) reaches the
	// field's refusal and, with the default, builds.
	around map[string]map[string]any

	// The fields of the spec that shape the pods, each in exactly one: held to
	// the policy, with the proof; owned, read by the ownership rules of pkg/oam,
	// with what they do; stated, not held, with the reason.
	held   map[string]heldField
	owned  map[string]string
	stated map[string]string

	// The fields of the API's own types that could hold a credential in the
	// clear, each in exactly one: literals, the paths the kind's workload
	// reports, refused under a policy that forbids explicit secrets;
	// notCredentials, with the reason the field holds none.
	literals       []string
	notCredentials map[string]string
}

// The reasons a field that shapes the pods is not held. The environment policy
// (oam.Policy) has dimensions for replicas, cpu, memory, storage, registries,
// host namespaces, hostPath volumes, privilege and capabilities, and for
// nothing else: a field outside them is not read on a pod kind either
// (enforcePodTemplatePolicy).
const (
	schedulingField = "says on which nodes and in which order the pods are scheduled: the environment policy has no dimension for it, on a pod kind either"
	rolloutField    = "says how the StatefulSet creates, replaces and removes its pods and claims: the environment policy has no dimension for it, on a statefulset either"
	podSettingField = "a setting of the pods the environment policy has no dimension for, on a pod kind either"
	pullPolicyField = "says when the image is pulled, not which image"
	claimMetadata   = "the labels and annotations of an ephemeral volume's claim template, which go onto a claim of a pod: not read for reserved keys and given no component label"
)

// The reasons a field named for a credential holds none.
const (
	secretNameField = "names Secrets of the namespace; their content is not in the object"
	keyPathField    = "a path in the container's file system, not key material"
	authTypeField   = "the name of a client-authentication policy of the TLS server, not a credential"
)

// monitoringWorkloadKinds lists those kinds. A new one joins with one row, and
// the tests below name every field of its spec type that has no answer.
var monitoringWorkloadKinds = []monitoringWorkloadKind{
	{
		monitoringKindRow: monitoringKindRow{"alertmanager", reflect.TypeFor[monitoringv1.AlertmanagerSpec](), alertmanagerKind.required, nil, alertmanagerKind.defaultedZerosFor(&monitoringv1.AlertmanagerSpec{}).fields},
		handler:           &AlertmanagerHandler{},
		rulesLeft:         alertmanagerRulesLeft,
		podCopies:         map[string]string{"schedulerName": "schedulerName", "imagePullPolicy": "containers[].imagePullPolicy"},
		held: map[string]heldField{
			"image": {
				props:  map[string]any{"image": "other.example/prometheus/alertmanager:v0.28.1", "version": "v0.28.1"},
				policy: &workloadPolicy{allowed: []string{"registry.example"}},
				class:  oam.RefusalRegistry,
			},
			"replicas": {
				props:  map[string]any{"replicas": 5},
				policy: &workloadPolicy{maxReplicas: new(int32(3))},
				class:  oam.RefusalReplicaMaximum,
			},
			"resources": {
				props:  map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "4"}}},
				policy: &workloadPolicy{maxCPU: "2"},
				class:  oam.RefusalResourceMaximum,
			},
			"storage": {
				props: map[string]any{"storage": map[string]any{"volumeClaimTemplate": map[string]any{
					"spec": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "100Gi"}}},
				}}},
				policy: &workloadPolicy{maxStorage: "10Gi"},
				class:  oam.RefusalStorageMaximum,
			},
			"volumes": {
				props:  map[string]any{"volumes": []any{map[string]any{"name": "host", "hostPath": map[string]any{"path": "/var/lib"}}}},
				policy: &workloadPolicy{noHostPath: true},
				class:  oam.RefusalHostPath,
			},
			"containers": {
				// A patch of the container the operator generates, which may
				// name no image.
				props:  map[string]any{"containers": []any{map[string]any{"name": "alertmanager", "securityContext": map[string]any{"privileged": true}}}},
				policy: &workloadPolicy{noPrivileged: true},
				class:  oam.RefusalPrivileged,
			},
			"initContainers": {
				// The spec's own image and both reloaders' are authored from the
				// allowed registry, so that the unset-image refusal does not
				// answer for this field.
				props: map[string]any{
					"image":      "registry.example/prometheus/alertmanager:v0.28.1",
					"version":    "v0.28.1",
					"containers": []any{map[string]any{"name": "config-reloader", "image": "registry.example/prometheus-operator/prometheus-config-reloader:v0.94.1"}},
					"initContainers": []any{
						map[string]any{"name": "init-config-reloader", "image": "registry.example/prometheus-operator/prometheus-config-reloader:v0.94.1"},
						map[string]any{"name": "init", "image": "other.example/team/init:1.0.0"},
					},
				},
				policy: &workloadPolicy{allowed: []string{"registry.example"}},
				class:  oam.RefusalRegistry,
			},
			"securityContext": {
				// The API requires the host network of a HostProcess pod.
				props:  map[string]any{"hostNetwork": true, "securityContext": map[string]any{"windowsOptions": map[string]any{"hostProcess": true}}},
				policy: &workloadPolicy{noPrivileged: true},
				class:  oam.RefusalPrivileged,
			},
			"hostNetwork": {
				props:  map[string]any{"hostNetwork": true},
				policy: &workloadPolicy{noHostNetwork: true},
				class:  oam.RefusalHostNamespace,
			},
		},
		owned: map[string]string{
			"podMetadata":                          "read by the wrapper every component's objects pass (pkg/oam), which refuses a key the consumer reserves in its labels and annotations and writes nothing there: the pods carry the component label only where the author writes it",
			"storage.volumeClaimTemplate.metadata": "read by the wrapper every component's objects pass (pkg/oam), which refuses a key the consumer reserves in its labels and annotations and writes nothing there: the operator copies them onto its StatefulSet's volume claim template",
		},
		stated: map[string]string{
			"affinity":                             schedulingField,
			"nodeSelector":                         schedulingField,
			"tolerations":                          schedulingField,
			"topologySpreadConstraints":            schedulingField,
			"schedulerName":                        schedulingField,
			"priorityClassName":                    schedulingField,
			"minReadySeconds":                      rolloutField,
			"podManagementPolicy":                  rolloutField,
			"updateStrategy":                       rolloutField,
			"persistentVolumeClaimRetentionPolicy": rolloutField,
			"serviceName":                          "names the governing Service of the StatefulSet; launcher creates none for it and reads none",
			"terminationGracePeriodSeconds":        podSettingField,
			"automountServiceAccountToken":         podSettingField,
			"serviceAccountName":                   podSettingField,
			"dnsConfig":                            podSettingField,
			"dnsPolicy":                            podSettingField,
			"enableServiceLinks":                   podSettingField,
			"hostAliases":                          podSettingField,
			"hostUsers":                            podSettingField,
			"imagePullPolicy":                      pullPolicyField,
			"imagePullSecrets":                     "names the Secrets holding registry credentials, not an image",
			"volumeMounts":                         "mounts, into the alertmanager container, volumes that are held where they are declared (volumes)",
			"storage.ephemeral.volumeClaimTemplate.metadata":   claimMetadata,
			"volumes[].ephemeral.volumeClaimTemplate.metadata": claimMetadata,
		},
		notCredentials: map[string]string{
			"configSecret": secretNameField,
			"secrets":      secretNameField,
			"alertmanagerConfiguration.global.httpConfig.oauth2.tokenUrl": "the URL tokens are fetched from; the client's secret is the key of a Secret (clientSecret)",
			"alertmanagerConfiguration.global.smtp.authUsername":          "a user name; the password is the key of a Secret (authPassword)",
			"alertmanagerConfiguration.global.smtp.authIdentity":          "the identity of PLAIN authentication, a name; the password is the key of a Secret (authPassword)",
			"clusterTLS.server.keyFile":                                   keyPathField,
			"web.tlsConfig.keyFile":                                       keyPathField,
			"clusterTLS.server.clientAuthType":                            authTypeField,
			"web.tlsConfig.clientAuthType":                                authTypeField,
		},
	},
	{
		monitoringKindRow: monitoringKindRow{"thanosruler", reflect.TypeFor[monitoringv1.ThanosRulerSpec](), thanosRulerKind.required, nil, thanosRulerKind.defaultedZeros.fields},
		handler:           &ThanosRulerHandler{},
		rulesLeft:         thanosRulerRulesLeft,
		podCopies:         map[string]string{"schedulerName": "schedulerName", "imagePullPolicy": "containers[].imagePullPolicy"},
		around:            map[string]map[string]any{"remoteWrite": {"url": "https://remote.example/api/v1/write"}},
		held: map[string]heldField{
			"image": {
				props:  map[string]any{"image": "other.example/thanos/thanos:v0.39.2"},
				policy: &workloadPolicy{allowed: []string{"registry.example"}},
				class:  oam.RefusalRegistry,
			},
			"replicas": {
				props:  map[string]any{"replicas": 5},
				policy: &workloadPolicy{maxReplicas: new(int32(3))},
				class:  oam.RefusalReplicaMaximum,
			},
			"resources": {
				props:  map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "4"}}},
				policy: &workloadPolicy{maxCPU: "2"},
				class:  oam.RefusalResourceMaximum,
			},
			"storage": {
				props: map[string]any{"storage": map[string]any{"volumeClaimTemplate": map[string]any{
					"spec": map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "100Gi"}}},
				}}},
				policy: &workloadPolicy{maxStorage: "10Gi"},
				class:  oam.RefusalStorageMaximum,
			},
			"volumes": {
				props:  map[string]any{"volumes": []any{map[string]any{"name": "host", "hostPath": map[string]any{"path": "/var/lib"}}}},
				policy: &workloadPolicy{noHostPath: true},
				class:  oam.RefusalHostPath,
			},
			"containers": {
				// A patch of the container the operator generates, which may
				// name no image.
				props:  map[string]any{"containers": []any{map[string]any{"name": "thanos-ruler", "securityContext": map[string]any{"privileged": true}}}},
				policy: &workloadPolicy{noPrivileged: true},
				class:  oam.RefusalPrivileged,
			},
			"initContainers": {
				// The spec's own image and the reloader's are authored from the
				// allowed registry, so that the unset-image refusal does not
				// answer for this field.
				props: map[string]any{
					"image":          "registry.example/thanos/thanos:v0.39.2",
					"containers":     []any{map[string]any{"name": "config-reloader", "image": "registry.example/prometheus-operator/prometheus-config-reloader:v0.94.1"}},
					"initContainers": []any{map[string]any{"name": "init", "image": "other.example/team/init:1.0.0"}},
				},
				policy: &workloadPolicy{allowed: []string{"registry.example"}},
				class:  oam.RefusalRegistry,
			},
		},
		owned: map[string]string{
			"podMetadata": "read by the wrapper every component's objects pass (pkg/oam), which refuses a key the consumer reserves in its labels and annotations and writes nothing there: the pods carry the component label only where the author writes it",
		},
		stated: map[string]string{
			"affinity":                             schedulingField,
			"nodeSelector":                         schedulingField,
			"tolerations":                          schedulingField,
			"topologySpreadConstraints":            schedulingField,
			"schedulerName":                        schedulingField,
			"priorityClassName":                    schedulingField,
			"minReadySeconds":                      rolloutField,
			"podManagementPolicy":                  rolloutField,
			"updateStrategy":                       rolloutField,
			"serviceName":                          "names the governing Service of the StatefulSet; launcher creates none for it and reads none",
			"terminationGracePeriodSeconds":        podSettingField,
			"serviceAccountName":                   podSettingField,
			"dnsConfig":                            podSettingField,
			"dnsPolicy":                            podSettingField,
			"enableServiceLinks":                   podSettingField,
			"hostAliases":                          podSettingField,
			"hostUsers":                            podSettingField,
			"imagePullPolicy":                      pullPolicyField,
			"imagePullSecrets":                     "names the Secrets holding registry credentials, not an image",
			"securityContext":                      "its windowsOptions.hostProcess, the part an EnvironmentPolicy holds on other kinds, is refused before any policy: the API requires hostNetwork: true of a HostProcess pod, and a ThanosRuler has no hostNetwork",
			"volumeMounts":                         "mounts, into the thanos-ruler container, volumes that are held where they are declared (volumes)",
			"storage.volumeClaimTemplate.metadata": claimMetadata,
			"storage.ephemeral.volumeClaimTemplate.metadata":   claimMetadata,
			"volumes[].ephemeral.volumeClaimTemplate.metadata": claimMetadata,
		},
		literals: []string{"remoteWrite[].bearerToken"},
		notCredentials: map[string]string{
			"grpcServerTlsConfig.keyFile":                 keyPathField,
			"remoteWrite[].authorization.credentialsFile": keyPathField,
			"remoteWrite[].bearerTokenFile":               keyPathField,
			"remoteWrite[].tlsConfig.keyFile":             keyPathField,
			"remoteWrite[].oauth2.tokenUrl":               "the URL tokens are fetched from; the client's secret is the key of a Secret (clientSecret)",
			"web.tlsConfig.keyFile":                       keyPathField,
			"web.tlsConfig.clientAuthType":                authTypeField,
		},
	},
}

// monitoringWorkloadPodOmitted is the answer of those kinds for the pod-spec
// fields upstream marks required and leaves out when empty, in the containers
// and volumes their specs list. It is podSpecOmitted without the ephemeral
// containers, which these specs do not have.
func monitoringWorkloadPodOmitted() map[string]string {
	const reason = "marked required in the upstream source and not checked: the linked module ships no CRD that shows the API server refuses the object without it, and the field is behind a feature gate of the cluster"
	out := map[string]string{}
	for _, list := range []string{"containers", "initContainers"} {
		out[list+"[].restartPolicyRules[].action"] = reason
		out[list+"[].restartPolicyRules[].exitCodes.operator"] = reason
	}
	out["volumes[].projected.sources[].podCertificate.signerName"] = reason
	out["volumes[].projected.sources[].podCertificate.keyType"] = reason
	return out
}

// markerPackageDir is the directory of the Go package pkg in the module of
// markerModules that holds it.
func markerPackageDir(t *testing.T, pkg string) (string, bool) {
	t.Helper()
	for _, module := range markerModules {
		if pkg == module || strings.HasPrefix(pkg, module+"/") {
			return filepath.Join(linkedModuleDir(t, module), filepath.FromSlash(strings.TrimPrefix(pkg, module))), true
		}
	}
	return "", false
}

// packageRules is the expression rules the source of one Go package states to
// the CRD generator: on a type, by its name, and on a struct field, by
// "<type>.<Go field name>". Each rule is its expression followed by its
// message, so that a change to either is a change of the rule.
type packageRules struct {
	types, fields map[string][]string
	// markers counts the rule markers of the package's source, whether or not
	// they were attributed to a type or a field.
	markers int
}

const ruleMarkerPrefix = "+kubebuilder:validation:XValidation:"

var (
	ruleMarkerRule    = regexp.MustCompile(`(?:^|,)rule="((?:[^"\\]|\\.)*)"`)
	ruleMarkerMessage = regexp.MustCompile(`,message="((?:[^"\\]|\\.)*)"`)
)

// rulesOf reads the rule markers of one comment, each as "<expression>
// (<message>)", or as the marker's whole text where it names no expression.
func rulesOf(doc *ast.CommentGroup) []string {
	if doc == nil {
		return nil
	}
	var out []string
	for _, comment := range doc.List {
		line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
		rule, ok := strings.CutPrefix(line, ruleMarkerPrefix)
		if !ok {
			continue
		}
		if expr := ruleMarkerRule.FindStringSubmatch(rule); expr != nil {
			if msg := ruleMarkerMessage.FindStringSubmatch(rule); msg != nil {
				rule = expr[1] + " (" + msg[1] + ")"
			} else {
				rule = expr[1]
			}
		}
		out = append(out, rule)
	}
	return out
}

// packageRuleMarkers reads the rule markers of the Go package in dir.
func packageRuleMarkers(t *testing.T, dir string) packageRules {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the package in %s: %v", dir, err)
	}
	rules := packageRules{types: map[string][]string{}, fields: map[string][]string{}}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		rules.markers += strings.Count(string(data), ruleMarkerPrefix)
		file, err := parser.ParseFile(fset, name, data, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typ := spec.(*ast.TypeSpec)
				if found := slices.Concat(rulesOf(gen.Doc), rulesOf(typ.Doc)); len(found) > 0 {
					rules.types[typ.Name.Name] = found
				}
				st, ok := typ.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					found := rulesOf(field.Doc)
					if len(found) == 0 {
						continue
					}
					names := make([]string, 0, len(field.Names))
					for _, ident := range field.Names {
						names = append(names, ident.Name)
					}
					if len(names) == 0 {
						names = append(names, embeddedTypeName(field.Type))
					}
					for _, fieldName := range names {
						rules.fields[typ.Name.Name+"."+fieldName] = found
					}
				}
			}
		}
	}
	return rules
}

// attributed counts the rules read onto a type or a field.
func (r packageRules) attributed() int {
	n := 0
	for _, found := range r.types {
		n += len(found)
	}
	for _, found := range r.fields {
		n += len(found)
	}
	return n
}

// elementType is the type a field holds behind any pointer, list or map.
func elementType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	return typ
}

// reachedRules is every expression rule stated on a type or a field the
// encoding of typ reaches, by the json path of the value the rule is stated
// on: "" for typ itself and for a struct it embeds. A reached type of a
// package outside markerModules fails the test: its rules were not read.
func reachedRules(t *testing.T, typ reflect.Type) map[string][]string {
	t.Helper()
	packages := map[string]packageRules{}
	of := func(pkg string) packageRules {
		rules, loaded := packages[pkg]
		if loaded || pkg == "" {
			return rules
		}
		dir, ok := markerPackageDir(t, pkg)
		if !ok {
			t.Errorf("the walk reaches a type of %s, whose source is not read", pkg)
		} else {
			rules = packageRuleMarkers(t, dir)
			if rules.attributed() != rules.markers {
				t.Errorf("%s states %d rule markers and %d were read onto a type or a field", pkg, rules.markers, rules.attributed())
			}
		}
		packages[pkg] = rules
		return rules
	}
	out := map[string][]string{}
	add := func(path string, found []string) {
		for _, rule := range found {
			if !slices.Contains(out[path], rule) {
				out[path] = append(out[path], rule)
			}
		}
	}
	walkKindFields(typ, func(kindField) bool { return true }, func(f kindField) {
		parent := ""
		if at := strings.LastIndex(f.path, "."); at >= 0 {
			parent = f.path[:at]
		}
		add(parent, of(f.owner.PkgPath()).types[f.owner.Name()])
		add(f.path, of(f.owner.PkgPath()).fields[f.owner.Name()+"."+f.field.Name])
		if elem := elementType(f.field.Type); elem.Name() != "" {
			path := f.path
			for at := f.field.Type; at != elem; at = at.Elem() {
				switch at.Kind() {
				case reflect.Slice, reflect.Array:
					path += "[]"
				case reflect.Map:
					path += "{}"
				default:
					// A pointer adds nothing to the path.
				}
			}
			add(path, of(elem.PkgPath()).types[elem.Name()])
		}
	})
	return out
}

// TestMonitoringWorkloadKinds_RulesListed derives, from the markers of the
// linked modules' source, every expression rule the API states on a type or a
// field the kind's spec reaches, and holds the kind's list of the rules it
// leaves to the API server to them. The linked module ships no CRD, so none is
// checked: a dependency bump that adds a rule to a reached type, drops one or
// rewords one fails here, naming it.
func TestMonitoringWorkloadKinds_RulesListed(t *testing.T) {
	// Vacuity guard: the markers are read, on a type the kinds do not reach
	// and on one they do.
	dir, _ := markerPackageDir(t, reflect.TypeFor[monitoringv1.AlertmanagerSpec]().PkgPath())
	all := packageRuleMarkers(t, dir)
	if len(all.types["Sigv4"]) != 1 || len(all.types["StatefulSetUpdateStrategy"]) != 1 {
		t.Fatalf("the rule markers of the monitoring package are not being read: %v", all.types)
	}
	for _, kind := range monitoringWorkloadKinds {
		t.Run(kind.component, func(t *testing.T) {
			derived := map[string]string{}
			for path, rules := range reachedRules(t, kind.typ) {
				derived[path] = strings.Join(rules, "; ")
			}
			t.Logf("expression rules on the types %s reaches: %v", kind.typ, derived)
			if !maps.Equal(derived, kind.rulesLeft) {
				t.Errorf("expression rules left to the API server:\n  derived %v\n  listed  %v", derived, kind.rulesLeft)
			}
		})
	}
}

// swaggerDocs is the field descriptions a Kubernetes API type publishes, by
// json name, and whether it publishes any.
func swaggerDocs(typ reflect.Type) (map[string]string, bool) {
	m := reflect.New(typ).Elem().MethodByName("SwaggerDoc")
	if !m.IsValid() {
		return nil, false
	}
	doc, ok := m.Call(nil)[0].Interface().(map[string]string)
	return doc, ok
}

// TestMonitoringWorkloadKinds_DefaultedZeros derives the fields of each kind's
// spec type on which an authored 0, false or "" would be silently replaced, and
// holds the kind's list to them (monitoringWorkloadDefaultedZeros). Such a
// field is a number, a boolean or a string that is no pointer and is omitted
// when zero, and that the API defaults to something else: by a default marker
// in the source of its type; for a number or a boolean of a Kubernetes type,
// by the default its published field description states
// (TestPodSpecDefaultedZeros_MatchFieldDocs, whose limits apply); for a string
// of a Kubernetes type, by the API server's defaulting code in the vendored
// excerpt, which those types state only in free text: a field at a path of the
// pod spec that SetObjectDefaults_Pod writes, with the default the pod kinds'
// list gives it (TestKubernetesDefaulters_MatchVendoredSource holds that list
// to the same code); and for a string of the operator's own types that the
// operator copies to a pod field (podCopies), by that pod field's. A
// Kubernetes string outside the pod spec's paths (a claim template's, say) is
// not read. A field of that shape whose source is not read, or a Kubernetes
// number or boolean that publishes no description, fails here. Each listed
// field is shown refused on the kind's handler: properties that author a 0 or
// a "" there do not build, and the same properties with a 2 or the default
// do. A container port's hostPort, which the API server defaults only under
// hostNetwork, is shown by TestMonitoringWorkloadKinds_HostNetworkHostPort.
func TestMonitoringWorkloadKinds_DefaultedZeros(t *testing.T) {
	src := markerAPISource(t)
	podWrites := loadVendoredK8s(t).podSpecWrites(t, k8sCoreV1, "SetObjectDefaults_Pod", reflect.TypeFor[corev1.Pod](), "spec.")
	podRows := podSpecDefaultedZeros("", nil).fields
	// podDefault is the pod kinds' default for the pod spec's field at path,
	// where the API server's defaulting code writes it.
	podDefault := func(t *testing.T, path string) (string, bool) {
		t.Helper()
		if podWrites[path] == nil {
			return "", false
		}
		def, ok := podRows[path]
		if !ok {
			t.Errorf("the API server defaults the pod spec's %s, which the pod kinds' list does not hold", path)
		}
		return def, ok
	}
	for _, kind := range monitoringWorkloadKinds {
		t.Run(kind.component, func(t *testing.T) {
			derived := map[string]string{}
			scalars, stated, kubeStrings := 0, 0, 0
			copied := map[string]bool{}
			walkKindFields(kind.typ, src.required, func(f kindField) {
				if !f.omitemptyScalar() {
					return
				}
				if pod, ok := kind.podCopies[f.path]; ok {
					copied[f.path] = true
					def, ok := podDefault(t, pod)
					if !ok {
						t.Errorf("%s is listed in podCopies as %s, which the API server does not default", f.path, pod)
						return
					}
					derived[f.path] = def
					return
				}
				if f.field.Type.Kind() == reflect.String && strings.HasPrefix(f.owner.PkgPath(), "k8s.io/") {
					kubeStrings++
					if def, ok := podDefault(t, f.path); ok {
						derived[f.path] = def
					}
					return
				}
				scalars++
				if !src.known(f) {
					t.Errorf("%s (%s.%s) is omitted when zero, and its default cannot be read: the source of its type is not", f.path, f.owner, f.field.Name)
					return
				}
				def, ok := src.def(f)
				if docs, published := swaggerDocs(f.owner); !ok && published {
					name, _, _ := strings.Cut(f.field.Tag.Get("json"), ",")
					if docs[name] == "" {
						t.Errorf("%s (%s.%s) has no field description to read a default from", f.path, f.owner, f.field.Name)
						return
					}
					if m := docDefault.FindStringSubmatch(docs[name]); m != nil {
						def, ok = strings.ToLower(m[1]), true
					}
				}
				if !ok {
					return
				}
				stated++
				if literal := f.defaultLiteral(def); !crdDefaultIsZero(literal) {
					derived[f.path] = literal
				}
			})
			t.Logf("walked %d omitempty numbers, booleans and strings of the operator's types, %d with a default, and %d strings of the Kubernetes types; %d fields not zero", scalars, stated, kubeStrings, len(derived))
			// Vacuity guards: the walk reaches the pod types, reads their
			// field descriptions and finds the API server's string defaults
			// on the containers and the volumes.
			if scalars < 40 || stated < 15 || kubeStrings < 20 {
				t.Fatalf("found %d omitempty scalars under %s, %d of them with a default, and %d Kubernetes strings; want >= 40, >= 15 and >= 20", scalars, kind.typ, stated, kubeStrings)
			}
			for _, path := range []string{"containers[].ports[].protocol", "volumes[].rbd.pool"} {
				if _, ok := derived[path]; !ok {
					t.Fatalf("derived no default for %s: the excerpt's writes are not being read", path)
				}
			}
			for path := range kind.podCopies {
				if !copied[path] {
					t.Errorf("stale podCopies %s: not an omitempty string of %s", path, kind.typ)
				}
			}
			if !maps.Equal(derived, kind.defaulted) {
				t.Errorf("fields on which an authored zero is replaced:\n  derived %v\n  listed  %v", derived, kind.defaulted)
			}
			for path, def := range derived {
				build := func(value any) error {
					props := authoredAt(path, value)
					// A listed container that patches none the operator
					// generates must name an image (validateMonitoringWorkload).
					for _, list := range []string{"containers", "initContainers"} {
						if entries, ok := props[list].([]any); ok {
							entries[0].(map[string]any)["image"] = "registry.example/team/probe:1.0.0"
						}
					}
					for list, fields := range kind.around {
						if strings.HasPrefix(path, list+"[].") {
							maps.Copy(props[list].([]any)[0].(map[string]any), fields)
						}
					}
					_, err := kind.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: kind.component, Properties: props}, "data")
					return err
				}
				var zero, other any = 0, 2
				zeroText := "0"
				switch unquoted, err := strconv.Unquote(def); {
				case def == imagePullPolicyDefault:
					zero, other, zeroText = "", string(corev1.PullIfNotPresent), `""`
				case err == nil:
					zero, other, zeroText = "", unquoted, `""`
				}
				want := strings.ReplaceAll(path, "[]", "[0]") + ": " + zeroText + " cannot be carried by the Prometheus operator API types"
				if err := build(zero); err == nil || !strings.HasPrefix(err.Error(), want) || !strings.Contains(err.Error(), "its default "+def+")") {
					t.Errorf("%s: an authored %s gave %v, want a refusal starting %q that names the default %s", path, zeroText, err, want, def)
				}
				if err := build(other); err != nil {
					t.Errorf("%s: an authored %v is refused: %v", path, other, err)
				}
			}
		})
	}
}

// TestMonitoringWorkloadKinds_HostNetworkHostPort: the operator copies
// hostNetwork to the pods, and with it true the API server sets an omitted
// hostPort of a container port to the port's containerPort. So under an
// authored hostNetwork: true, a hostPort of 0 on a port of a listed container
// or init container is refused, and without hostNetwork the same port builds.
// A kind whose spec has no hostNetwork, such as thanosruler, only builds it.
func TestMonitoringWorkloadKinds_HostNetworkHostPort(t *testing.T) {
	for _, kind := range monitoringWorkloadKinds {
		hasHostNetwork := slices.ContainsFunc(reflect.VisibleFields(kind.typ), func(f reflect.StructField) bool {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			return name == "hostNetwork"
		})
		for _, list := range []string{"containers", "initContainers"} {
			t.Run(kind.component+"/"+list, func(t *testing.T) {
				build := func(hostNetwork bool) error {
					props := map[string]any{list: []any{map[string]any{
						"name":  "sidecar",
						"image": "registry.example/team/sidecar:1.0.0",
						"ports": []any{map[string]any{"containerPort": 9000, "hostPort": 0}},
					}}}
					if hostNetwork {
						props["hostNetwork"] = true
					}
					_, err := kind.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: kind.component, Properties: props}, "data")
					return err
				}
				want := list + "[0].ports[0].hostPort: 0 cannot be carried by the Prometheus operator API types (the field is omitted when zero, so the API server would apply its default " + hostNetworkHostPortDefault + ")"
				if !hasHostNetwork {
					if _, ok := kind.defaulted[list+"[].ports[].hostPort"]; ok {
						t.Errorf("%s lists %s[].ports[].hostPort, and its spec has no hostNetwork", kind.component, list)
					}
				} else if err := build(true); err == nil || err.Error() != want {
					t.Errorf("under hostNetwork: err = %v\nwant %s", err, want)
				}
				if err := build(false); err != nil {
					t.Errorf("without hostNetwork: %v", err)
				}
			})
		}
	}
}

// authoredAt is properties that hold value at the json path, in the first
// element of each list on it.
func authoredAt(path string, value any) map[string]any {
	segments := strings.Split(path, ".")
	for i := len(segments) - 1; i >= 0; i-- {
		name, list := strings.CutSuffix(segments[i], "[]")
		if list {
			value = []any{value}
		}
		value = map[string]any{name: value}
	}
	return value.(map[string]any)
}

// jsonNames is the json names of the fields of a struct type, embedded
// structs promoted.
func jsonNames(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case f.Anonymous && name == "":
			maps.Copy(out, jsonNames(elementType(f.Type)))
		case name != "" && name != "-":
			out[name] = true
		}
	}
	return out
}

// podShapingFields is the fields of typ that shape the pods the operator
// runs: a top-level field under the json name of a field of a pod spec, a
// container or a StatefulSet's spec, or holding the operator's storage block
// or its embedded metadata; and, at any depth, a field that holds object
// metadata, which the operator copies onto what it creates. That is the whole
// of what is recognised: a top-level field that shapes the pods under a name
// no Kubernetes workload type uses is not.
func podShapingFields(typ reflect.Type) []string {
	names := jsonNames(reflect.TypeFor[corev1.PodSpec]())
	maps.Copy(names, jsonNames(reflect.TypeFor[corev1.Container]()))
	maps.Copy(names, jsonNames(reflect.TypeFor[appsv1.StatefulSetSpec]()))
	metadata := []reflect.Type{reflect.TypeFor[monitoringv1.EmbeddedObjectMetadata](), reflect.TypeFor[metav1.ObjectMeta]()}
	var out []string
	walkKindFields(typ, func(kindField) bool { return true }, func(f kindField) {
		elem := elementType(f.field.Type)
		switch {
		case slices.Contains(metadata, elem):
		case strings.Contains(f.path, "."):
			return
		case names[f.path] || elem == reflect.TypeFor[monitoringv1.StorageSpec]():
		default:
			return
		}
		out = append(out, f.path)
	})
	slices.Sort(out)
	return out
}

// TestMonitoringWorkloadKinds_PodFieldsHeldOrListed derives, from each kind's
// spec type, the fields podShapingFields recognises as shaping the pods the
// operator runs (not one under a name of the operator's own, such as secrets
// or configMaps), and fails on one that is neither held to the
// environment policy, nor read by the ownership rules, nor stated with the
// reason it is neither. A dependency bump that adds such a field fails here,
// naming it. A held field is proven held: properties with it build, pass under
// a policy that holds nothing, and are refused with the class under one that
// does.
func TestMonitoringWorkloadKinds_PodFieldsHeldOrListed(t *testing.T) {
	for _, kind := range monitoringWorkloadKinds {
		t.Run(kind.component, func(t *testing.T) {
			candidates := podShapingFields(kind.typ)
			t.Logf("fields of %s that shape the pods: %v", kind.typ, candidates)
			if len(candidates) < 20 {
				t.Fatalf("found %d fields of %s that shape the pods; the walk is broken", len(candidates), kind.typ)
			}
			answers := slices.Concat(slices.Collect(maps.Keys(kind.held)), slices.Collect(maps.Keys(kind.owned)), slices.Collect(maps.Keys(kind.stated)))
			slices.Sort(answers)
			if !slices.Equal(answers, candidates) {
				var missing, stale []string
				for _, path := range candidates {
					if !slices.Contains(answers, path) {
						missing = append(missing, path)
					}
				}
				for _, path := range answers {
					if !slices.Contains(candidates, path) {
						stale = append(stale, path)
					}
				}
				t.Errorf("each field that shapes the pods is answered once: without an answer %v; answered and not such a field, or answered twice: %v", missing, stale)
			}
			for path, reason := range kind.owned {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s is owned without saying what the ownership rules do", path)
				}
			}
			for path, reason := range kind.stated {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s is stated without a reason", path)
				}
			}
			for path, held := range kind.held {
				if _, authored := held.props[path]; !authored {
					t.Errorf("%s: the properties that prove it held do not author it", path)
				}
				config, err := kind.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: kind.component, Properties: held.props}, "data")
				if err != nil {
					t.Errorf("%s: the properties do not build: %v", path, err)
					continue
				}
				enforceable, ok := config.(oam.Enforceable)
				if !ok {
					t.Fatalf("the config of %s takes no policy", kind.component)
				}
				if err := enforceable.ApplyPolicy(&workloadPolicy{}); err != nil {
					t.Errorf("%s: refused under a policy that holds nothing: %v", path, err)
				}
				err = enforceable.ApplyPolicy(held.policy)
				var refusal *oam.PolicyRefusal
				if !errors.As(err, &refusal) || refusal.Class != held.class {
					t.Errorf("%s: err = %v, want a refusal of class %q", path, err, held.class)
					continue
				}
				// The refusal is the field's: without it, the same properties
				// are not refused the same way.
				refused := err.Error()
				without := maps.Clone(held.props)
				delete(without, path)
				config, err = kind.handler.ToApplicationConfig(&oam.Component{Name: "fast", Type: kind.component, Properties: without}, "data")
				if err != nil {
					t.Errorf("%s: the properties without it do not build: %v", path, err)
					continue
				}
				if other := config.(oam.Enforceable).ApplyPolicy(held.policy); other != nil && other.Error() == refused {
					t.Errorf("%s: the properties are refused the same way without it (%v): the refusal is not the field's", path, other)
				}
			}
		})
	}
}

// credentialName is what the json name of a field that could hold a
// credential says.
var credentialName = regexp.MustCompile(`(?i)passw|secret|token|credential|key|auth`)

// isTextField says the field holds text of the author's: a string, behind any
// pointer, list or map, or bytes.
func isTextField(typ reflect.Type) bool {
	if typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Uint8 {
		return true
	}
	return elementType(typ).Kind() == reflect.String
}

// TestMonitoringWorkloadKinds_CredentialsHeldOrListed derives, from each
// kind's spec type, every field of the operator's own types that holds text
// and is named for a credential (credentialName), and fails on one that is
// neither reported by the kind's workload as a credential in the clear nor
// listed with the reason it holds none. The refusal a reported one yields is
// shown in TestMonitoringWorkload_HeldOnASyntheticValue, and on the one kind
// that reports one, through its handler (TestThanosRuler_PolicyRefusals). A
// field of a Kubernetes type is not
// derived: an environment variable's value is no more held here than on a pod
// kind. That is the whole of what is recognised: a credential under a name
// that does not say so is not.
func TestMonitoringWorkloadKinds_CredentialsHeldOrListed(t *testing.T) {
	for _, kind := range monitoringWorkloadKinds {
		t.Run(kind.component, func(t *testing.T) {
			var candidates []string
			walkKindFields(kind.typ, func(kindField) bool { return true }, func(f kindField) {
				name, _, _ := strings.Cut(f.field.Tag.Get("json"), ",")
				if f.owner.PkgPath() == kind.typ.PkgPath() && isTextField(f.field.Type) && credentialName.MatchString(name) {
					candidates = append(candidates, f.path)
				}
			})
			slices.Sort(candidates)
			t.Logf("fields of %s named for a credential that hold text: %v", kind.typ, candidates)
			if len(candidates) == 0 {
				t.Fatalf("found no field of %s named for a credential; the walk is broken", kind.typ)
			}
			answers := slices.Concat(kind.literals, slices.Collect(maps.Keys(kind.notCredentials)))
			slices.Sort(answers)
			if !slices.Equal(answers, candidates) {
				t.Errorf("each field named for a credential is answered once:\n  derived  %v\n  answered %v", candidates, answers)
			}
			for path, reason := range kind.notCredentials {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s is listed as no credential without a reason", path)
				}
			}
		})
	}
}

// TestMonitoringWorkload_HeldOnASyntheticValue holds the two shared functions
// to each refusal they state, on workloads no kind maps. A kind reaches only
// the branches its spec has fields for: the alertmanager and thanosruler
// kinds name one image and one resource block, both at the top of their spec,
// and the thanosruler kind reports one credential per entry of a list. A
// branch nothing reaches is not shown to work.
//
// Each workload of the first table is valid without a policy, passes under one
// that holds nothing, and is refused under one that holds it, with the class
// and under the path of the property. Each of the second is refused with or
// without a policy.
func TestMonitoringWorkload_HeldOnASyntheticValue(t *testing.T) {
	quantities := func(name corev1.ResourceName, q string) corev1.ResourceList {
		return corev1.ResourceList{name: resource.MustParse(q)}
	}
	claim := func(size string) corev1.PersistentVolumeClaimSpec {
		return corev1.PersistentVolumeClaimSpec{Resources: corev1.VolumeResourceRequirements{Requests: quantities(corev1.ResourceStorage, size)}}
	}
	five := int64(5)
	for name, tc := range map[string]struct {
		workload monitoringWorkload
		policy   *workloadPolicy
		class    oam.RefusalClass
		want     string
	}{
		"a credential in the clear, the first of two": {
			monitoringWorkload{literals: []string{"remoteWrite[0].basicAuth.password", "remoteWrite[1].bearerToken"}},
			&workloadPolicy{forbidExplicitSecrets: true},
			oam.RefusalExplicitSecret, "remoteWrite[0].basicAuth.password: holds a credential in the object",
		},
		"the second image outside the allowed registries": {
			monitoringWorkload{images: []fieldValue{
				{"image", "registry.example/prometheus/prometheus:v3.0.0"},
				{"thanos.image", "other.example/thanos/thanos:v0.37.0"},
			}},
			&workloadPolicy{allowed: []string{"registry.example"}},
			oam.RefusalRegistry, `thanos.image: image "other.example/thanos/thanos:v0.37.0" is not from an allowed registry`,
		},
		"replicas over the maximum, under the path the kind gives": {
			monitoringWorkload{replicas: &five, replicasPath: "replicas times shards"},
			&workloadPolicy{maxReplicas: new(int32(3))},
			oam.RefusalReplicaMaximum, "replicas times shards 5 exceeds enforced maximum 3",
		},
		"a claim template over the storage maximum": {
			monitoringWorkload{storage: &monitoringv1.StorageSpec{VolumeClaimTemplate: monitoringv1.EmbeddedPersistentVolumeClaim{Spec: claim("100Gi")}}},
			&workloadPolicy{maxStorage: "10Gi"},
			oam.RefusalStorageMaximum, "storage.volumeClaimTemplate.spec.resources.requests.storage",
		},
		"an ephemeral claim template over the storage maximum": {
			monitoringWorkload{storage: &monitoringv1.StorageSpec{Ephemeral: &corev1.EphemeralVolumeSource{
				VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{Spec: claim("100Gi")},
			}}},
			&workloadPolicy{maxStorage: "10Gi"},
			oam.RefusalStorageMaximum, "storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage",
		},
		"the second resource block over the cpu maximum": {
			monitoringWorkload{resources: []fieldResources{
				{"resources", corev1.ResourceRequirements{Limits: quantities(corev1.ResourceCPU, "1")}},
				{"thanos.resources", corev1.ResourceRequirements{Limits: quantities(corev1.ResourceCPU, "4")}},
			}},
			&workloadPolicy{maxCPU: "2"},
			oam.RefusalResourceMaximum, `thanos.resources: cpu limit "4" exceeds enforced maximum "2"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateMonitoringWorkload(tc.workload); err != nil {
				t.Errorf("without a policy: %v, want it valid", err)
			}
			if err := enforceMonitoringWorkloadPolicy(tc.workload, &workloadPolicy{}); err != nil {
				t.Errorf("under a policy that holds nothing: %v, want it passed", err)
			}
			err := enforceMonitoringWorkloadPolicy(tc.workload, tc.policy)
			var refusal *oam.PolicyRefusal
			if !errors.As(err, &refusal) || refusal.Class != tc.class {
				t.Fatalf("err = %v, want a refusal of class %q", err, tc.class)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}

	over := corev1.ResourceRequirements{Requests: quantities(corev1.ResourceCPU, "2"), Limits: quantities(corev1.ResourceCPU, "1")}
	for name, tc := range map[string]struct {
		workload monitoringWorkload
		want     string
	}{
		"an image without a tag": {
			monitoringWorkload{images: []fieldValue{{"thanos.image", "registry.example/thanos/thanos"}}},
			`thanos.image: image "registry.example/thanos/thanos" rejected: no tag or digest specified; use an explicit version tag or digest`,
		},
		"an image tagged latest": {
			monitoringWorkload{images: []fieldValue{{"image", "registry.example/prometheus/prometheus:latest"}}},
			`image: image "registry.example/prometheus/prometheus:latest" rejected: :latest tag not allowed; use an explicit version tag or digest`,
		},
		"a request over its limit in the spec's own block": {
			monitoringWorkload{resources: []fieldResources{{"resources", over}}},
			"resources: cpu: request 2 must not exceed limit 1",
		},
		"a request over its limit in a block the spec holds deeper": {
			monitoringWorkload{resources: []fieldResources{{"thanos.resources", over}}},
			"thanos: resources: cpu: request 2 must not exceed limit 1",
		},
		"a listed container without an image, named for none the operator generates": {
			monitoringWorkload{
				generated: map[string][]string{"containers": {"prometheus", "config-reloader"}},
				pod:       corev1.PodSpec{Containers: []corev1.Container{{Name: "prometheus"}, {Name: "proxy"}}},
			},
			`containers[1] "proxy": names no image, and the Prometheus operator generates no container of that name to merge it into; name an image, or the container it patches (prometheus, config-reloader)`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateMonitoringWorkload(tc.workload)
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}

	if err := validateMonitoringWorkload(monitoringWorkload{}); err != nil {
		t.Errorf("a workload that says nothing: %v, want it valid", err)
	}
	strict := &workloadPolicy{
		maxReplicas: new(int32(1)), maxCPU: "1", maxStorage: "1Gi", allowed: []string{"registry.example"},
		noHostNetwork: true, noPrivileged: true, noHostPath: true, forbidExplicitSecrets: true,
	}
	if err := enforceMonitoringWorkloadPolicy(monitoringWorkload{}, strict); err != nil {
		t.Errorf("a workload that says nothing, under a policy that holds everything: %v, want it passed", err)
	}
}
