package kurel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// This file is the output-invariant guard for go-kure/launcher#572. A component
// name is a DNS-1123 subdomain (up to 253 characters) while a label value is at
// most 63, so the component-identity labels and selectors launcher generates —
// the `app` label, the `app` selectors that pick a component's pods, and the
// synthesized NetworkPolicies' `<domain>/component` selector — take their value
// from oam.ComponentLabelValue. Selectors an operator owns (a component type
// that selects the pods an operator creates, by the operator's own label and
// naming contract) are not component-identity labels and are out of scope here;
// the fixtures below do not exercise them. The test renders every registered
// component type and every registered trait through the real `kurel build` path
// and checks the emitted manifests, so a new emitter that writes the raw name
// into an `app` label — or a selector that stops agreeing with the labels it
// targets — fails here. The fixture tables below are checked against the
// registries (builtinComponentHandlers, builtinTraitHandlers,
// builtinTraitLoweringRules), so a type registered without a fixture fails too.

// kurelComponentLabel is the component label key of a kurel build: the key the
// synthesized NetworkPolicies select and, since go-kure/launcher#788, the label
// launcher puts on every object a component owns and on its pod templates.
const kurelComponentLabel = kurelDomain + "/component"

// labelInvariantProfile carries every capability a fixture below needs. The
// expose rendering also injects networkPolicy.trafficSources, so the routing
// traits exercise the synthesized ingress NetworkPolicy and its component
// selector.
const labelInvariantProfile = `apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: nginx
        networkPolicy:
          trafficSources:
            - namespace: ingress-nginx
    certificate:
      rendering:
        issuerRef:
          name: letsencrypt-prod
          kind: ClusterIssuer
    external-secret:
      rendering:
        secretStoreRef:
          name: vault-cluster-store
          kind: ClusterSecretStore
`

// longLabelComponentName is a valid component name (a DNS-1123 subdomain) of
// 200 characters, dotted, far over the 63-character label-value limit.
func longLabelComponentName(t *testing.T) string {
	t.Helper()
	segment := "component-label-invariant-guard" // 31 characters
	var parts []string
	for len(strings.Join(parts, ".")) < 200 {
		parts = append(parts, segment)
	}
	name := strings.Join(parts, ".")[:199] + "z"
	if len(name) != 200 {
		t.Fatalf("long test name is %d characters, want 200", len(name))
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		t.Fatalf("long test name %q is not a valid component name: %v", name, errs)
	}
	return name
}

// boundaryLabelComponentName is a 63-character name that every component type
// without a shorter name bound of its own (componentLabelFixture.nameBound)
// accepts: a DNS-1035 label, so also a valid Service and container name. It is
// the longest name whose label value is the name itself.
func boundaryLabelComponentName(t *testing.T) string {
	t.Helper()
	name := "a" + strings.Repeat("b-", 30) + "cd"
	if len(name) != validation.LabelValueMaxLength {
		t.Fatalf("boundary test name is %d characters, want %d", len(name), validation.LabelValueMaxLength)
	}
	if errs := validation.IsDNS1035Label(name); len(errs) > 0 {
		t.Fatalf("boundary test name %q is not a DNS-1035 label: %v", name, errs)
	}
	return name
}

// boundedLabelComponentName is a DNS-1035 name of exactly n characters, for a
// type whose own name bound is below the 63-character boundary.
func boundedLabelComponentName(t *testing.T, n int) string {
	t.Helper()
	name := "a" + strings.Repeat("b", n-2) + "c"
	if errs := validation.IsDNS1035Label(name); len(errs) > 0 || len(name) != n {
		t.Fatalf("bounded test name %q (%d characters, want %d) is not a DNS-1035 label: %v", name, len(name), n, errs)
	}
	return name
}

// componentLabelFixture renders one component type on its own.
type componentLabelFixture struct {
	props map[string]any
	// propsFor, when set, replaces props: it builds them inside the running
	// test, for a type whose properties must name something only the test can
	// provide (helmtemplate's locally served chart). Both renders of the type
	// use what it returns.
	propsFor func(t *testing.T) map[string]any
	// longRefusal is empty when the type accepts a name over 63 characters.
	// Otherwise it is a substring of the refusal the type already gives such a
	// name (go-kure/launcher#407 container name, go-kure/launcher#546 Service
	// name, a kind's own name length); the test pins that the refusal is
	// unchanged.
	longRefusal string
	// nameBound, when set, is a name bound of the type's own below 63
	// characters. The type is rendered at a DNS-1035 name of exactly that
	// length instead of the 63-character boundary, which must then be refused
	// with longRefusal like the long name.
	nameBound int
	// labelled says the component's own output carries an `app` label, so the
	// invariant is checked on at least one value rather than vacuously — at
	// the boundary name, and at the long name when the type accepts it.
	labelled bool
	// selectors is the minimum number of pod selectors the boundary render must
	// match against its own pod template (0 for a type with no pods, or a Job
	// kind, which authors no spec.selector).
	selectors int
	// storedTemplates is the number of stored pod templates (a PodTemplate's)
	// each render must have read for the component label; 0 for every type that
	// emits no PodTemplate.
	storedTemplates int
}

const (
	containerNameRefusal = "container name"
	serviceNameRefusal   = "is not a valid Service name"
)

func workloadProps(extra map[string]any) map[string]any {
	p := map[string]any{"image": "ghcr.io/example/app:v1.0.0"}
	maps.Copy(p, extra)
	return p
}

// helmtemplateLabelProps serves a one-template chart from a Helm repository
// local to t and returns helmtemplate properties naming it: helmtemplate
// renders its chart at build time.
//
// The chart renders one ConfigMap and no pods, and helmtemplate emits it as
// rendered, generating no component-identity label of its own — so the fixture
// is unlabelled and matches no pod selector. A chart's own labels and
// selectors are the chart's contract, like an operator's, and out of scope; an
// `app` label a chart authored would still be compared with the projection,
// so this chart has none. It labels the ConfigMap with .Release.Name, the one
// value through which a component name reaches a rendered label: with no
// releaseName authored the release name is the component name, shortened as
// Flux shortens a release name to at most 53 characters, so the label value
// stays within the 63-character limit and the 200-character name renders.
func helmtemplateLabelProps(t *testing.T) map[string]any {
	t.Helper()
	chart := buildMinimalChartTar(t, "labelchart", "0.1.0", map[string]string{
		"labelchart/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: label-cm\n" +
			"  labels:\n    app.kubernetes.io/instance: {{ .Release.Name }}\ndata:\n  k: v\n",
	})
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprint(w, helmIndexYAML("labelchart", "0.1.0", srvURL+"/labelchart-0.1.0.tgz"))
		case "/labelchart-0.1.0.tgz":
			w.Write(chart)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	srvURL = srv.URL
	return map[string]any{"chart": "labelchart", "version": "0.1.0", "source": map[string]any{"url": srvURL}}
}

// componentLabelFixtures holds one fixture per component type, in the order
// of the types: a new type's entry goes at its position
// (TestKindLists_InOrder).
var componentLabelFixtures = map[string]componentLabelFixture{
	"artifactgenerator": {props: map[string]any{
		"sources": []any{map[string]any{"alias": "app", "kind": "GitRepository", "name": "app"}},
		"artifacts": []any{map[string]any{
			"name": "app",
			"copy": []any{map[string]any{"from": "@app/deploy/**", "to": "@artifact/"}}}}}},
	// The five kinds of the Gateway API's infrastructure objects emit identity
	// and the authored fields too, with no `app` label and no pods: a
	// GatewayClass cluster-scoped, the other four namespaced. None checks a
	// name rule of its own.
	"backendtlspolicy": {props: map[string]any{
		"targetRefs": []any{map[string]any{"group": "", "kind": "Service", "name": "payments"}},
		"validation": map[string]any{"hostname": "payments.internal.example.com", "wellKnownCACertificates": "System"}}},
	// The kind-named Flux sources each emit one source CR named after the
	// component: no `app` label, no pods, and a name up to a DNS-1123 subdomain.
	"bucket": {props: map[string]any{"bucketName": "artifacts", "endpoint": "minio.example.com:9000"}},
	// The three cert-manager kinds emit identity and the authored fields too,
	// with no `app` label and no pods: an Issuer and a Certificate namespaced, a
	// ClusterIssuer cluster-scoped. None checks a name rule of its own.
	"certificate": {props: map[string]any{
		"secretName": "web-tls", "dnsNames": []any{"web.example.com"},
		"issuerRef": map[string]any{"name": "selfsigned", "kind": "ClusterIssuer"}}},
	// The four kinds of Cilium's BGP control plane emit identity and the
	// authored fields too, cluster-scoped, with no `app` label and no pods. A
	// selector in one is the author's label query over nodes, pools, Services
	// or advertisements, not a pod selector of this document. None checks a
	// name rule of its own.
	"cilium-bgpadvertisement": {props: map[string]any{"advertisements": []any{
		map[string]any{"advertisementType": "PodCIDR"}}}},
	"cilium-bgpclusterconfig": {props: map[string]any{"bgpInstances": []any{
		map[string]any{"name": "instance-65000", "localASN": 65000}}}},
	"cilium-bgpnodeconfigoverride": {props: map[string]any{"bgpInstances": []any{
		map[string]any{"name": "instance-65000", "routerID": "192.0.2.1"}}}},
	"cilium-bgppeerconfig": {props: map[string]any{"ebgpMultihop": 2}},
	// Five more kinds of Cilium's API (cilium-cidrgroup,
	// cilium-loadbalancerippool, cilium-egressgatewaypolicy,
	// cilium-localredirectpolicy, cilium-nodeconfig) emit identity and the
	// authored spec. A selector in one is the author's label query over Services,
	// nodes, namespaces or pods of the cluster, not a pod selector of this
	// document, so the rows select on `role`: the invariant reads every `app`
	// value as this component's own. None checks a name rule of its own.
	"cilium-cidrgroup": {props: map[string]any{"externalCIDRs": []any{"192.0.2.0/24"}}},
	// A CiliumClusterwideNetworkPolicy's selectors are the author's, as the
	// CiliumNetworkPolicy's: endpoints of the whole cluster, or nodes.
	"cilium-clusterwidenetworkpolicy": {props: map[string]any{"spec": map[string]any{
		"nodeSelector": map[string]any{"matchLabels": map[string]any{"node-role": "edge"}},
		"ingress": []any{map[string]any{"fromEndpoints": []any{
			map[string]any{"matchLabels": map[string]any{"role": "api"}}}}}}}},
	"cilium-egressgatewaypolicy": {props: map[string]any{
		"selectors":        []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"role": "web"}}}},
		"destinationCIDRs": []any{"192.0.2.0/24"},
		"egressGateway":    map[string]any{"nodeSelector": map[string]any{"matchLabels": map[string]any{"egress": "true"}}},
	}},
	"cilium-loadbalancerippool": {props: map[string]any{"blocks": []any{map[string]any{"cidr": "192.0.2.0/24"}}}},
	"cilium-localredirectpolicy": {props: map[string]any{
		"redirectFrontend": map[string]any{"addressMatcher": map[string]any{
			"ip": "169.254.169.254", "toPorts": []any{map[string]any{"port": "80", "protocol": "TCP"}},
		}},
		"redirectBackend": map[string]any{
			"localEndpointSelector": map[string]any{"matchLabels": map[string]any{"role": "metadata-proxy"}},
			"toPorts":               []any{map[string]any{"port": "8080", "protocol": "TCP"}},
		},
	}},
	// A CiliumNetworkPolicy's endpoint selectors are the author's, as a
	// NetworkPolicy's podSelector is.
	"cilium-networkpolicy": {props: map[string]any{"spec": map[string]any{
		"endpointSelector": map[string]any{"matchLabels": map[string]any{"role": "db"}},
		"ingress": []any{map[string]any{"fromEndpoints": []any{
			map[string]any{"matchLabels": map[string]any{"role": "api"}}}}}}}},
	"cilium-nodeconfig": {props: map[string]any{
		"defaults":     map[string]any{"enable-hubble": "false"},
		"nodeSelector": map[string]any{"matchLabels": map[string]any{"node-role": "edge"}},
	}},
	// The four external-secrets kinds emit identity and the authored fields
	// too, with no `app` label and no pods: a SecretStore and an ExternalSecret
	// namespaced, the two cluster kinds cluster-scoped. A selector in one is the
	// author's label query over namespaces, not a pod selector of this document.
	// None checks a name rule of its own.
	"clusterexternalsecret": {props: map[string]any{
		"namespaceSelectors": []any{map[string]any{"matchLabels": map[string]any{"team": "payments"}}},
		"externalSecretSpec": map[string]any{
			"secretStoreRef": map[string]any{"name": "vault-cluster-store", "kind": "ClusterSecretStore"},
			"data": []any{map[string]any{
				"secretKey": "PASSWORD", "remoteRef": map[string]any{"key": "prod/app", "property": "password"}}}}}},
	"clusterissuer": {props: map[string]any{"ca": map[string]any{"secretName": "ca-key-pair"}}},
	// The four kinds of the RBAC API are identity and the authored fields too.
	// They select no pod, and check no name rule of their own.
	"clusterrole": {props: map[string]any{"rules": []any{
		map[string]any{"apiGroups": []any{""}, "resources": []any{"nodes"}, "verbs": []any{"get"}}}}},
	"clusterrolebinding": {props: map[string]any{
		"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": "web", "namespace": "default"}},
		"roleRef":  map[string]any{"kind": "ClusterRole", "name": "node-reader"}}},
	"clustersecretstore": {props: map[string]any{"provider": map[string]any{
		"aws": map[string]any{"service": "SecretsManager", "region": "eu-west-1"}}}},
	// The Cluster's pods are created and labelled by the operator, so postgresql
	// and cnpg-cluster emit no `app` label and no pod selector of their own.
	// CloudNativePG admits a Cluster name of at most 50 characters.
	"cnpg-cluster": {props: map[string]any{"storage": map[string]any{"size": "10Gi"}},
		nameBound: 50, longRefusal: "must be a DNS-1035 label of at most 50 characters"},
	// The other CloudNativePG kinds also run no pods of their own. CloudNativePG
	// names the Pooler's Service after it, so its name is a DNS-1035 label.
	"cnpg-database":    {props: map[string]any{"cluster": map[string]any{"name": "db"}, "name": "app", "owner": "app"}},
	"cnpg-objectstore": {props: map[string]any{"configuration": map[string]any{"destinationPath": "s3://backups/db"}}},
	"cnpg-pooler": {props: map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}},
		longRefusal: "must be a DNS-1035 label of at most 63 characters"},
	// The go-kure/launcher#702 kinds name their one object after the
	// component, and a ServiceAccount, ConfigMap or claim name is a DNS-1123
	// subdomain, so each accepts the 200-character name and labels its object.
	"configmap": {props: map[string]any{"data": map[string]any{"k": "v"}}, labelled: true},
	"crd": {props: map[string]any{"inline": `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
`}},
	// The two batch kinds refuse a long name by the length of their own object
	// first: a CronJob's name is at most 52 characters, a Job's at most 63.
	"cronjob": {props: workloadProps(map[string]any{"schedule": "0 2 * * *"}),
		nameBound: 52, longRefusal: "the component name is the CronJob's name, which must be at most 52 characters", labelled: true},
	// The six cluster-scoped kinds storageclass, volumeattributesclass,
	// priorityclass, runtimeclass, ingressclass and csidriver emit identity and
	// the authored fields, with no `app` label and no pods. They leave the name
	// rules of their object to the API server, so each accepts the 200-character
	// name.
	"csidriver": {props: map[string]any{"attachRequired": false}},
	// daemonset and statefulset emit no Service since go-kure/launcher#690, so
	// their long-name refusal is the container name's, as deployment's.
	"daemonset":  {props: workloadProps(nil), longRefusal: containerNameRefusal, labelled: true, selectors: 1},
	"deployment": {props: workloadProps(map[string]any{"replicas": 3}), longRefusal: containerNameRefusal, labelled: true, selectors: 1},
	// An EndpointSlice is identity and the authored fields too. It selects no
	// pod: it lists addresses, and belongs to a Service through a label the
	// author writes under `labels`. It checks no name rule of its own.
	"endpointslice": {props: map[string]any{
		"addressType": "IPv4",
		"endpoints":   []any{map[string]any{"addresses": []any{"192.0.2.10"}}},
		"ports":       []any{map[string]any{"name": "http", "port": 8080}},
	}},
	"externalsecret": {props: map[string]any{
		"secretStoreRef": map[string]any{"name": "vault-cluster-store", "kind": "ClusterSecretStore"},
		"data": []any{map[string]any{
			"secretKey": "PASSWORD", "remoteRef": map[string]any{"key": "prod/app", "property": "password"}}}}},
	"fluxcd-alert": {props: map[string]any{
		"providerRef":  map[string]any{"name": "slack"},
		"eventSources": []any{map[string]any{"kind": "Kustomization", "name": "*"}}}},
	// Emits only the Kustomization, which carries no `app` label.
	"fluxcd-kustomization": {props: map[string]any{"path": "./", "prune": true,
		"sourceRef": map[string]any{"kind": "OCIRepository", "name": "app"}}},
	"gateway": {props: map[string]any{
		"gatewayClassName": "public",
		"listeners":        []any{map[string]any{"name": "http", "port": 80, "protocol": "HTTP"}}}},
	"gatewayclass":  {props: map[string]any{"controllerName": "example.net/gateway-controller"}},
	"gitrepository": {props: map[string]any{"url": "https://git.example.com/app.git", "ref": map[string]any{"branch": "main"}}},
	// Lowers to the helmrelease terminal plus a generated HelmRepository;
	// valuesMode configMap with non-empty values adds the values ConfigMap
	// through a configmap trait, the one labelled object.
	"helm": {props: map[string]any{"chart": "app",
		"source":     map[string]any{"url": "https://charts.example.com"},
		"valuesMode": "configMap", "values": map[string]any{"replicaCount": 2}}, labelled: true},
	"helmchart": {props: map[string]any{"chart": "podinfo", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "podinfo"}}},
	// Emits only the HelmRelease, which carries no `app` label.
	"helmrelease": {props: map[string]any{
		"chart": map[string]any{"spec": map[string]any{"chart": "app",
			"sourceRef": map[string]any{"kind": "HelmRepository", "name": "example"}}},
		"values": map[string]any{"replicaCount": 2}}},
	"helmrepository": {props: map[string]any{"url": "https://charts.example.com"}},
	// Renders a locally served chart; helmtemplateLabelProps says why it is
	// unlabelled, selects no pods and accepts the 200-character name.
	"helmtemplate": {propsFor: helmtemplateLabelProps},
	// The servicecidr, poddisruptionbudget and horizontalpodautoscaler kinds emit
	// identity and the authored fields too, with no `app` label and no pods: a
	// ServiceCIDR is cluster-scoped, the budget and the autoscaler are
	// namespaced. None checks a name rule of its own. The budget authors no
	// selector, so it selects no pod.
	"horizontalpodautoscaler": {props: map[string]any{
		"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"},
		"maxReplicas":    3}},
	// An HTTPRoute name is a DNS-1123 subdomain too, and the object is authored
	// as an Ingress is: no pods, no selector, a backend by name.
	"httproute": {props: map[string]any{
		"parentRefs": []any{map[string]any{"name": "gateway"}},
		"rules": []any{map[string]any{"backendRefs": []any{
			map[string]any{"name": "web", "port": 80}}}}}},
	"imagepolicy": {props: map[string]any{
		"imageRepositoryRef": map[string]any{"name": "web"},
		"policy":             map[string]any{"semver": map[string]any{"range": ">=1.0.0"}}}},
	"imagerepository": {props: map[string]any{"image": "registry.example.com/org/web", "interval": "10m"}},
	"imageupdateautomation": {props: map[string]any{
		"sourceRef": map[string]any{"kind": "GitRepository", "name": "fleet"},
		"interval":  "30m"}},
	// An Ingress name is a DNS-1123 subdomain. The object is authored: it runs
	// no pods and selects none, and its backend is a Service by name.
	"ingress": {props: map[string]any{"defaultBackend": map[string]any{
		"service": map[string]any{"name": "web", "port": map[string]any{"number": 80}}}}},
	"ingressclass": {props: map[string]any{"controller": "example.com/ingress-controller"}},
	"issuer":       {props: map[string]any{"selfSigned": map[string]any{}}},
	"job":          {props: workloadProps(nil), longRefusal: "the component name is the Job's name, which must be at most 63 characters", labelled: true},
	"limitrange": {props: map[string]any{"limits": []any{map[string]any{"type": "Container",
		"default": map[string]any{"cpu": "500m"}}}}},
	"listenerset": {props: map[string]any{
		"parentRef": map[string]any{"name": "public"},
		"listeners": []any{map[string]any{"name": "http", "port": 8080, "protocol": "HTTP"}}}},
	"manifests": {props: map[string]any{"inline": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  k: v\n"}},
	// The kinds of MetalLB's API emit identity and the authored spec too, with
	// no `app` label and no pods, all namespaced. A selector in one is the
	// author's label query over Services, namespaces, nodes or pools, so the
	// rows select on `role`, as the Cilium rows above. None checks a name rule
	// of its own.
	// A BFD profile holds no selector at all.
	"metallb-bfdprofile": {props: map[string]any{"receiveInterval": 300, "detectMultiplier": 3}},
	"metallb-bgpadvertisement": {props: map[string]any{
		"ipAddressPools":   []any{"edge"},
		"peers":            []any{"upstream"},
		"serviceSelectors": []any{map[string]any{"matchLabels": map[string]any{"role": "edge"}}}}},
	// A set of community aliases holds no selector either.
	"metallb-community": {props: map[string]any{
		"communities": []any{map[string]any{"name": "no-export", "value": "65535:65281"}}}},
	"metallb-ipaddresspool": {props: map[string]any{
		"addresses": []any{"192.0.2.0/24"},
		"serviceAllocation": map[string]any{"serviceSelectors": []any{
			map[string]any{"matchLabels": map[string]any{"role": "edge"}}}}}},
	"metallb-l2advertisement": {props: map[string]any{
		"ipAddressPools": []any{"edge"},
		"nodeSelectors":  []any{map[string]any{"matchLabels": map[string]any{"role": "edge"}}}}},
	// The namespace, limitrange, resourcequota and persistentvolume kinds of
	// go-kure/launcher#790 emit identity and the authored spec, with no `app`
	// label and no pods. The component name is the Namespace's name, which the
	// API holds to a DNS-1123 label.
	"namespace": {props: map[string]any{}, longRefusal: "must be a DNS-1123 label of at most 63 characters"},
	// A NetworkPolicy name is a DNS-1123 subdomain too. Its podSelector is the
	// author's and picks pods another component owns, so the output holds no pod
	// template to compare it with, as with a Service fronting another component.
	"networkpolicy": {props: map[string]any{
		"podSelector": map[string]any{"matchLabels": map[string]any{"role": "db"}},
		"policyTypes": []any{"Ingress"}}},
	// Lowers to the ocirepository and fluxcd-kustomization terminals, both named
	// after the component.
	"oci":           {props: map[string]any{"source": map[string]any{"url": "oci://registry.example.com/manifests/app"}, "version": "0.3.0"}},
	"ocirepository": {props: map[string]any{"url": "oci://registry.example.com/manifests/app", "ref": map[string]any{"tag": "v1.0.0"}}},
	"passthrough":   {props: map[string]any{"object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "data": map[string]any{"k": "v"}}}},
	// A PersistentVolume name is a DNS-1123 subdomain, so the 200-character
	// name is accepted.
	"persistentvolume": {props: map[string]any{
		"capacity": map[string]any{"storage": "1Gi"}, "accessModes": []any{"ReadWriteMany"},
		"nfs": map[string]any{"server": "nfs.example.com", "path": "/exports/data"}}},
	"persistentvolumeclaim": {props: map[string]any{"size": "1Gi"}, labelled: true},
	// A Pod name is a DNS-1123 subdomain too. The pod carries the authored spec
	// and the `app` label; it is a pod, not a selector over pods.
	"pod": {props: map[string]any{"containers": []any{
		map[string]any{"name": "app", "image": "ghcr.io/example/app:v1.0.0"}}}, labelled: true},
	"poddisruptionbudget": {props: map[string]any{"maxUnavailable": 1}},
	// The four Prometheus operator kinds emit identity and the authored fields
	// too, namespaced, with no `app` label and no pods. A monitor's selector is
	// the author's label query over Services or Pods, not a pod selector of
	// this document. None checks a name rule of its own.
	"podmonitor": {props: map[string]any{
		"selector":            map[string]any{"matchLabels": map[string]any{"team": "payments"}},
		"podMetricsEndpoints": []any{map[string]any{"port": "metrics"}}}},
	// A PodTemplate name is a DNS-1123 subdomain too. The template is stored,
	// not run: it gets no `app` label, and nothing selects it. It still carries
	// the component label, on a template authored without labels.
	"podtemplate": {props: map[string]any{
		"template": map[string]any{
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": "ghcr.io/example/app:v1.0.0"}}}}},
		storedTemplates: 1},
	"postgresql": {props: map[string]any{"version": "16", "storageSize": "10Gi"},
		nameBound: 50, longRefusal: "must be a DNS-1035 label of at most 50 characters"},
	"priorityclass": {props: map[string]any{"value": 1000}},
	"prometheus-probe": {props: map[string]any{
		"prober":  map[string]any{"url": "blackbox-exporter.monitoring.svc:9115"},
		"targets": map[string]any{"staticConfig": map[string]any{"static": []any{"https://example.com"}}}}},
	"prometheusrule": {props: map[string]any{"groups": []any{map[string]any{
		"name":  "availability",
		"rules": []any{map[string]any{"alert": "TargetDown", "expr": "up == 0"}}}}}},
	"referencegrant": {props: map[string]any{
		"from": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "namespace": "shop"}},
		"to":   []any{map[string]any{"group": "", "kind": "Service"}}}},
	// A ReplicaSet name is a DNS-1123 subdomain too. The `app` label is on the
	// pod template, beside the authored label the authored selector picks.
	"replicaset": {props: map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": "ghcr.io/example/app:v1.0.0"}}}}},
		labelled: true, selectors: 1},
	// A ReplicationController name is a DNS-1123 subdomain too. Its selector is
	// a plain label map; the `app` label is on the pod template beside the
	// authored label the selector asks for.
	"replicationcontroller": {props: map[string]any{
		"selector": map[string]any{"tier": "web"},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": "ghcr.io/example/app:v1.0.0"}}}}},
		labelled: true, selectors: 1},
	// The two kinds of VolSync's API emit identity and the authored fields too,
	// namespaced, with no `app` label and no pods of their own: the mover pod
	// is the operator's. Neither checks a name rule of its own.
	"replicationdestination": {props: map[string]any{
		"trigger": map[string]any{"manual": "restore-1"},
		"restic":  map[string]any{"repository": "restic-repo", "copyMethod": "Direct", "destinationPVC": "data"}}},
	"replicationsource": {props: map[string]any{
		"sourcePVC": "data", "trigger": map[string]any{"schedule": "0 3 * * *"},
		"restic": map[string]any{"repository": "restic-repo", "copyMethod": "Snapshot"}}},
	"resourcequota": {props: map[string]any{"hard": map[string]any{"pods": "10"}}},
	"role": {props: map[string]any{"rules": []any{
		map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"}}}}},
	"rolebinding": {props: map[string]any{
		"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": "web"}},
		"roleRef":  map[string]any{"kind": "Role", "name": "reader"}}},
	"runtimeclass": {props: map[string]any{"handler": "runc"}},
	// A Secret name is a DNS-1123 subdomain, and the kind labels its Secret as
	// the configmap kind labels its ConfigMap.
	"secret": {props: map[string]any{"stringData": map[string]any{"k": "v"}}, labelled: true},
	"secretstore": {props: map[string]any{"provider": map[string]any{
		"aws": map[string]any{"service": "SecretsManager", "region": "eu-west-1"}}}},
	"service": {props: map[string]any{"ports": []any{map[string]any{"name": "http", "port": 80, "targetPort": 8080}}},
		longRefusal: serviceNameRefusal, labelled: true},
	"serviceaccount": {props: map[string]any{}, labelled: true},
	"servicecidr":    {props: map[string]any{"cidrs": []any{"10.96.0.0/16"}}},
	"servicemonitor": {props: map[string]any{
		"selector":  map[string]any{"matchLabels": map[string]any{"team": "payments"}},
		"endpoints": []any{map[string]any{"port": "metrics"}}}},
	"statefulset":           {props: workloadProps(nil), longRefusal: containerNameRefusal, labelled: true, selectors: 1},
	"storageclass":          {props: map[string]any{"provisioner": "csi.example.com"}},
	"volumeattributesclass": {props: map[string]any{"driverName": "csi.example.com", "parameters": map[string]any{"iops": "3000"}}},
	// replicas 3 with topologySpread and pod anti-affinity puts every scheduling
	// selector the workload kinds build into the output.
	"webservice": {props: workloadProps(map[string]any{"port": 8080, "replicas": 3, "topologySpread": true,
		"affinity": map[string]any{"enablePodAntiAffinity": true}}), longRefusal: serviceNameRefusal, labelled: true, selectors: 5},
	"worker": {props: workloadProps(map[string]any{"replicas": 3, "topologySpread": true,
		"affinity": map[string]any{"enablePodAntiAffinity": true}}), longRefusal: containerNameRefusal, labelled: true, selectors: 4},
}

// traitLabelFixture renders one trait on a host component.
type traitLabelFixture struct {
	props map[string]any // nil: the trait is authored with no properties
	// host is the component type (and props) the trait is attached to at the
	// 63-character boundary name.
	host      string
	hostProps map[string]any
	// longHost is true when the trait also renders on a component type that
	// accepts a name over 63 characters (a passthrough ConfigMap); false for a
	// trait that needs a workload host, which already refuses such a name.
	longHost bool
	// longLabelled says the trait itself emits an `app` label, so on the long
	// host the projected value must actually appear.
	longLabelled bool
	// selectors is the minimum number of pod selectors the boundary render must
	// match against the host's pod template; 0 means the host's own two (the
	// workload selector and its Service, or its topology spread).
	selectors int
}

var passthroughHostProps = map[string]any{"object": map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "data": map[string]any{"k": "v"}}}

func webserviceHost() (string, map[string]any) {
	return "webservice", workloadProps(map[string]any{"port": 8080})
}

var traitLabelFixtures = map[string]traitLabelFixture{
	"ingress": {props: map[string]any{"ingressClassName": "nginx", "rules": []any{map[string]any{
		"host": "app.example.com", "paths": []any{map[string]any{"path": "/", "backend": "other-svc", "port": 8080}}}}},
		longHost: true, longLabelled: true},
	"httproute": {props: map[string]any{"parentRefs": []any{map[string]any{"name": "gw"}}, "rules": []any{map[string]any{
		"backendRefs": []any{map[string]any{"name": "other-svc", "port": 8080}}}}},
		longHost: true, longLabelled: true},
	"expose":      {props: map[string]any{"rules": []any{map[string]any{"host": "app.example.com", "paths": []any{map[string]any{"path": "/"}}}}}},
	"certificate": {props: map[string]any{"secretName": "app-tls", "dnsNames": []any{"app.example.com"}}, longHost: true},
	"scaler":      {props: map[string]any{"minReplicas": 2, "maxReplicas": 5, "enablePDB": true}, selectors: 3},
	"pvc":         {props: map[string]any{"name": "shared-data", "size": "5Gi"}, longHost: true, longLabelled: true},
	"external-secret": {props: map[string]any{"secretName": "app-credentials", "data": []any{map[string]any{
		"secretKey": "PASSWORD", "remoteRef": map[string]any{"key": "prod/app", "property": "password"}}}},
		longHost: true, longLabelled: true},
	"configmap": {props: map[string]any{"name": "app-config", "data": map[string]any{"K": "v"}}, longHost: true, longLabelled: true},
	"secret":    {props: map[string]any{"name": "app-creds", "stringData": map[string]any{"K": "v"}}, longHost: true, longLabelled: true},
	"networkpolicy": {props: map[string]any{"ingress": []any{map[string]any{
		"from":  []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"role": "frontend"}}}},
		"ports": []any{map[string]any{"port": 8080, "protocol": "TCP"}}}}},
		longHost: true, longLabelled: true, selectors: 3},
	"cilium-networkpolicy": {props: map[string]any{"name": "app-allow",
		"endpointSelector": map[string]any{"matchLabels": map[string]any{"role": "api"}},
		"ingress":          []any{map[string]any{"fromEndpoints": []any{map[string]any{"matchLabels": map[string]any{"role": "frontend"}}}}}},
		longHost: true},
	"volsync": {props: map[string]any{"sourcePVC": "data", "schedule": "@daily"},
		// Pod anti-affinity gives the host a second pod selector beside the
		// StatefulSet's own, now that it emits no Service (go-kure/launcher#690).
		// statefulset reads the raw upstream Affinity, so the term is authored;
		// `Exists` on `app` matches the pods without naming the label value.
		host: "statefulset", hostProps: workloadProps(map[string]any{
			"affinity": map[string]any{"podAntiAffinity": map[string]any{
				"preferredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"weight": 100,
					"podAffinityTerm": map[string]any{
						"topologyKey": "kubernetes.io/hostname",
						"labelSelector": map[string]any{"matchExpressions": []any{
							map[string]any{"key": "app", "operator": "Exists"},
						}},
					},
				}},
			}},
			"volumeClaimTemplates": []any{map[string]any{"name": "data", "size": "10Gi", "mountPath": "/data"}}})},
	"rbac": {props: map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps"}, "verbs": []any{"get"}}},
		"clusterWide": true}, longHost: true, longLabelled: true},
	"prune-protection": {longHost: true},
	"force-replace":    {longHost: true},
	"security-context": {props: map[string]any{"psaLevel": "restricted"}},
	"topology-spread":  {host: "deployment", hostProps: workloadProps(map[string]any{"replicas": 3}), selectors: 3},
}

// TestComponentLabelInvariant_FixturesCoverRegistry: every registered component
// handler, component lowering rule, trait handler and trait lowering rule has a
// fixture, and no fixture names an unregistered type. A new emitter cannot join
// the registry without joining the invariant test.
func TestComponentLabelInvariant_FixturesCoverRegistry(t *testing.T) {
	wantComponents := slices.Sorted(maps.Keys(builtinComponentHandlers()))
	wantComponents = append(wantComponents, slices.Collect(maps.Keys(builtinComponentLoweringRules()))...)
	slices.Sort(wantComponents)
	if got := slices.Sorted(maps.Keys(componentLabelFixtures)); !slices.Equal(got, wantComponents) {
		t.Errorf("componentLabelFixtures covers %v, registry has %v — add a fixture for every registered component type", got, wantComponents)
	}
	wantTraits := slices.Sorted(maps.Keys(builtinTraitHandlers()))
	wantTraits = append(wantTraits, slices.Collect(maps.Keys(builtinTraitLoweringRules()))...)
	slices.Sort(wantTraits)
	if got := slices.Sorted(maps.Keys(traitLabelFixtures)); !slices.Equal(got, wantTraits) {
		t.Errorf("traitLabelFixtures covers %v, registry has %v — add a fixture for every registered trait", got, wantTraits)
	}
}

// TestComponentLabelInvariant_ComponentTypes renders every component type at the
// 63-character boundary and, where the type accepts it, with a 200-character
// name; a type that refuses the long name must still refuse it the same way.
func TestComponentLabelInvariant_ComponentTypes(t *testing.T) {
	boundary, long := boundaryLabelComponentName(t), longLabelComponentName(t)
	for _, typ := range slices.Sorted(maps.Keys(componentLabelFixtures)) {
		fx := componentLabelFixtures[typ]
		t.Run(typ, func(t *testing.T) {
			props := fx.props
			if fx.propsFor != nil {
				props = fx.propsFor(t)
			}
			name := boundary
			if fx.nameBound > 0 {
				_, err := renderLabelInvariantErr(t, labelInvariantApp(boundary, typ, props, "", nil))
				if err == nil || !strings.Contains(err.Error(), fx.longRefusal) || !strings.Contains(err.Error(), boundary) {
					t.Fatalf("%s at the 63-character boundary: err = %v, want a refusal mentioning %q and the name", typ, err, fx.longRefusal)
				}
				name = boundedLabelComponentName(t, fx.nameBound)
			}
			app := labelInvariantApp(name, typ, props, "", nil)
			docs := renderLabelInvariant(t, app)
			n := checkComponentLabelInvariant(t, docs, name)
			if fx.labelled {
				requireAppLabel(t, n, typ+" at the boundary name")
			}
			if n.selectors < fx.selectors {
				t.Errorf("%s at the boundary name matched %d pod selectors against its pod template, want at least %d", typ, n.selectors, fx.selectors)
			}
			if n.storedTemplates != fx.storedTemplates {
				t.Errorf("%s at the boundary name: read %d stored pod templates for the component label, want %d", typ, n.storedTemplates, fx.storedTemplates)
			}

			app = labelInvariantApp(long, typ, props, "", nil)
			if fx.longRefusal != "" {
				_, err := renderLabelInvariantErr(t, app)
				if err == nil {
					t.Fatalf("%s accepted a 200-character name; want the existing refusal %q", typ, fx.longRefusal)
				}
				if !strings.Contains(err.Error(), fx.longRefusal) || !strings.Contains(err.Error(), long) {
					t.Errorf("%s refused the long name with %q, want it to mention %q and the name", typ, err, fx.longRefusal)
				}
				return
			}
			// A labelled type must keep its label past 63 characters too:
			// omitting it would pass the value checks vacuously.
			n = checkComponentLabelInvariant(t, renderLabelInvariant(t, app), long)
			if fx.labelled {
				requireAppLabel(t, n, typ+" with the 200-character name")
			}
			if n.storedTemplates != fx.storedTemplates {
				t.Errorf("%s with the 200-character name: read %d stored pod templates for the component label, want %d", typ, n.storedTemplates, fx.storedTemplates)
			}
		})
	}
}

// TestComponentLabelInvariant_RejectsUnlabelledStoredTemplate: the invariant
// check reads the pod template a PodTemplate stores, which sits at the object's
// root where podTemplateLabels does not look, and fails when that template
// lost its component label. The fixture authors no template label, so the
// stripped template has no labels at all: the check must not read "no labels"
// as "nothing to check".
func TestComponentLabelInvariant_RejectsUnlabelledStoredTemplate(t *testing.T) {
	name := longLabelComponentName(t)
	docs := renderLabelInvariant(t, labelInvariantApp(name, "podtemplate", componentLabelFixtures["podtemplate"].props, "", nil))

	var clean recordingReporter
	if n := checkComponentLabelInvariant(&clean, docs, name); len(clean.errs) != 0 || n.storedTemplates != 1 {
		t.Fatalf("unbroken render: errors %v, %d stored pod templates read; want none and 1", clean.errs, n.storedTemplates)
	}

	md := childMap(t, childMap(t, findDoc(t, docs, "PodTemplate"), "template"), "metadata")
	lbls := childMap(t, md, "labels")
	if _, has := lbls[kurelComponentLabel]; !has || len(lbls) != 1 {
		t.Fatalf("stored template labels = %v, want only %s", lbls, kurelComponentLabel)
	}
	delete(md, "labels")

	var got recordingReporter
	checkComponentLabelInvariant(&got, docs, name)
	want := "the stored pod template carries no " + kurelComponentLabel + " label"
	if !slices.ContainsFunc(got.errs, func(e string) bool { return strings.Contains(e, "PodTemplate") && strings.Contains(e, want) }) {
		t.Errorf("stripped render: errors %v, want one naming the PodTemplate and %q", got.errs, want)
	}
}

// TestComponentLabelInvariant_Traits renders every trait on a host at the
// 63-character boundary and, where the trait can attach to a component type
// that accepts it, on a host with a 200-character name.
func TestComponentLabelInvariant_Traits(t *testing.T) {
	boundary, long := boundaryLabelComponentName(t), longLabelComponentName(t)
	for _, trait := range slices.Sorted(maps.Keys(traitLabelFixtures)) {
		fx := traitLabelFixtures[trait]
		t.Run(trait, func(t *testing.T) {
			host, hostProps := fx.host, fx.hostProps
			if host == "" {
				host, hostProps = webserviceHost()
			}
			docs := renderLabelInvariant(t, labelInvariantApp(boundary, host, hostProps, trait, fx.props))
			n := checkComponentLabelInvariant(t, docs, boundary)
			requireAppLabel(t, n, fmt.Sprintf("%s on a %s host", trait, host))
			if want := max(fx.selectors, 2); n.selectors < want {
				t.Errorf("%s on a %s host matched %d pod selectors against its pod template, want at least %d", trait, host, n.selectors, want)
			}
			if !fx.longHost {
				return
			}
			docs = renderLabelInvariant(t, labelInvariantApp(long, "passthrough", passthroughHostProps, trait, fx.props))
			if n := checkComponentLabelInvariant(t, docs, long); fx.longLabelled {
				requireAppLabel(t, n, trait+" on a long-named host")
			}
		})
	}
}

// recordingReporter collects what checkComponentLabelInvariant reports, so a
// test can assert the check rejects a broken output.
type recordingReporter struct{ errs []string }

func (r *recordingReporter) Helper() {}

func (r *recordingReporter) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// findDoc returns the first emitted object of kind.
func findDoc(t *testing.T, docs []map[string]any, kind string) map[string]any {
	t.Helper()
	for _, d := range docs {
		if d["kind"] == kind {
			return d
		}
	}
	t.Fatalf("no %s in the rendered output", kind)
	return nil
}

// childMap returns m[key] as a map, failing the test when it is not one.
func childMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	c, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is %T, want a map", key, m[key])
	}
	return c
}

// networkPolicyPodSelector returns spec.podSelector of the one emitted
// NetworkPolicy that NetworkPolicy synthesis generated for the component called
// name (synthesized true) or of the one it did not (synthesized false),
// identified by resource name. It fails the test unless
// exactly one such policy is emitted, so a negative case cannot silently break
// a policy of the other class.
func networkPolicyPodSelector(t *testing.T, docs []map[string]any, name string, synthesized bool) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, d := range docs {
		if d["kind"] != "NetworkPolicy" {
			continue
		}
		md, _ := d["metadata"].(map[string]any)
		objName, _ := md["name"].(string)
		if slices.Contains(synthesizedNetworkPolicyNames(name), objName) == synthesized {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d NetworkPolicies with synthesized=%v in the rendered output, want exactly 1", len(found), synthesized)
	}
	return childMap(t, childMap(t, found[0], "spec"), "podSelector")
}

// TestComponentLabelInvariant_RejectsBrokenSelectors: the invariant check
// fails on selector shapes it once let through — a matchExpressions value it
// never read, an expression it never evaluated, a synthesized component-key
// selector it skipped before validating or matching it, and a selector
// requiring the component key over pod templates that do not carry it — so a
// generated selector carrying any of them cannot pass the guard. Each case
// breaks one emitted selector of a real render and names the report it must
// produce.
func TestComponentLabelInvariant_RejectsBrokenSelectors(t *testing.T) {
	boundary := boundaryLabelComponentName(t)
	host, hostProps := webserviceHost()
	scaler := traitLabelFixtures["scaler"]
	netpol := traitLabelFixtures["networkpolicy"]
	expose := traitLabelFixtures["expose"]
	tooLong := strings.Repeat("v", 200)

	cases := []struct {
		name  string
		trait string
		props map[string]any
		// mutate breaks one selector of the rendered output.
		mutate func(t *testing.T, docs []map[string]any)
		want   []string // substrings every one of which some report must carry
	}{
		{
			name: "pdb expression contradicts its matchLabels", trait: "scaler", props: scaler.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := childMap(t, childMap(t, findDoc(t, docs, "PodDisruptionBudget"), "spec"), "selector")
				sel["matchExpressions"] = []any{map[string]any{"key": "app", "operator": "In", "values": []any{"wrong-component"}}}
			},
			want: []string{"matches no pod template", "want oam.ComponentLabelValue"},
		},
		{
			name: "pdb expression on another key selects no pod", trait: "scaler", props: scaler.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := childMap(t, childMap(t, findDoc(t, docs, "PodDisruptionBudget"), "spec"), "selector")
				sel["matchExpressions"] = []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"backend"}}}
			},
			want: []string{"PodDisruptionBudget", "matches no pod template"},
		},
		{
			name: "pdb selector with only matchExpressions selects no pod", trait: "scaler", props: scaler.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				spec := childMap(t, findDoc(t, docs, "PodDisruptionBudget"), "spec")
				spec["selector"] = map[string]any{"matchExpressions": []any{
					map[string]any{"key": "app", "operator": "In", "values": []any{oam.ComponentLabelValue(boundary)}},
					map[string]any{"key": "tier", "operator": "Exists"},
				}}
			},
			want: []string{"PodDisruptionBudget", "matches no pod template"},
		},
		{
			name: "pdb expression value over 63 characters", trait: "scaler", props: scaler.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := childMap(t, childMap(t, findDoc(t, docs, "PodDisruptionBudget"), "spec"), "selector")
				sel["matchExpressions"] = []any{map[string]any{"key": "tier", "operator": "NotIn", "values": []any{tooLong}}}
			},
			want: []string{"matchExpressions[0].values[0]", "is not a valid label value", "is not a valid label selector"},
		},
		{
			name: "networkpolicy peer expression value over 63 characters", trait: "networkpolicy", props: netpol.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				spec := childMap(t, findDoc(t, docs, "NetworkPolicy"), "spec")
				ingress := asSlice(spec["ingress"])
				if len(ingress) == 0 {
					t.Fatal("rendered NetworkPolicy has no ingress rule")
				}
				from := asSlice(ingress[0].(map[string]any)["from"])
				if len(from) == 0 {
					t.Fatal("rendered NetworkPolicy ingress rule has no peer")
				}
				peer := childMap(t, from[0].(map[string]any), "podSelector")
				peer["matchExpressions"] = []any{map[string]any{"key": "role", "operator": "In", "values": []any{tooLong}}}
			},
			want: []string{"from[0].podSelector.matchExpressions[0].values[0]", "is not a valid label value"},
		},
		{
			name: "synthesized component selector with an invalid operator", trait: "expose", props: expose.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := networkPolicyPodSelector(t, docs, boundary, true)
				sel["matchExpressions"] = []any{map[string]any{"key": "tier", "operator": "Bogus", "values": []any{"backend"}}}
			},
			want: []string{boundary + "-allow-ingress-traffic", "spec.podSelector", "is not a valid label selector"},
		},
		{
			name: "synthesized component selector contradicts the pods", trait: "expose", props: expose.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := networkPolicyPodSelector(t, docs, boundary, true)
				sel["matchExpressions"] = []any{map[string]any{"key": "app", "operator": "NotIn", "values": []any{oam.ComponentLabelValue(boundary)}}}
			},
			want: []string{boundary + "-allow-ingress-traffic", "spec.podSelector", "matches no pod template"},
		},
		{
			// A selector that names the component key is matched against the pods
			// as emitted: it holds only because launcher put the label on the pod
			// template (go-kure/launcher#788). Without the label it matches none.
			name: "component key selector without the pod template label", trait: "networkpolicy", props: netpol.props,
			mutate: func(t *testing.T, docs []map[string]any) {
				sel := networkPolicyPodSelector(t, docs, boundary, false)
				sel["matchExpressions"] = []any{map[string]any{"key": kurelComponentLabel, "operator": "Exists"}}
				stripped := 0
				for _, doc := range docs {
					if tl := podTemplateLabels(doc); tl != nil {
						if _, has := tl[kurelComponentLabel]; has {
							delete(tl, kurelComponentLabel)
							stripped++
						}
					}
				}
				if stripped == 0 {
					t.Fatal("no pod template carried the component label")
				}
			},
			want: []string{"NetworkPolicy", "spec.podSelector", "matches no pod template", "carries no " + kurelComponentLabel + " label"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docs := renderLabelInvariant(t, labelInvariantApp(boundary, host, hostProps, tc.trait, tc.props))

			var clean recordingReporter
			checkComponentLabelInvariant(&clean, docs, boundary)
			if len(clean.errs) > 0 {
				t.Fatalf("the unbroken render already fails the invariant: %v", clean.errs)
			}

			tc.mutate(t, docs)
			var got recordingReporter
			checkComponentLabelInvariant(&got, docs, boundary)
			for _, w := range tc.want {
				if !slices.ContainsFunc(got.errs, func(e string) bool { return strings.Contains(e, w) }) {
					t.Errorf("no report mentions %q; reports: %v", w, got.errs)
				}
			}
		})
	}
}

// TestComponentLabelInvariant_SelectorsDoNotCountAsLabels: the vacuity check a
// labelled fixture relies on counts emitted `app` labels, not selector values.
// A trait output whose `app` labels are gone but whose selectors still carry the
// projected value must fail it, or the label an emitter stopped writing would go
// unnoticed.
func TestComponentLabelInvariant_SelectorsDoNotCountAsLabels(t *testing.T) {
	long := longLabelComponentName(t)
	fx := traitLabelFixtures["networkpolicy"]
	docs := renderLabelInvariant(t, labelInvariantApp(long, "passthrough", passthroughHostProps, "networkpolicy", fx.props))

	var clean recordingReporter
	requireAppLabel(&clean, checkComponentLabelInvariant(&clean, docs, long), "unbroken render")
	if len(clean.errs) > 0 {
		t.Fatalf("the unbroken render already fails: %v", clean.errs)
	}

	for _, d := range docs {
		stripAppLabels(d)
	}
	sel := childMap(t, childMap(t, findDoc(t, docs, "NetworkPolicy"), "spec"), "podSelector")
	if got := childMap(t, sel, "matchLabels")["app"]; got != oam.ComponentLabelValue(long) {
		t.Fatalf("NetworkPolicy spec.podSelector app = %v, want the projected value still present", got)
	}

	var got recordingReporter
	requireAppLabel(&got, checkComponentLabelInvariant(&got, docs, long), "stripped render")
	if !slices.ContainsFunc(got.errs, func(e string) bool { return strings.Contains(e, "emitted no `app` label") }) {
		t.Errorf("an output with selectors but no `app` label passed the vacuity check; reports: %v", got.errs)
	}
}

// stripAppLabels deletes the `app` key from every map under a `labels` key in
// obj, leaving selectors untouched.
func stripAppLabels(obj any) {
	switch v := obj.(type) {
	case map[string]any:
		for k, child := range v {
			if m, ok := child.(map[string]any); ok && k == "labels" {
				delete(m, "app")
				continue
			}
			stripAppLabels(child)
		}
	case []any:
		for _, child := range v {
			stripAppLabels(child)
		}
	}
}

// labelInvariantApp builds a one-component Application document.
func labelInvariantApp(name, typ string, props map[string]any, trait string, traitProps map[string]any) map[string]any {
	comp := map[string]any{"name": name, "type": typ, "properties": props}
	if trait != "" {
		tr := map[string]any{"type": trait}
		if traitProps != nil {
			tr["properties"] = traitProps
		}
		comp["traits"] = []any{tr}
	}
	return map[string]any{
		"apiVersion": "launcher.gokure.dev/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "label-invariant", "namespace": "default"},
		"spec":       map[string]any{"components": []any{comp}},
	}
}

func renderLabelInvariant(t *testing.T, app map[string]any) []map[string]any {
	t.Helper()
	docs, err := renderLabelInvariantErr(t, app)
	if err != nil {
		t.Fatalf("kurel build failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("kurel build emitted no documents")
	}
	return docs
}

// renderLabelInvariantErr runs `kurel build` on app with labelInvariantProfile
// and returns the emitted objects.
func renderLabelInvariantErr(t *testing.T, app map[string]any) ([]map[string]any, error) {
	t.Helper()
	appYAML, err := yaml.Marshal(app)
	if err != nil {
		t.Fatalf("marshaling test application: %v", err)
	}
	dir := t.TempDir()
	appPath := writeTempFile(t, dir, "app.yaml", string(appYAML))
	profilePath := writeTempFile(t, dir, "cluster.yaml", labelInvariantProfile)

	cmd := NewKurelCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"build", appPath, "--profile", profilePath})
	if err := cmd.Execute(); err != nil {
		return nil, err
	}
	var docs []map[string]any
	for _, raw := range strings.Split(out.String(), "\n---\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("decoding output document: %v\n%s", err, raw)
		}
		docs = append(docs, obj)
	}
	return docs, nil
}

// invariantReporter is the part of *testing.T the invariant check reports
// through, so the negative tests can run the check on a deliberately broken
// output and assert that it fails.
type invariantReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// checkComponentLabelInvariant asserts, over every emitted object:
//
//   - every label value and every selector value — matchLabels and
//     matchExpressions values alike, in any selector anywhere in the object —
//     is a valid label value;
//   - every `app` label and `app` selector value, and every synthesized
//     `launcher.gokure.dev/component` selector value, equals
//     oam.ComponentLabelValue(name) — labels and selectors agree because both
//     come from that one function;
//   - every selector that targets the component's pods (a workload's own
//     selector and scheduling selectors, a Service, a PodDisruptionBudget, a
//     NetworkPolicy) is a valid label selector — checked for every such
//     selector, before anything else — and, when the output carries pod
//     templates, evaluated whole (matchLabels and matchExpressions), matches a
//     pod template's labels emitted with it;
//   - every pod template carries `launcher.gokure.dev/component` =
//     oam.ComponentLabelValue(name): launcher labels what a component owns
//     (go-kure/launcher#788), so every selector above, a synthesized
//     NetworkPolicy's component selector included, is evaluated against the pod
//     templates exactly as emitted;
//   - the pod template a PodTemplate object stores carries that label too. It
//     runs no pods, so no selector is evaluated against it.
//
// It returns how many `app` labels it saw — label values only, never selector
// values — how many pod selectors it matched against a pod template, and how
// many stored pod templates it read, so a caller can reject a vacuous pass.
func checkComponentLabelInvariant(t invariantReporter, docs []map[string]any, name string) invariantCounts {
	t.Helper()
	want := oam.ComponentLabelValue(name)
	const componentKey = kurelComponentLabel
	appLabels, selectorsMatched, storedTemplates := 0, 0, 0
	checkValue := func(where, key string, v any, label bool) {
		s, ok := v.(string)
		if !ok {
			t.Errorf("%s: %s = %v (%T), want a string", where, key, v, v)
			return
		}
		if errs := validation.IsValidLabelValue(s); len(errs) > 0 {
			t.Errorf("%s: %s = %q (%d characters) is not a valid label value: %v", where, key, s, len(s), errs)
		}
		if key == "app" || key == componentKey {
			if label && key == "app" {
				appLabels++
			}
			if s != want {
				t.Errorf("%s: %s = %q, want oam.ComponentLabelValue(%d-character name) = %q", where, key, s, len(name), want)
			}
		}
	}

	var templates []labels.Set
	for _, doc := range docs {
		kind, _ := doc["kind"].(string)
		md, _ := doc["metadata"].(map[string]any)
		where := fmt.Sprintf("%s/%v", kind, md["name"])
		walkLabelMaps(doc, where, checkValue)
		if tl := podTemplateLabels(doc); tl != nil {
			if _, has := tl[componentKey]; !has {
				t.Errorf("%s: the pod template carries no %s label", where, componentKey)
			}
			templates = append(templates, labelSet(tl))
		}
		// A CronJob's job template is the metadata of every Job it creates.
		if kind == "CronJob" {
			spec, _ := doc["spec"].(map[string]any)
			jt, _ := spec["jobTemplate"].(map[string]any)
			jmd, _ := jt["metadata"].(map[string]any)
			jobLabels, _ := jmd["labels"].(map[string]any)
			if _, has := jobLabels[componentKey]; !has {
				t.Errorf("%s: the job template carries no %s label", where, componentKey)
			}
		}
		if tl, stored := storedPodTemplateLabels(doc); stored {
			storedTemplates++
			if _, has := tl[componentKey]; !has {
				t.Errorf("%s: the stored pod template carries no %s label", where, componentKey)
			}
		}
	}

	for _, doc := range docs {
		kind, _ := doc["kind"].(string)
		md, _ := doc["metadata"].(map[string]any)
		where := fmt.Sprintf("%s/%v", kind, md["name"])
		for _, ps := range podSelectors(doc) {
			if ps.err != nil {
				t.Errorf("%s: %s does not decode as a label selector: %v", where, ps.path, ps.err)
				continue
			}
			sel, err := metav1.LabelSelectorAsSelector(ps.selector)
			if err != nil {
				t.Errorf("%s: %s is not a valid label selector: %v", where, ps.path, err)
				continue
			}
			if len(templates) == 0 {
				continue // no pods in this output (e.g. a Service fronting another component)
			}
			selectorsMatched++
			if !slices.ContainsFunc(templates, func(tl labels.Set) bool { return sel.Matches(tl) }) {
				t.Errorf("%s: %s %q matches no pod template emitted with it (templates: %v)", where, ps.path, sel.String(), templates)
			}
		}
	}
	return invariantCounts{appLabels: appLabels, selectors: selectorsMatched, storedTemplates: storedTemplates}
}

// synthesizedNetworkPolicyNames returns the resource names NetworkPolicy
// synthesis gives the policies it generates to select the pods of the component
// called name: the routing-derived ingress allow (retargeted onto the pods a
// Service selector picks when the component routes through one) and the
// dependency-derived egress allow (pkg/oam/netpol_synthesis.go). Object
// names are the raw
// component name — only label values are projected. The external-backend
// policy (`{service}-allow-ingress-traffic`) selects another Service's pods, not
// this component's, and is not one of them.
func synthesizedNetworkPolicyNames(name string) []string {
	return []string{name + "-allow-ingress-traffic", name + "-allow-egress-traffic"}
}

// requireAppLabel reports a vacuous pass: output that must carry an `app`
// label, of which the invariant check saw none. Only emitted labels count; a
// selector carrying the value does not stand in for the label it targets.
func requireAppLabel(t invariantReporter, n invariantCounts, what string) {
	t.Helper()
	if n.appLabels == 0 {
		t.Errorf("%s emitted no `app` label; the invariant check is vacuous for it", what)
	}
}

// labelSet converts decoded pod template labels to a labels.Set. A non-string
// value is left out here; walkLabelMaps has already reported it.
func labelSet(m map[string]any) labels.Set {
	out := make(labels.Set, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// invariantCounts is what checkComponentLabelInvariant actually checked.
type invariantCounts struct {
	// appLabels counts emitted `app` labels (maps under a `labels` key: object
	// metadata and pod templates) compared with the projection. Selector values
	// are compared too but not counted: a selector alone does not show that the
	// label it targets was emitted.
	appLabels int
	selectors int // pod selectors compared with an emitted pod template
	// storedTemplates counts the pod templates a PodTemplate object stores that
	// were read for the component label.
	storedTemplates int
}

// walkLabelMaps calls check for every value of every map found under a
// `labels` or `matchLabels` key, for every value of every requirement in a
// `matchExpressions` list (with the requirement's key), and for every value of
// a Service's `spec.selector`, anywhere in obj — so the selectors nested in
// scheduling terms, namespaceSelectors and NetworkPolicy peers are covered as
// well as the top-level ones. label is true only for a value of a map under a
// `labels` key, an emitted label rather than a selector value.
func walkLabelMaps(obj any, where string, check func(where, key string, v any, label bool)) {
	switch v := obj.(type) {
	case map[string]any:
		for k, child := range v {
			if m, ok := child.(map[string]any); ok && (k == "labels" || k == "matchLabels") {
				for lk, lv := range m {
					check(where+"."+k, lk, lv, k == "labels")
				}
				continue
			}
			if exprs, ok := child.([]any); ok && k == "matchExpressions" {
				for i, e := range exprs {
					em, _ := e.(map[string]any)
					key, _ := em["key"].(string)
					for j, ev := range asSlice(em["values"]) {
						check(fmt.Sprintf("%s.%s[%d].values[%d]", where, k, i, j), key, ev, false)
					}
				}
				continue
			}
			walkLabelMaps(child, where+"."+k, check)
		}
		if v["kind"] == "Service" {
			if spec, ok := v["spec"].(map[string]any); ok {
				if sel, ok := spec["selector"].(map[string]any); ok {
					for lk, lv := range sel {
						check(where+".spec.selector", lk, lv, false)
					}
				}
			}
		}
	case []any:
		for i, child := range v {
			walkLabelMaps(child, fmt.Sprintf("%s[%d]", where, i), check)
		}
	}
}

// podTemplateLabels returns the pod template labels of a workload object.
func podTemplateLabels(doc map[string]any) map[string]any {
	spec, _ := doc["spec"].(map[string]any)
	if doc["kind"] == "CronJob" {
		jt, _ := spec["jobTemplate"].(map[string]any)
		spec, _ = jt["spec"].(map[string]any)
	}
	tmpl, _ := spec["template"].(map[string]any)
	md, _ := tmpl["metadata"].(map[string]any)
	labels, _ := md["labels"].(map[string]any)
	return labels
}

// storedPodTemplateLabels returns the labels of the pod template a PodTemplate
// object stores, and whether doc is such an object holding one. The template
// sits at the object's root, not under a spec, and runs no pods: it carries
// the component label and is no match for a pod selector, so it is read here
// and kept out of podTemplateLabels. A template without labels reads as
// stored, with a nil map.
func storedPodTemplateLabels(doc map[string]any) (map[string]any, bool) {
	if doc["kind"] != "PodTemplate" {
		return nil, false
	}
	tmpl, ok := doc["template"].(map[string]any)
	if !ok {
		return nil, false
	}
	md, _ := tmpl["metadata"].(map[string]any)
	labels, _ := md["labels"].(map[string]any)
	return labels, true
}

// podSelector is one emitted selector that picks the component's pods, decoded
// whole — matchLabels and matchExpressions — or the reason it would not decode.
type podSelector struct {
	path     string
	selector *metav1.LabelSelector
	err      error
}

// podSelectors returns every selector in doc that picks the component's own
// pods: a workload's own selector, its topology-spread and pod-(anti-)affinity
// selectors, a Service's selector, a PodDisruptionBudget's and a
// NetworkPolicy's spec.podSelector. A selector carrying only matchExpressions
// is included. Selectors that pick something else — a namespaceSelector, or a
// NetworkPolicy peer, which selects traffic sources — are not pod selectors of
// this component; walkLabelMaps still checks their values.
func podSelectors(doc map[string]any) []podSelector {
	var out []podSelector
	add := func(path string, sel any) {
		if m, ok := sel.(map[string]any); ok {
			ls, err := decodeLabelSelector(m)
			out = append(out, podSelector{path, ls, err})
		}
	}
	spec, _ := doc["spec"].(map[string]any)
	switch doc["kind"] {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		add("spec.selector", spec["selector"])
		tmpl, _ := spec["template"].(map[string]any)
		ps, _ := tmpl["spec"].(map[string]any)
		for i, c := range asSlice(ps["topologySpreadConstraints"]) {
			cm, _ := c.(map[string]any)
			add(fmt.Sprintf("topologySpreadConstraints[%d].labelSelector", i), cm["labelSelector"])
		}
		aff, _ := ps["affinity"].(map[string]any)
		for _, kind := range []string{"podAffinity", "podAntiAffinity"} {
			pa, _ := aff[kind].(map[string]any)
			for i, term := range asSlice(pa["requiredDuringSchedulingIgnoredDuringExecution"]) {
				tm, _ := term.(map[string]any)
				add(fmt.Sprintf("%s.required[%d].labelSelector", kind, i), tm["labelSelector"])
			}
			for i, w := range asSlice(pa["preferredDuringSchedulingIgnoredDuringExecution"]) {
				wm, _ := w.(map[string]any)
				tm, _ := wm["podAffinityTerm"].(map[string]any)
				add(fmt.Sprintf("%s.preferred[%d].labelSelector", kind, i), tm["labelSelector"])
			}
		}
	case "Service", "ReplicationController":
		// A plain label map, read as the matchLabels it is equivalent to.
		if sel, ok := spec["selector"].(map[string]any); ok {
			add("spec.selector", map[string]any{"matchLabels": sel})
		}
	case "PodDisruptionBudget":
		add("spec.selector", spec["selector"])
	case "NetworkPolicy":
		add("spec.podSelector", spec["podSelector"])
	}
	return out
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// decodeLabelSelector decodes an emitted selector into a metav1.LabelSelector,
// refusing a field LabelSelector does not have, so an unexpected selector shape
// fails the check instead of being dropped from it.
func decodeLabelSelector(m map[string]any) (*metav1.LabelSelector, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var ls metav1.LabelSelector
	if err := dec.Decode(&ls); err != nil {
		return nil, err
	}
	return &ls, nil
}
