package components

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	fluxoperatorv1 "github.com/controlplaneio-fluxcd/flux-operator/api/v1"
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	notificationv1 "github.com/fluxcd/notification-controller/api/v1"
	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	"github.com/go-kure/kure/pkg/stack"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	metallbv1beta2 "go.universe.tf/metallb/api/v1beta2"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestValidateLabelSelector_MatchesAPIMachinery holds validateLabelSelector to
// ValidateLabelSelectorRequirement of the linked k8s.io/apimachinery, the
// function the API server validates a match expression with. On an expression
// whose key is a label name and whose values are label values the two agree,
// under both settings of the one option the API server passes it
// (AllowInvalidLabelValueInSelector). The other option,
// AllowUnknownOperatorInRequirement, is set at one call site of Kubernetes
// v1.37.1, the validation of a SubjectAccessReview's label requirements
// (pkg/apis/authorization/validation), which is no object a kind emits.
//
// The second table is what that function refuses and validateLabelSelector
// leaves to the API server: a row that stops being refused there, or starts
// being refused here, fails.
func TestValidateLabelSelector_MatchesAPIMachinery(t *testing.T) {
	options := []metav1validation.LabelSelectorValidationOptions{{}, {AllowInvalidLabelValueInSelector: true}}
	selector := func(expr metav1.LabelSelectorRequirement) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "ok", Operator: metav1.LabelSelectorOpExists}, expr}}
	}
	for name, tc := range map[string]struct {
		expr metav1.LabelSelectorRequirement
		want string
	}{
		"In with values":            {metav1.LabelSelectorRequirement{Key: "tier", Operator: "In", Values: []string{"a", "b"}}, ""},
		"NotIn with a value":        {metav1.LabelSelectorRequirement{Key: "tier", Operator: "NotIn", Values: []string{"a"}}, ""},
		"Exists":                    {metav1.LabelSelectorRequirement{Key: "tier", Operator: "Exists"}, ""},
		"DoesNotExist":              {metav1.LabelSelectorRequirement{Key: "tier", Operator: "DoesNotExist"}, ""},
		"Exists, empty values":      {metav1.LabelSelectorRequirement{Key: "tier", Operator: "Exists", Values: []string{}}, ""},
		"In without values":         {metav1.LabelSelectorRequirement{Key: "tier", Operator: "In"}, "selector.matchExpressions[1].values: required with the operator In (at least one value)"},
		"In, empty values":          {metav1.LabelSelectorRequirement{Key: "tier", Operator: "In", Values: []string{}}, "selector.matchExpressions[1].values: required with the operator In (at least one value)"},
		"NotIn without values":      {metav1.LabelSelectorRequirement{Key: "tier", Operator: "NotIn"}, "selector.matchExpressions[1].values: required with the operator NotIn (at least one value)"},
		"Exists with a value":       {metav1.LabelSelectorRequirement{Key: "tier", Operator: "Exists", Values: []string{"a"}}, "selector.matchExpressions[1].values: not allowed with the operator Exists (it takes no value)"},
		"DoesNotExist with values":  {metav1.LabelSelectorRequirement{Key: "tier", Operator: "DoesNotExist", Values: []string{"a", "b"}}, "selector.matchExpressions[1].values: not allowed with the operator DoesNotExist (it takes no value)"},
		"no operator":               {metav1.LabelSelectorRequirement{Key: "tier"}, `selector.matchExpressions[1].operator: "" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`},
		"an unknown operator":       {metav1.LabelSelectorRequirement{Key: "tier", Operator: "Bogus", Values: []string{"a"}}, `selector.matchExpressions[1].operator: "Bogus" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`},
		"an operator in lower case": {metav1.LabelSelectorRequirement{Key: "tier", Operator: "in", Values: []string{"a"}}, `selector.matchExpressions[1].operator: "in" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`},
		"a node selector operator":  {metav1.LabelSelectorRequirement{Key: "tier", Operator: "Gt", Values: []string{"1"}}, `selector.matchExpressions[1].operator: "Gt" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateLabelSelector("selector", selector(tc.expr))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want none", err)
			case tc.want != "" && (err == nil || err.Error() != tc.want):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			for _, opts := range options {
				if errs := metav1validation.ValidateLabelSelectorRequirement(tc.expr, opts, field.NewPath("selector")); (len(errs) > 0) != (tc.want != "") {
					t.Errorf("apimachinery under %+v gives %v, and validateLabelSelector %v: the two disagree", opts, errs, err)
				}
			}
		})
	}

	for name, sel := range map[string]*metav1.LabelSelector{
		"a key that is no label name":          selector(metav1.LabelSelectorRequirement{Key: "not a label!", Operator: "Exists"}),
		"an empty key":                         selector(metav1.LabelSelectorRequirement{Operator: "Exists"}),
		"a value that is no label value":       selector(metav1.LabelSelectorRequirement{Key: "tier", Operator: "In", Values: []string{"not a value!"}}),
		"a matchLabels key that is no name":    {MatchLabels: map[string]string{"not a label!": "a"}},
		"a matchLabels value that is no value": {MatchLabels: map[string]string{"tier": "not a value!"}},
	} {
		t.Run("left to the API server: "+name, func(t *testing.T) {
			if errs := metav1validation.ValidateLabelSelector(sel, metav1validation.LabelSelectorValidationOptions{}, field.NewPath("selector")); len(errs) == 0 {
				t.Errorf("apimachinery refuses nothing of %+v; the row no longer shows a rule left to the API server", sel)
			}
			if err := validateLabelSelector("selector", sel); err != nil {
				t.Errorf("err = %v, want none: the rule is the API server's", err)
			}
		})
	}

	for name, sel := range map[string]*metav1.LabelSelector{"no selector": nil, "an empty selector": {}, "labels only": {MatchLabels: map[string]string{"tier": "a"}}} {
		if err := validateLabelSelector("selector", sel); err != nil {
			t.Errorf("%s: err = %v, want none", name, err)
		}
	}
}

// strictlyDecodedTypes maps the type argument of every strict decode of this
// package, as the source spells it, to the type: what decodeKindSpec and
// builtin.DecodeStrictJSON decode into and what a policyFreeKind or a
// policyHeldKind is declared over. TestLabelSelectorKinds_CoverEverySelector
// holds the keys to the package's source and counts the kinds of each type by
// the files that decode it, so a kind added without a row fails there.
//
// A row is one line, {name, type}: kind PRs add rows here, and a map literal's
// rows are aligned by gofmt, so one longer key would rewrite every other row.
// TestSharedKindTables_RowLocal holds that form.
var strictlyDecodedTypes = typesByName([]namedType{
	{"admissionregistrationv1.MutatingWebhookConfiguration", reflect.TypeFor[admissionregistrationv1.MutatingWebhookConfiguration]()},
	{"admissionregistrationv1.ValidatingWebhookConfiguration", reflect.TypeFor[admissionregistrationv1.ValidatingWebhookConfiguration]()},
	{"apiregistrationv1.APIServiceSpec", reflect.TypeFor[apiregistrationv1.APIServiceSpec]()},
	{"appsv1.ReplicaSetSpec", reflect.TypeFor[appsv1.ReplicaSetSpec]()},
	{"autoscalingv2.HorizontalPodAutoscalerSpec", reflect.TypeFor[autoscalingv2.HorizontalPodAutoscalerSpec]()},
	{"autov1.ImageUpdateAutomationSpec", reflect.TypeFor[autov1.ImageUpdateAutomationSpec]()},
	{"barmanv1.ObjectStoreSpec", reflect.TypeFor[barmanv1.ObjectStoreSpec]()},
	{"certv1.CertificateSpec", reflect.TypeFor[certv1.CertificateSpec]()},
	{"certv1.IssuerSpec", reflect.TypeFor[certv1.IssuerSpec]()},
	{"ciliumNetworkPolicyProperties", reflect.TypeFor[ciliumNetworkPolicyProperties]()},
	{"ciliumv2.CiliumBGPAdvertisementSpec", reflect.TypeFor[ciliumv2.CiliumBGPAdvertisementSpec]()},
	{"ciliumv2.CiliumBGPClusterConfigSpec", reflect.TypeFor[ciliumv2.CiliumBGPClusterConfigSpec]()},
	{"ciliumv2.CiliumBGPNodeConfigOverrideSpec", reflect.TypeFor[ciliumv2.CiliumBGPNodeConfigOverrideSpec]()},
	{"ciliumv2.CiliumBGPPeerConfigSpec", reflect.TypeFor[ciliumv2.CiliumBGPPeerConfigSpec]()},
	{"ciliumv2.CiliumCIDRGroupSpec", reflect.TypeFor[ciliumv2.CiliumCIDRGroupSpec]()},
	{"ciliumv2.CiliumEgressGatewayPolicySpec", reflect.TypeFor[ciliumv2.CiliumEgressGatewayPolicySpec]()},
	{"ciliumv2.CiliumLoadBalancerIPPoolSpec", reflect.TypeFor[ciliumv2.CiliumLoadBalancerIPPoolSpec]()},
	{"ciliumv2.CiliumLocalRedirectPolicySpec", reflect.TypeFor[ciliumv2.CiliumLocalRedirectPolicySpec]()},
	{"ciliumv2.CiliumNodeConfigSpec", reflect.TypeFor[ciliumv2.CiliumNodeConfigSpec]()},
	{"cnpgv1.BackupSpec", reflect.TypeFor[cnpgv1.BackupSpec]()},
	{"cnpgv1.ClusterSpec", reflect.TypeFor[cnpgv1.ClusterSpec]()},
	{"cnpgv1.DatabaseRoleSpec", reflect.TypeFor[cnpgv1.DatabaseRoleSpec]()},
	{"cnpgv1.DatabaseSpec", reflect.TypeFor[cnpgv1.DatabaseSpec]()},
	{"cnpgv1.ImageCatalogSpec", reflect.TypeFor[cnpgv1.ImageCatalogSpec]()},
	{"cnpgv1.PoolerSpec", reflect.TypeFor[cnpgv1.PoolerSpec]()},
	{"cnpgv1.PublicationSpec", reflect.TypeFor[cnpgv1.PublicationSpec]()},
	{"cnpgv1.ScheduledBackupSpec", reflect.TypeFor[cnpgv1.ScheduledBackupSpec]()},
	{"cnpgv1.SubscriptionSpec", reflect.TypeFor[cnpgv1.SubscriptionSpec]()},
	{"corev1.LimitRangeSpec", reflect.TypeFor[corev1.LimitRangeSpec]()},
	{"corev1.NamespaceSpec", reflect.TypeFor[corev1.NamespaceSpec]()},
	{"corev1.PersistentVolumeSpec", reflect.TypeFor[corev1.PersistentVolumeSpec]()},
	{"corev1.PodSpec", reflect.TypeFor[corev1.PodSpec]()},
	{"corev1.ReplicationControllerSpec", reflect.TypeFor[corev1.ReplicationControllerSpec]()},
	{"corev1.ResourceQuotaSpec", reflect.TypeFor[corev1.ResourceQuotaSpec]()},
	{"discoveryv1.EndpointSlice", reflect.TypeFor[discoveryv1.EndpointSlice]()},
	{"esv1.ClusterExternalSecretSpec", reflect.TypeFor[esv1.ClusterExternalSecretSpec]()},
	{"esv1.ExternalSecretSpec", reflect.TypeFor[esv1.ExternalSecretSpec]()},
	{"esv1.SecretStoreSpec", reflect.TypeFor[esv1.SecretStoreSpec]()},
	{"fluxoperatorv1.ResourceSetInputProviderSpec", reflect.TypeFor[fluxoperatorv1.ResourceSetInputProviderSpec]()},
	{"gatewayv1.BackendTLSPolicySpec", reflect.TypeFor[gatewayv1.BackendTLSPolicySpec]()},
	{"gatewayv1.GatewayClassSpec", reflect.TypeFor[gatewayv1.GatewayClassSpec]()},
	{"gatewayv1.GatewaySpec", reflect.TypeFor[gatewayv1.GatewaySpec]()},
	{"gatewayv1.GRPCRouteSpec", reflect.TypeFor[gatewayv1.GRPCRouteSpec]()},
	{"gatewayv1.HTTPRouteSpec", reflect.TypeFor[gatewayv1.HTTPRouteSpec]()},
	{"gatewayv1.ListenerSetSpec", reflect.TypeFor[gatewayv1.ListenerSetSpec]()},
	{"gatewayv1.ReferenceGrantSpec", reflect.TypeFor[gatewayv1.ReferenceGrantSpec]()},
	{"gatewayv1.TCPRouteSpec", reflect.TypeFor[gatewayv1.TCPRouteSpec]()},
	{"gatewayv1.TLSRouteSpec", reflect.TypeFor[gatewayv1.TLSRouteSpec]()},
	{"gatewayv1.UDPRouteSpec", reflect.TypeFor[gatewayv1.UDPRouteSpec]()},
	{"helmProperties", reflect.TypeFor[helmProperties]()},
	{"helmTemplateProperties", reflect.TypeFor[helmTemplateProperties]()},
	{"helmValuesFromSpec", reflect.TypeFor[helmValuesFromSpec]()},
	{"helmv2.HelmReleaseSpec", reflect.TypeFor[helmv2.HelmReleaseSpec]()},
	{"imagev1.ImagePolicySpec", reflect.TypeFor[imagev1.ImagePolicySpec]()},
	{"imagev1.ImageRepositorySpec", reflect.TypeFor[imagev1.ImageRepositorySpec]()},
	{"kustv1.KustomizationSpec", reflect.TypeFor[kustv1.KustomizationSpec]()},
	{"metallbv1beta1.BFDProfileSpec", reflect.TypeFor[metallbv1beta1.BFDProfileSpec]()},
	{"metallbv1beta1.BGPAdvertisementSpec", reflect.TypeFor[metallbv1beta1.BGPAdvertisementSpec]()},
	{"metallbv1beta1.CommunitySpec", reflect.TypeFor[metallbv1beta1.CommunitySpec]()},
	{"metallbv1beta1.IPAddressPoolSpec", reflect.TypeFor[metallbv1beta1.IPAddressPoolSpec]()},
	{"metallbv1beta1.L2AdvertisementSpec", reflect.TypeFor[metallbv1beta1.L2AdvertisementSpec]()},
	{"metallbv1beta2.BGPPeerSpec", reflect.TypeFor[metallbv1beta2.BGPPeerSpec]()},
	{"monitoringv1.AlertmanagerSpec", reflect.TypeFor[monitoringv1.AlertmanagerSpec]()},
	{"monitoringv1.PodMonitorSpec", reflect.TypeFor[monitoringv1.PodMonitorSpec]()},
	{"monitoringv1.ProbeSpec", reflect.TypeFor[monitoringv1.ProbeSpec]()},
	{"monitoringv1.PrometheusRuleSpec", reflect.TypeFor[monitoringv1.PrometheusRuleSpec]()},
	{"monitoringv1.ServiceMonitorSpec", reflect.TypeFor[monitoringv1.ServiceMonitorSpec]()},
	{"monitoringv1.ThanosRulerSpec", reflect.TypeFor[monitoringv1.ThanosRulerSpec]()},
	{"networkingv1.IngressClassSpec", reflect.TypeFor[networkingv1.IngressClassSpec]()},
	{"networkingv1.IngressSpec", reflect.TypeFor[networkingv1.IngressSpec]()},
	{"networkingv1.NetworkPolicySpec", reflect.TypeFor[networkingv1.NetworkPolicySpec]()},
	{"networkingv1.ServiceCIDRSpec", reflect.TypeFor[networkingv1.ServiceCIDRSpec]()},
	{"nodev1.RuntimeClass", reflect.TypeFor[nodev1.RuntimeClass]()},
	{"notificationv1.ReceiverSpec", reflect.TypeFor[notificationv1.ReceiverSpec]()},
	{"notificationv1beta3.AlertSpec", reflect.TypeFor[notificationv1beta3.AlertSpec]()},
	{"notificationv1beta3.ProviderSpec", reflect.TypeFor[notificationv1beta3.ProviderSpec]()},
	{"podTemplateProperties", reflect.TypeFor[podTemplateProperties]()},
	{"policyv1.PodDisruptionBudgetSpec", reflect.TypeFor[policyv1.PodDisruptionBudgetSpec]()},
	{"rbacv1.ClusterRole", reflect.TypeFor[rbacv1.ClusterRole]()},
	{"rbacv1.ClusterRoleBinding", reflect.TypeFor[rbacv1.ClusterRoleBinding]()},
	{"rbacv1.Role", reflect.TypeFor[rbacv1.Role]()},
	{"rbacv1.RoleBinding", reflect.TypeFor[rbacv1.RoleBinding]()},
	{"schedulingv1.PriorityClass", reflect.TypeFor[schedulingv1.PriorityClass]()},
	{"sourcev1.BucketSpec", reflect.TypeFor[sourcev1.BucketSpec]()},
	{"sourcev1.GitRepositorySpec", reflect.TypeFor[sourcev1.GitRepositorySpec]()},
	{"sourcev1.HelmChartSpec", reflect.TypeFor[sourcev1.HelmChartSpec]()},
	{"sourcev1.HelmRepositorySpec", reflect.TypeFor[sourcev1.HelmRepositorySpec]()},
	{"sourcev1.OCIRepositorySpec", reflect.TypeFor[sourcev1.OCIRepositorySpec]()},
	{"storagev1.CSIDriverSpec", reflect.TypeFor[storagev1.CSIDriverSpec]()},
	{"storagev1.StorageClass", reflect.TypeFor[storagev1.StorageClass]()},
	{"storagev1.VolumeAttributesClass", reflect.TypeFor[storagev1.VolumeAttributesClass]()},
	{"swv1beta1.ArtifactGeneratorSpec", reflect.TypeFor[swv1beta1.ArtifactGeneratorSpec]()},
	{"thanosRulerExternalLabels", reflect.TypeFor[thanosRulerExternalLabels]()},
	{"volsyncv1alpha1.ReplicationDestinationSpec", reflect.TypeFor[volsyncv1alpha1.ReplicationDestinationSpec]()},
	{"volsyncv1alpha1.ReplicationSourceSpec", reflect.TypeFor[volsyncv1alpha1.ReplicationSourceSpec]()},
})

// namedType is a row of strictlyDecodedTypes: a type argument as the source
// spells it, and the type.
type namedType struct {
	name string
	typ  reflect.Type
}

// typesByName returns the rows by name. A name listed twice panics, where a
// map literal's duplicate key would not compile.
func typesByName(rows []namedType) map[string]reflect.Type {
	byName := make(map[string]reflect.Type, len(rows))
	for _, row := range rows {
		if _, ok := byName[row.name]; ok {
			panic("strictlyDecodedTypes lists " + row.name + " twice")
		}
		byName[row.name] = row.typ
	}
	return byName
}

// TestSharedKindTables_RowLocal holds the two shared kind tables gofmt would
// otherwise align across rows, strictlyDecodedTypes in this file and
// componentLabelFixtures in pkg/cmd/kurel, to a form in which adding or
// removing a row changes that row's lines alone: here every row is a
// positional {name, type} pair, which gofmt does not align. The kurel table's
// half of the guard is TestComponentLabelFixtures_RowLocal there.
func TestSharedKindTables_RowLocal(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "label_selector_internal_test.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse label_selector_internal_test.go: %v", err)
	}
	rows := sharedTableRows(t, file, "strictlyDecodedTypes")
	for _, row := range rows {
		lit, ok := row.(*ast.CompositeLit)
		if !ok || len(lit.Elts) != 2 || slices.ContainsFunc(lit.Elts, func(e ast.Expr) bool { _, keyed := e.(*ast.KeyValueExpr); return keyed }) {
			t.Errorf("strictlyDecodedTypes: the row at %s is not a positional {name, type} pair; gofmt aligns keyed rows, so a row added beside it rewrites its neighbours", fset.Position(row.Pos()))
		}
	}
	if len(rows) != len(strictlyDecodedTypes) {
		t.Errorf("strictlyDecodedTypes: the guard read %d rows, the table holds %d", len(rows), len(strictlyDecodedTypes))
	}
}

// sharedTableRows returns the elements of the composite literal the package
// variable name is declared with: the literal itself, or the one literal
// argument of the call that builds it.
func sharedTableRows(t *testing.T, file *ast.File, name string) []ast.Expr {
	t.Helper()
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != name || len(value.Values) != 1 {
				continue
			}
			expr := value.Values[0]
			if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 1 {
				expr = call.Args[0]
			}
			if lit, ok := expr.(*ast.CompositeLit); ok {
				return lit.Elts
			}
			t.Fatalf("%s is declared with %s, not a composite literal", name, types.ExprString(expr))
		}
	}
	t.Fatalf("no package variable %s", name)
	return nil
}

// strictDecodeSites reads the package's source, test files apart, and returns
// the type argument of every strict decode in it, as spelled, with the files
// that decode it, in order: of a call to decodeKindSpec or
// builtin.DecodeStrictJSON, and of a policyFreeKind or policyHeldKind value.
// The type parameter of the generic code itself is not one. A kind component
// is declared in a file of its own, so two files that decode one type are two
// kinds of it (issuer and clusterissuer).
func strictDecodeSites(t *testing.T) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	found := map[string][]string{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			index, ok := n.(*ast.IndexExpr)
			if !ok {
				return true
			}
			switch types.ExprString(index.X) {
			case "decodeKindSpec", "builtin.DecodeStrictJSON", "policyFreeKind", "policyHeldKind":
				if arg := types.ExprString(index.Index); arg != "T" && !slices.Contains(found[arg], name) {
					found[arg] = append(found[arg], name)
				}
			}
			return true
		})
	}
	return found
}

// labelSelectorPaths returns the json paths, with [] for a list element and {}
// for a map value, of every metav1.LabelSelector the encoding of typ reaches.
// A struct that embeds one inline, as the Flux Operator's
// ExternalArtifactSelector does, holds its matchLabels and matchExpressions at
// its own path, and is reported there: walkKindFields promotes the embedded
// fields and visits no field of the selector's own type.
// TestLabelSelectorPaths_FindsAnInlineSelector holds that.
func labelSelectorPaths(typ reflect.Type) []string {
	selector := reflect.TypeFor[metav1.LabelSelector]()
	var inline func(t reflect.Type) bool
	inline = func(t reflect.Type) bool {
		if t.Kind() != reflect.Struct {
			return false
		}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if !f.Anonymous || name != "" {
				continue
			}
			embedded := f.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded == selector || inline(embedded) {
				return true
			}
		}
		return false
	}
	var paths []string
	walkKindFields(typ, func(kindField) bool { return true }, func(f kindField) {
		at, path := f.field.Type, f.path
	unwrap:
		for {
			switch at.Kind() {
			case reflect.Pointer:
				at = at.Elem()
			case reflect.Slice, reflect.Array:
				at, path = at.Elem(), path+"[]"
			case reflect.Map:
				at, path = at.Elem(), path+"{}"
			default:
				break unwrap
			}
		}
		if at == selector || inline(at) {
			paths = append(paths, path)
		}
	})
	slices.Sort(paths)
	return paths
}

// TestLabelSelectorPaths_FindsAnInlineSelector holds labelSelectorPaths to a
// selector embedded inline, which walkKindFields promotes away: on a type made
// for the test, beside a selector named by a field, and on the one kind type
// that embeds one, the Flux Operator's ResourceSetInputProviderSpec. A walk that
// loses the inline selector leaves its kind out of
// TestLabelSelectorKinds_CoverEverySelector, and fails here.
func TestLabelSelectorPaths_FindsAnInlineSelector(t *testing.T) {
	type inlineSelector struct {
		metav1.LabelSelector `json:",inline"`
		Name                 string `json:"name,omitempty"`
	}
	type deeper struct {
		inlineSelector `json:",inline"`
	}
	type probe struct {
		Named   *metav1.LabelSelector `json:"named,omitempty"`
		Listed  []inlineSelector      `json:"listed,omitempty"`
		Pointer *deeper               `json:"pointer,omitempty"`
		Keyed   map[string]deeper     `json:"keyed,omitempty"`
	}
	if got, want := labelSelectorPaths(reflect.TypeFor[probe]()), []string{"keyed{}", "listed[]", "named", "pointer"}; !slices.Equal(got, want) {
		t.Errorf("labelSelectorPaths(probe) = %v, want %v", got, want)
	}
	if got, want := labelSelectorPaths(reflect.TypeFor[fluxoperatorv1.ResourceSetInputProviderSpec]()), []string{"selectors[]"}; !slices.Equal(got, want) {
		t.Errorf("labelSelectorPaths(ResourceSetInputProviderSpec) = %v, want %v", got, want)
	}
}

// kindConfig is the ToApplicationConfig of a handler, in the build namespace
// the test uses.
func kindConfig(h interface {
	ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error)
}) func(*oam.Component) (stack.ApplicationConfig, error) {
	return func(c *oam.Component) (stack.ApplicationConfig, error) { return h.ToApplicationConfig(c, "default") }
}

// labelSelectorKind is one kind component whose type holds a label selector.
type labelSelectorKind struct {
	component string
	// typ is the key of strictlyDecodedTypes the component's properties decode
	// into.
	typ    string
	config func(*oam.Component) (stack.ApplicationConfig, error)
	// base is a set of properties the component builds from; a selector is
	// written into a copy of it.
	base map[string]any
	// expressions says the kind holds its selectors to validateLabelSelector
	// beside the presence of key and operator. Without it the operator and its
	// values are left to the API the kind's object belongs to.
	expressions bool
	// excluded maps a selector path the kind does not hold to the reason.
	excluded map[string]string
	// crds, on a kind that holds presence only, reads the CRDs of the linked
	// module that define the kind's object: each must require key and operator
	// at every selector path. A kind without one states its ground instead.
	crds   func(t *testing.T) []crdSchema
	ground string
}

// crdSchema is one CRD that defines the object of a presence-only kind, in the
// version the kind emits: the properties of its spec by json path and the
// paths their parent requires (schemaProperties), and the API server's own
// answer for a document of it (crdCreate).
type crdSchema struct {
	name     string
	props    map[string]apiextensionsv1.JSONSchemaProps
	required map[string]bool
	create   *crdCreate
}

// linkedCRDSchema reads the CRD in file, a path under the directory of the
// linked module, and prepares its version as the API server serves it.
func linkedCRDSchema(t *testing.T, modulePath, file, version string) crdSchema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(linkedModuleDir(t, modulePath), filepath.FromSlash(file)))
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(data, crd); err != nil {
		t.Fatalf("decode the CRD %s: %v", file, err)
	}
	at := slices.IndexFunc(crd.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool { return v.Name == version })
	if at < 0 || crd.Spec.Versions[at].Schema == nil || crd.Spec.Versions[at].Schema.OpenAPIV3Schema == nil {
		t.Fatalf("%s has no schema for the version %s", file, version)
	}
	spec, ok := crd.Spec.Versions[at].Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		t.Fatalf("%s has no spec", file)
	}
	props, required := schemaProperties(spec)
	return crdSchema{
		name: file, props: props, required: required,
		create: crdCreateOf(t, crd, version),
	}
}

// show holds the selector at path, of a presence-only kind, to the CRD: by the
// schema's required lists, and by the API server's own answer for the object
// the kind emits.
//
// With a whole expression the API server accepts the object. With the
// expression's key, or its operator, taken out of that object, it refuses it,
// for that field's absence and nothing else: that is the rule the kind's
// refusal repeats. With the field written as the empty string, which is what
// the Go type writes for one that was not authored, it accepts the object
// again: the schema requires the field and bounds no value of it. The object
// the kind would emit for an unauthored field therefore neither shows the
// omission nor is refused for it, and only the kind, which reads what was
// authored, can tell the author.
func (s crdSchema) show(t *testing.T, kind labelSelectorKind, path string) {
	t.Helper()
	at := strings.ReplaceAll(path, "[]", "[0]") + ".matchExpressions[0]."
	// create answers the create of the object the kind emits for a whole
	// expression at the selector, after change has edited that expression in it.
	create := func(change func(expr map[string]any)) crdAnswer {
		props := withValueAt(kind.base, path, map[string]any{"matchExpressions": []any{
			map[string]any{"key": "tier", "operator": "In", "values": []any{"a"}},
		}})
		object := kind.object(t, props)
		spec, _ := object["spec"].(map[string]any)
		expr, _ := fieldAt(t, spec, at+"key")
		change(expr)
		return s.create.create(t, object)
	}
	what := s.name + ": " + path
	create(func(map[string]any) {}).accepted(t, what+" with a whole expression")
	for _, name := range []string{"key", "operator"} {
		prop := path + ".matchExpressions[]." + name
		if _, ok := s.props[prop]; !ok {
			t.Errorf("%s is no property of the CRD %s; the paths are keyed wrongly, or the CRD does not hold the selector", prop, s.name)
			continue
		}
		if !s.required[prop] {
			t.Errorf("%s is not required by the CRD %s; the kind refuses what the API admits", prop, s.name)
		}
		want := "spec." + at + name
		if answer := create(func(expr map[string]any) { delete(expr, name) }); len(answer.unknown) > 0 || len(answer.rules) > 0 ||
			len(answer.schema) != 1 || answer.schema[0].Type != field.ErrorTypeRequired || answer.schema[0].Field != want {
			t.Errorf("%s with an expression without its %s: the API server answers %q, want one refusal, of %s as required", what, name, answer, want)
		}
		create(func(expr map[string]any) { expr[name] = "" }).accepted(t, what+" with an expression whose "+name+" is the empty string the type writes")
	}
}

// object returns the one object the kind emits for the properties, as the JSON
// it is written as.
func (kind labelSelectorKind) object(t *testing.T, props map[string]any) map[string]any {
	t.Helper()
	cfg, err := kind.config(&oam.Component{Name: "web", Type: kind.component, Properties: props})
	if err != nil {
		t.Fatalf("build the kind: %v", err)
	}
	objs, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
	if err != nil {
		t.Fatalf("generate the kind's object: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("the kind emits %d objects, want one", len(objs))
	}
	raw, err := json.Marshal(*objs[0])
	if err != nil {
		t.Fatalf("encode the object: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode the object: %v", err)
	}
	return out
}

// crdFile reads one CRD file of a linked module, in its v1 version.
func crdFile(modulePath, file string) func(t *testing.T) []crdSchema {
	return crdFileVersion(modulePath, file, "v1")
}

// crdFileVersion reads one CRD file of a linked module, in the version named.
func crdFileVersion(modulePath, file, version string) func(t *testing.T) []crdSchema {
	return func(t *testing.T) []crdSchema {
		t.Helper()
		return []crdSchema{linkedCRDSchema(t, modulePath, file, version)}
	}
}

// gatewayAPICRDs reads one Gateway API CRD in both of its channels: a cluster
// holds either.
func gatewayAPICRDs(name string) func(t *testing.T) []crdSchema {
	return func(t *testing.T) []crdSchema {
		t.Helper()
		var out []crdSchema
		for _, channel := range gatewayAPIChannels {
			out = append(out, linkedCRDSchema(t, gatewayAPIModulePath, gatewayAPICRDFile(channel, name), "v1"))
		}
		return out
	}
}

var labelSelectorTestContainers = []any{map[string]any{"name": "app", "image": "registry.example.com/app:1.0"}}

// labelSelectorTestWebhooks is a webhook configuration with one whole webhook,
// whose two selectors the test writes.
var labelSelectorTestWebhooks = map[string]any{"webhooks": []any{map[string]any{
	"name":                    "policy.example.com",
	"clientConfig":            map[string]any{"url": "https://policy.example.com/check"},
	"sideEffects":             "None",
	"admissionReviewVersions": []any{"v1"},
}}}

// labelSelectorKinds lists every kind component whose type reaches a
// metav1.LabelSelector.
//
// What the walk does not seek is out of this change's scope, each for its own
// reason. Cilium's API declares a copy of the selector type; the kinds that
// decode it list key and operator already and are held to the CRDs of the
// linked module (TestCiliumBGPKinds_RequiredMatchCRD and its siblings). The
// workload kinds and the claim templates decode no type: they parse a selector
// by hand and refuse key, operator and values there (parseLabelSelectorOpts).
// A node selector term (corev1.NodeSelectorTerm) and a quota's scope selector
// (corev1.ScopeSelector) are other types, with other operators and validators
// of their own in the API server.
var labelSelectorKinds = []labelSelectorKind{
	{
		component: "networkpolicy", typ: "networkingv1.NetworkPolicySpec", config: kindConfig(&NetworkPolicyHandler{}),
		base: map[string]any{}, expressions: true,
	},
	{
		component: "poddisruptionbudget", typ: "policyv1.PodDisruptionBudgetSpec", config: kindConfig(&PodDisruptionBudgetHandler{}),
		base: map[string]any{}, expressions: true,
	},
	{
		component: "pod", typ: "corev1.PodSpec", config: kindConfig(&PodHandler{}),
		base: map[string]any{"containers": labelSelectorTestContainers}, expressions: true,
	},
	{
		component: "podtemplate", typ: "podTemplateProperties", config: kindConfig(&PodTemplateHandler{}),
		base: map[string]any{"template": map[string]any{"spec": map[string]any{"containers": labelSelectorTestContainers}}}, expressions: true,
	},
	{
		component: "replicaset", typ: "appsv1.ReplicaSetSpec", config: kindConfig(&ReplicaSetHandler{}),
		base: map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"tier": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"tier": "web"}},
				"spec":     map[string]any{"containers": labelSelectorTestContainers},
			},
		},
		expressions: true,
		excluded: map[string]string{
			"selector": "refused already, and wholly: the kind reads it as apimachinery does to compare it with the `app` label (refuseSelectorAgainstAppLabel), which refuses an expression without a key, with an operator that is none of the four or with the wrong number of values, in apimachinery's words (TestReplicaSetSelector_RefusedAsAPIMachineryReadsIt)",
		},
	},
	{
		component: "replicationcontroller", typ: "corev1.ReplicationControllerSpec", config: kindConfig(&ReplicationControllerHandler{}),
		base: map[string]any{"template": map[string]any{"spec": map[string]any{"containers": labelSelectorTestContainers}}}, expressions: true,
	},
	{
		component: "clusterrole", typ: "rbacv1.ClusterRole", config: kindConfig(&ClusterRoleHandler{}),
		base: map[string]any{}, expressions: true,
	},
	{
		component: "validatingwebhookconfiguration", typ: "admissionregistrationv1.ValidatingWebhookConfiguration", config: kindConfig(&ValidatingWebhookConfigurationHandler{}),
		base: labelSelectorTestWebhooks, expressions: true,
	},
	{
		component: "mutatingwebhookconfiguration", typ: "admissionregistrationv1.MutatingWebhookConfiguration", config: kindConfig(&MutatingWebhookConfigurationHandler{}),
		base: labelSelectorTestWebhooks, expressions: true,
	},
	{
		component: "horizontalpodautoscaler", typ: "autoscalingv2.HorizontalPodAutoscalerSpec",
		excluded: map[string]string{
			"metrics[].object.metric.selector":   hpaMetricSelectorReason,
			"metrics[].pods.metric.selector":     hpaMetricSelectorReason,
			"metrics[].external.metric.selector": hpaMetricSelectorReason,
		},
	},
	{
		component: "cilium-nodeconfig", typ: "ciliumv2.CiliumNodeConfigSpec", config: kindConfig(&CiliumNodeConfigHandler{}),
		base:   map[string]any{"defaults": map[string]any{"debug": "true"}},
		ground: "the CiliumNodeConfig CRD of the linked module, which TestCiliumPlainKinds_RequiredMatchCRD holds the kind's whole required list to",
	},
	{
		component: "servicemonitor", typ: "monitoringv1.ServiceMonitorSpec", config: kindConfig(&ServiceMonitorHandler{}),
		base:   map[string]any{"endpoints": []any{}, "selector": map[string]any{}},
		ground: generatorRuleGround,
	},
	{
		component: "podmonitor", typ: "monitoringv1.PodMonitorSpec", config: kindConfig(&PodMonitorHandler{}),
		base:   map[string]any{"selector": map[string]any{}},
		ground: generatorRuleGround,
	},
	{
		component: "prometheus-probe", typ: "monitoringv1.ProbeSpec", config: kindConfig(&PrometheusProbeHandler{}),
		base:   map[string]any{"prober": map[string]any{"url": "blackbox-exporter:9115"}},
		ground: generatorRuleGround,
	},
	{
		component: "alertmanager", typ: "monitoringv1.AlertmanagerSpec", config: kindConfig(&AlertmanagerHandler{}),
		// The storage arm a selector is written into must claim storage.
		base:   map[string]any{"storage": amClaimingStorage()},
		ground: generatorRuleGround,
	},
	{
		component: "thanosruler", typ: "monitoringv1.ThanosRulerSpec", config: kindConfig(&ThanosRulerHandler{}),
		// The storage arm a selector is written into must claim storage.
		base:   map[string]any{"storage": amClaimingStorage()},
		ground: generatorRuleGround,
	},
	{
		component: "cnpg-cluster", typ: "cnpgv1.ClusterSpec", config: kindConfig(&CnpgClusterHandler{}),
		// The entry carries the name the kind requires of it, so that a selector
		// written into it is the only thing a case changes.
		base: map[string]any{"podSelectorRefs": []any{map[string]any{"name": "apps"}}},
		crds: crdFile(cnpgModulePath, cnpgCRDs+"clusters.yaml"),
	},
	{
		component: "cnpg-pooler", typ: "cnpgv1.PoolerSpec", config: kindConfig(&CnpgPoolerHandler{}),
		base: map[string]any{"cluster": map[string]any{"name": "db"}, "pgbouncer": map[string]any{}},
		crds: crdFile(cnpgModulePath, cnpgCRDs+"poolers.yaml"),
	},
	{
		component: "issuer", typ: "certv1.IssuerSpec", config: kindConfig(&IssuerHandler{}),
		base: labelSelectorTestIssuer,
		crds: crdFile(certManagerModulePath, certManagerCRDs+"issuers.yaml"),
	},
	{
		component: "clusterissuer", typ: "certv1.IssuerSpec", config: kindConfig(&ClusterIssuerHandler{}),
		base: labelSelectorTestIssuer,
		crds: crdFile(certManagerModulePath, certManagerCRDs+"clusterissuers.yaml"),
	},
	{
		component: "gateway", typ: "gatewayv1.GatewaySpec", config: kindConfig(&GatewayHandler{}),
		base: map[string]any{"gatewayClassName": "public", "listeners": labelSelectorTestListeners},
		crds: gatewayAPICRDs("gateways"),
	},
	{
		component: "listenerset", typ: "gatewayv1.ListenerSetSpec", config: kindConfig(&ListenerSetHandler{}),
		base: map[string]any{"parentRef": map[string]any{"name": "public"}, "listeners": labelSelectorTestListeners},
		crds: gatewayAPICRDs("listenersets"),
	},
	{
		component: "secretstore", typ: "esv1.SecretStoreSpec", config: kindConfig(&SecretStoreHandler{}),
		base:   labelSelectorTestStore,
		ground: externalSecretsRuleGround,
	},
	{
		component: "clustersecretstore", typ: "esv1.SecretStoreSpec", config: kindConfig(&ClusterSecretStoreHandler{}),
		base:   labelSelectorTestStore,
		ground: externalSecretsRuleGround,
	},
	{
		component: "clusterexternalsecret", typ: "esv1.ClusterExternalSecretSpec", config: kindConfig(&ClusterExternalSecretHandler{}),
		base:   map[string]any{"externalSecretSpec": map[string]any{}},
		ground: externalSecretsRuleGround,
	},
	{
		component: "imageupdateautomation", typ: "autov1.ImageUpdateAutomationSpec", config: kindConfig(&ImageUpdateAutomationHandler{}),
		base:   map[string]any{"sourceRef": map[string]any{"kind": "GitRepository", "name": "fleet"}, "interval": "30m"},
		ground: fluxRuleGround,
	},
	{
		component: "resourcesetinputprovider", typ: "fluxoperatorv1.ResourceSetInputProviderSpec", config: kindConfig(&ResourceSetInputProviderHandler{}),
		base: map[string]any{"type": "ExternalArtifact"},
		crds: crdFile("github.com/controlplaneio-fluxcd/flux-operator", fluxOperatorCRDs[resourceSetInputProviderType]),
	},
	{
		component: "replicationsource", typ: "volsyncv1alpha1.ReplicationSourceSpec", config: kindConfig(&ReplicationSourceHandler{}),
		base: map[string]any{"sourcePVC": "data"},
		crds: crdFileVersion(volsyncModulePath, volsyncCRDs+"replicationsources.yaml", "v1alpha1"),
	},
	{
		component: "replicationdestination", typ: "volsyncv1alpha1.ReplicationDestinationSpec", config: kindConfig(&ReplicationDestinationHandler{}),
		base: map[string]any{},
		crds: crdFileVersion(volsyncModulePath, volsyncCRDs+"replicationdestinations.yaml", "v1alpha1"),
	},
	{
		component: "metallb-ipaddresspool", typ: "metallbv1beta1.IPAddressPoolSpec", config: kindConfig(&MetalLBIPAddressPoolHandler{}),
		base: map[string]any{"addresses": []any{"192.0.2.0/24"}},
		crds: crdFileVersion(metallbModulePath, metallbCRDDir+"/metallb.io_ipaddresspools.yaml", metallbVersion),
	},
	{
		component: "metallb-l2advertisement", typ: "metallbv1beta1.L2AdvertisementSpec", config: kindConfig(&MetalLBL2AdvertisementHandler{}),
		base: map[string]any{},
		crds: crdFileVersion(metallbModulePath, metallbCRDDir+"/metallb.io_l2advertisements.yaml", metallbVersion),
	},
	{
		component: "metallb-bgpadvertisement", typ: "metallbv1beta1.BGPAdvertisementSpec", config: kindConfig(&MetalLBBGPAdvertisementHandler{}),
		base: map[string]any{},
		crds: crdFileVersion(metallbModulePath, metallbCRDDir+"/metallb.io_bgpadvertisements.yaml", metallbVersion),
	},
	{
		component: "metallb-bgppeer", typ: "metallbv1beta2.BGPPeerSpec", config: kindConfig(&MetalLBBGPPeerHandler{}),
		base: map[string]any{"myASN": 64512},
		crds: crdFileVersion(metallbModulePath, metallbCRDDir+"/metallb.io_bgppeers.yaml", metallbPeerVersion),
	},
}

var (
	labelSelectorTestIssuer = map[string]any{"acme": map[string]any{
		"server":              "https://acme.example.com/directory",
		"privateKeySecretRef": map[string]any{"name": "acme-account"},
	}}
	labelSelectorTestListeners = []any{map[string]any{"name": "http", "port": 80, "protocol": "HTTP"}}
	// labelSelectorTestStore configures the one provider a store must, with
	// nothing else of it.
	labelSelectorTestStore = map[string]any{"provider": map[string]any{"fake": map[string]any{"data": []any{}}}}
)

// generatorRuleGround is the ground of a kind whose object a CRD defines that
// the linked module does not ship: the Prometheus operator's.
const generatorRuleGround = "the source of the linked metav1.LabelSelectorRequirement by the schema generators' rule, no optional marker and no omitempty, from which TestMonitoringKinds_RequiredMatchMarkers derives the kind's whole required list; the module ships no CRD to read"

// externalSecretsRuleGround is generatorRuleGround for the External Secrets
// Operator's kinds.
const externalSecretsRuleGround = "the source of the linked metav1.LabelSelectorRequirement by the schema generators' rule, no optional marker and no omitempty, from which TestExternalSecretsKinds_RequiredMatchSource derives the expressions of the kind's required list; the module ships no CRD to read"

// fluxRuleGround is generatorRuleGround for the kinds of the Flux APIs.
const fluxRuleGround = "the source of the linked metav1.LabelSelectorRequirement by the schema generators' rule, no optional marker and no omitempty, from which TestFluxKinds_RequiredMatchMarkers derives the expressions of the kind's required list; the module ships no CRD to read"

// hpaMetricSelectorReason is why the horizontalpodautoscaler kind holds no
// metric selector.
const hpaMetricSelectorReason = "the API server validates nothing of a metric selector (pkg/apis/autoscaling/validation, Kubernetes v1.37.1, does not read it), and a kind does not refuse what the API admits"

// withValueAt returns a copy of props with value written at the json path
// path, in which [] names the first element of a list, creating what the path
// crosses.
func withValueAt(props map[string]any, path string, value any) map[string]any {
	var clone func(v any) any
	clone = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(v))
			for k, e := range v {
				out[k] = clone(e)
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, e := range v {
				out[i] = clone(e)
			}
			return out
		}
		return v
	}
	out := clone(props).(map[string]any)
	node := out
	segments := strings.Split(path, ".")
	for i, segment := range segments {
		name, list := strings.CutSuffix(segment, "[]")
		last := i == len(segments)-1
		switch {
		case last && list:
			node[name] = []any{value}
		case last:
			node[name] = value
		case list:
			items, _ := node[name].([]any)
			if len(items) == 0 {
				items = []any{map[string]any{}}
				node[name] = items
			}
			node = items[0].(map[string]any)
		default:
			child, ok := node[name].(map[string]any)
			if !ok {
				child = map[string]any{}
				node[name] = child
			}
			node = child
		}
	}
	return out
}

// TestLabelSelectorKinds_CoverEverySelector walks the type of every strict
// decode of the package for the label selectors it reaches and holds each to
// its kind: an expression without a key or without an operator is refused
// there, by path, and, where the kind checks expressions, so are an operator
// that is none of the four, In without a value and Exists with one. A selector
// no kind holds fails, unless its row excludes it with a reason; so does a
// dependency bump that adds one.
//
// The types are the ones strictlyDecodedTypes lists, which is held to the
// package's source first. What the walk does not seek is stated at
// labelSelectorKinds.
func TestLabelSelectorKinds_CoverEverySelector(t *testing.T) {
	sites := strictDecodeSites(t)
	if got, want := slices.Sorted(maps.Keys(sites)), slices.Sorted(maps.Keys(strictlyDecodedTypes)); !slices.Equal(got, want) {
		t.Fatalf("the package's strict decodes are of %v\nstrictlyDecodedTypes lists     %v", got, want)
	}
	rows := map[string][]labelSelectorKind{}
	components := map[string]bool{}
	for _, kind := range labelSelectorKinds {
		if _, ok := strictlyDecodedTypes[kind.typ]; !ok {
			t.Errorf("%s: %s is no key of strictlyDecodedTypes", kind.component, kind.typ)
		}
		if components[kind.component] {
			t.Errorf("%s has two rows of labelSelectorKinds", kind.component)
		}
		components[kind.component] = true
		rows[kind.typ] = append(rows[kind.typ], kind)
	}
	// Vacuity guard: the walk finds a selector behind a pointer, in a list and
	// in a struct that is no pointer.
	if got := labelSelectorPaths(reflect.TypeFor[networkingv1.NetworkPolicySpec]()); !slices.Equal(got, slices.Sorted(slices.Values(networkPolicyLabelSelectors))) {
		t.Fatalf("the walk finds %v in a NetworkPolicySpec, want %v; the reflection walk is broken", got, networkPolicyLabelSelectors)
	}
	// One row per kind: a kind added over a type another kind already decodes
	// needs its own row as well.
	for _, name := range slices.Sorted(maps.Keys(strictlyDecodedTypes)) {
		if paths := labelSelectorPaths(strictlyDecodedTypes[name]); len(paths) > 0 && len(rows[name]) != len(sites[name]) {
			t.Errorf("%s reaches the label selectors %v; %d files decode it (%v) and %d rows of labelSelectorKinds hold it", name, paths, len(sites[name]), sites[name], len(rows[name]))
		}
	}

	expression := func(fields map[string]any) map[string]any {
		return map[string]any{"matchExpressions": []any{fields}}
	}
	for _, kind := range labelSelectorKinds {
		t.Run(kind.component, func(t *testing.T) {
			paths := labelSelectorPaths(strictlyDecodedTypes[kind.typ])
			if len(paths) == 0 {
				t.Fatalf("the walk finds no label selector in %s; the row holds nothing", kind.typ)
			}
			for path, reason := range kind.excluded {
				if !slices.Contains(paths, path) {
					t.Errorf("%s is excluded and is no label selector of %s (%v)", path, kind.typ, paths)
				}
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s is excluded without a reason", path)
				}
			}
			// A kind that holds presence only says why key and operator are
			// required of its object: the CRDs of the linked module, which are
			// read here, or a stated ground.
			var schemas []crdSchema
			switch {
			case kind.config == nil:
			case kind.expressions && (kind.crds != nil || kind.ground != ""):
				t.Errorf("the row checks expressions as the API server's own validation does, and names a CRD or a ground beside it")
			case kind.expressions:
			case kind.crds != nil:
				schemas = kind.crds(t)
			case strings.TrimSpace(kind.ground) == "":
				t.Errorf("the row holds presence only and states neither a CRD nor a ground for it")
			}
			held := 0
			for _, path := range paths {
				if _, ok := kind.excluded[path]; ok {
					continue
				}
				for _, schema := range schemas {
					schema.show(t, kind, path)
				}
			}
			for _, path := range paths {
				if _, ok := kind.excluded[path]; ok {
					continue
				}
				held++
				at := strings.ReplaceAll(path, "[]", "[0]") + ".matchExpressions[0]"
				cases := map[string]struct {
					expr map[string]any
					want string
				}{
					"a whole expression": {map[string]any{"key": "tier", "operator": "In", "values": []any{"a"}}, ""},
					"no key":             {map[string]any{"operator": "In", "values": []any{"a"}}, at + ".key: required (the label key the expression applies to)"},
					"no operator":        {map[string]any{"key": "tier"}, at + ".operator: required (the operator of the expression: In, NotIn, Exists or DoesNotExist)"},
					// Without the expression check these three build: the row
					// states it, and a kind that starts to refuse one fails here.
					"an unknown operator": {map[string]any{"key": "tier", "operator": "Bogus"}, ""},
					"In without values":   {map[string]any{"key": "tier", "operator": "In"}, ""},
					"Exists with a value": {map[string]any{"key": "tier", "operator": "Exists", "values": []any{"a"}}, ""},
				}
				if kind.expressions {
					for name, want := range map[string]string{
						"an unknown operator": at + `.operator: "Bogus" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`,
						"In without values":   at + ".values: required with the operator In (at least one value)",
						"Exists with a value": at + ".values: not allowed with the operator Exists (it takes no value)",
					} {
						tc := cases[name]
						tc.want = want
						cases[name] = tc
					}
				}
				for name, tc := range cases {
					_, err := kind.config(&oam.Component{Name: "web", Type: kind.component, Properties: withValueAt(kind.base, path, expression(tc.expr))})
					switch {
					case tc.want == "" && err != nil:
						t.Errorf("%s, %s: err = %v, want none", path, name, err)
					case tc.want != "" && (err == nil || err.Error() != tc.want):
						t.Errorf("%s, %s: err = %v, want %q", path, name, err, tc.want)
					}
				}
			}
			if held == 0 {
				t.Logf("holds no selector: %v", kind.excluded)
			}
		})
	}
}

// TestReplicaSetSelector_RefusedAsAPIMachineryReadsIt is the ground of the one
// selector a kind that checks expressions leaves out of the two checks: a
// replicaset's `selector` is refused for each of the five defects all the
// same, under its path, by the comparison with the `app` label.
func TestReplicaSetSelector_RefusedAsAPIMachineryReadsIt(t *testing.T) {
	var replicaSet labelSelectorKind
	for _, kind := range labelSelectorKinds {
		if kind.component == "replicaset" {
			replicaSet = kind
		}
	}
	for name, expr := range map[string]map[string]any{
		"no key":              {"operator": "In", "values": []any{"a"}},
		"no operator":         {"key": "tier"},
		"an unknown operator": {"key": "tier", "operator": "Bogus"},
		"In without values":   {"key": "tier", "operator": "In"},
		"Exists with a value": {"key": "tier", "operator": "Exists", "values": []any{"a"}},
	} {
		props := withValueAt(replicaSet.base, "selector", map[string]any{"matchExpressions": []any{expr}})
		if _, err := replicaSet.config(&oam.Component{Name: "web", Type: "replicaset", Properties: props}); err == nil || !strings.HasPrefix(err.Error(), "selector: ") {
			t.Errorf("%s: err = %v, want a refusal of selector", name, err)
		}
	}
}

// TestLabelSelectorKinds_GenerateRepeatsTheRefusal: a kind whose config is
// exported refuses at Generate what its typed spec can show, an expression the
// API server refuses on every object included.
func TestLabelSelectorKinds_GenerateRepeatsTheRefusal(t *testing.T) {
	bad := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "tier", Operator: "Bogus"}}}
	app := &stack.Application{Name: "web", Namespace: "default"}
	containers := []corev1.Container{{Name: "app", Image: "registry.example.com/app:1.0"}}
	for name, tc := range map[string]struct {
		config stack.ApplicationConfig
		want   string
	}{
		"networkpolicy": {
			&NetworkPolicyConfig{Name: "web", Spec: networkingv1.NetworkPolicySpec{Egress: []networkingv1.NetworkPolicyEgressRule{{}, {To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: bad}}}}}},
			`egress[1].to[0].namespaceSelector.matchExpressions[0].operator: "Bogus" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`,
		},
		"pod": {
			&PodConfig{Name: "web", Spec: corev1.PodSpec{Containers: containers, TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{LabelSelector: bad}}}},
			`topologySpreadConstraints[0].labelSelector.matchExpressions[0].operator: "Bogus" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`,
		},
		"podtemplate": {
			&PodTemplateConfig{Name: "web", Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: containers, TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{LabelSelector: bad}}}}},
			`template.spec.topologySpreadConstraints[0].labelSelector.matchExpressions[0].operator: "Bogus" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`,
		},
		"replicaset": {
			&ReplicaSetConfig{Name: "web", Spec: appsv1.ReplicaSetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: containers, TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{LabelSelector: bad}}}}}},
			`template.spec.topologySpreadConstraints[0].labelSelector.matchExpressions[0].operator: "Bogus" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`,
		},
		"replicationcontroller": {
			&ReplicationControllerConfig{Name: "web", Spec: corev1.ReplicationControllerSpec{Template: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: containers, TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{LabelSelector: bad}}}}}},
			`template.spec.topologySpreadConstraints[0].labelSelector.matchExpressions[0].operator: "Bogus" is not a label selector operator (In, NotIn, Exists or DoesNotExist)`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.config.Generate(app); err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
