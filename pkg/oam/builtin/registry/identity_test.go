package registry_test

import (
	"maps"
	"slices"
	"testing"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	fluxoperatorv1 "github.com/controlplaneio-fluxcd/flux-operator/api/v1"
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	autov1 "github.com/fluxcd/image-automation-controller/api/v1"
	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	swv1beta1 "github.com/fluxcd/source-watcher/api/v2/v1beta1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/registry"
)

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

// union returns the sorted, de-duplicated keys of several key lists; the
// handler and lowering-rule maps of a position have different value types.
func union(lists ...[]string) []string {
	all := map[string]struct{}{}
	for _, l := range lists {
		for _, k := range l {
			all[k] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(all))
}

// The identity maps hold exactly the types the registry maps hold: a type
// launcher adds without a declaration, or a declaration of no type, fails here.
func TestTypeIDs_CoverEveryRegisteredType(t *testing.T) {
	for position, tc := range map[string]struct {
		want []string
		got  map[string]oam.TypeID
	}{
		"component": {union(sortedKeys(registry.ComponentHandlers()), sortedKeys(registry.ComponentLoweringRules())), registry.ComponentTypeIDs()},
		"trait":     {union(sortedKeys(registry.TraitHandlers()), sortedKeys(registry.TraitLoweringRules())), registry.TraitTypeIDs()},
		"policy":    {sortedKeys(registry.PolicyHandlers()), registry.PolicyTypeIDs()},
	} {
		if len(tc.want) == 0 {
			t.Fatalf("%s: the registry holds no type", position)
		}
		if !slices.Equal(tc.want, sortedKeys(tc.got)) {
			t.Errorf("%s: declared types %v, registered types %v", position, sortedKeys(tc.got), tc.want)
		}
	}
}

// Every declaration is a valid identity, the set of a position registers
// without a conflict, and every plain name a built-in registers under still
// resolves to its own identity: the legacy short name is the unique alias.
func TestTypeIDs_RegisterAndResolveEveryLegacyName(t *testing.T) {
	for position, ids := range map[string]map[string]oam.TypeID{
		"component": registry.ComponentTypeIDs(),
		"trait":     registry.TraitTypeIDs(),
		"policy":    registry.PolicyTypeIDs(),
	} {
		reg := oam.NewTypeRegistry()
		for _, key := range sortedKeys(ids) {
			id := ids[key]
			if _, err := oam.ValidateTypeIdentity(id.String()); err != nil {
				t.Errorf("%s %q: %v", position, key, err)
				continue
			}
			if got := id.ShortName(); got != key {
				t.Errorf("%s %q: identity %s claims the short name %q", position, key, id, got)
			}
			if err := reg.Register(id); err != nil {
				t.Errorf("%s %q: %v", position, key, err)
			}
		}
		for _, key := range sortedKeys(ids) {
			got, err := reg.Resolve(key)
			if err != nil {
				t.Errorf("%s %q does not resolve: %v", position, key, err)
				continue
			}
			if got != ids[key] {
				t.Errorf("%s %q resolves to %s, declared %s", position, key, got, ids[key])
			}
			if _, err := reg.Resolve(ids[key].String()); err != nil {
				t.Errorf("%s %s does not resolve by its own identity: %v", position, ids[key], err)
			}
		}
	}
}

// A kind component is the kind's identity and every other built-in is a
// launcher type; the two forms are told apart by the domain alone.
func TestTypeIDs_FormsByType(t *testing.T) {
	ids := registry.ComponentTypeIDs()
	for key, want := range map[string]string{
		"deployment":      "deployment.apps@v1",
		"service":         "service@v1",
		"configmap":       "configmap@v1",
		"tlsroute":        "tlsroute.gateway.networking.k8s.io@v1",
		"certificate":     "certificate.cert-manager.io@v1",
		"metallb-bgppeer": "metallb-bgppeer.launcher.gokure.dev@v1",
		"cnpg-cluster":    "cnpg-cluster.launcher.gokure.dev@v1",
		"webservice":      "webservice.launcher.gokure.dev@v1",
		"helm":            "helm.launcher.gokure.dev@v1",
	} {
		if got := ids[key].String(); got != want {
			t.Errorf("component %q = %s, want %s", key, got, want)
		}
	}
	if got := registry.TraitTypeIDs()["certificate"].String(); got != "certificate.launcher.gokure.dev@v1" {
		t.Errorf("trait certificate = %s", got)
	}
}

// The identity a kind component declares carries the group and version of the Go
// API package its handler decodes into and emits. A package that moves to
// another version fails here, naming the type.
func TestTypeIDs_MatchTheLinkedAPIPackages(t *testing.T) {
	ids := registry.ComponentTypeIDs()
	for typ, gv := range map[string][2]string{
		"service":                        {corev1.SchemeGroupVersion.Group, corev1.SchemeGroupVersion.Version},
		"deployment":                     {appsv1.SchemeGroupVersion.Group, appsv1.SchemeGroupVersion.Version},
		"job":                            {batchv1.SchemeGroupVersion.Group, batchv1.SchemeGroupVersion.Version},
		"horizontalpodautoscaler":        {autoscalingv2.SchemeGroupVersion.Group, autoscalingv2.SchemeGroupVersion.Version},
		"poddisruptionbudget":            {policyv1.SchemeGroupVersion.Group, policyv1.SchemeGroupVersion.Version},
		"ingress":                        {networkingv1.SchemeGroupVersion.Group, networkingv1.SchemeGroupVersion.Version},
		"storageclass":                   {storagev1.SchemeGroupVersion.Group, storagev1.SchemeGroupVersion.Version},
		"priorityclass":                  {schedulingv1.SchemeGroupVersion.Group, schedulingv1.SchemeGroupVersion.Version},
		"runtimeclass":                   {nodev1.SchemeGroupVersion.Group, nodev1.SchemeGroupVersion.Version},
		"endpointslice":                  {discoveryv1.SchemeGroupVersion.Group, discoveryv1.SchemeGroupVersion.Version},
		"role":                           {rbacv1.SchemeGroupVersion.Group, rbacv1.SchemeGroupVersion.Version},
		"validatingwebhookconfiguration": {admissionregistrationv1.SchemeGroupVersion.Group, admissionregistrationv1.SchemeGroupVersion.Version},
		"apiservice":                     {apiregistrationv1.SchemeGroupVersion.Group, apiregistrationv1.SchemeGroupVersion.Version},
		"gateway":                        {gatewayv1.GroupVersion.Group, gatewayv1.GroupVersion.Version},
		"issuer":                         {certv1.SchemeGroupVersion.Group, certv1.SchemeGroupVersion.Version},
		"externalsecret":                 {esv1.SchemeGroupVersion.Group, esv1.SchemeGroupVersion.Version},
		"prometheus":                     {monitoringv1.SchemeGroupVersion.Group, monitoringv1.SchemeGroupVersion.Version},
		"replicationsource":              {volsyncv1alpha1.GroupVersion.Group, volsyncv1alpha1.GroupVersion.Version},
		"gitrepository":                  {sourcev1.GroupVersion.Group, sourcev1.GroupVersion.Version},
		"helmrelease":                    {helmv2.GroupVersion.Group, helmv2.GroupVersion.Version},
		"imagerepository":                {imagev1.GroupVersion.Group, imagev1.GroupVersion.Version},
		"imageupdateautomation":          {autov1.GroupVersion.Group, autov1.GroupVersion.Version},
		"artifactgenerator":              {swv1beta1.GroupVersion.Group, swv1beta1.GroupVersion.Version},
		"resourcesetinputprovider":       {fluxoperatorv1.GroupVersion.Group, fluxoperatorv1.GroupVersion.Version},
	} {
		id, ok := ids[typ]
		if !ok {
			t.Errorf("%s is not a built-in component type", typ)
			continue
		}
		want := typ + "." + gv[0] + "@" + gv[1]
		if gv[0] == "" {
			want = typ + "@" + gv[1]
		}
		if got := id.String(); got != want {
			t.Errorf("%s declares %s, the linked API package is %s", typ, got, want)
		}
	}
}
