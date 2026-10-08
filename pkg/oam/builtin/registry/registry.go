// Package registry exports every built-in handler and lowering rule launcher
// implements, keyed by the type each one registers under, so that a consumer
// registers the builtins from these functions instead of a list of its own.
//
// Each function returns a new map on every call: a caller may delete, replace or
// add entries, and the change reaches no other caller. A type launcher adds later
// appears in the map of its kind without a change on the caller's side.
//
// A type that is a lowering rule is never also a handler of the same position:
// RegisterComponentLowering and RegisterTraitLowering (which
// RegisterBuiltinTraitLowering calls) panic on that collision. A consumer that
// replaces a handler sets its own under the same key; one that replaces a
// lowered type with a handler deletes the type from the lowering-rule map.
//
// Registering all of them builds the transformer kurel build uses:
//
//	t := oam.NewTransformer(registry.ComponentHandlers(), nil)
//	for _, r := range registry.ComponentLoweringRules() {
//		t.RegisterComponentLowering(r)
//	}
//	for name, h := range registry.TraitHandlers() {
//		t.RegisterBuiltinTrait(name, h)
//	}
//	for name, h := range registry.PolicyHandlers() {
//		t.RegisterPolicy(name, h)
//	}
//	for _, r := range registry.TraitLoweringRules() {
//		t.RegisterBuiltinTraitLowering(r)
//	}
package registry

import (
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// ComponentHandlers returns the built-in component handlers keyed by type, for
// oam.NewTransformer or RegisterComponent.
//
// The entries stand in the order of their type, as sort.Strings gives it; a
// new type goes at its position (TestKindLists_InOrder in pkg/cmd/kurel).
//
// Each call returns a new map: a caller may drop or replace entries without
// affecting another caller, and a component type launcher adds later appears
// here without a change on the caller's side.
func ComponentHandlers() map[string]oam.ComponentHandler {
	return map[string]oam.ComponentHandler{
		"alertmanager":                    &components.AlertmanagerHandler{},
		"apiservice":                      &components.APIServiceHandler{},
		"artifactgenerator":               &components.ArtifactGeneratorHandler{},
		"backendtlspolicy":                &components.BackendTLSPolicyHandler{},
		"bucket":                          &components.BucketHandler{},
		"certificate":                     &components.CertificateHandler{},
		"cilium-bgpadvertisement":         &components.CiliumBGPAdvertisementHandler{},
		"cilium-bgpclusterconfig":         &components.CiliumBGPClusterConfigHandler{},
		"cilium-bgpnodeconfigoverride":    &components.CiliumBGPNodeConfigOverrideHandler{},
		"cilium-bgppeerconfig":            &components.CiliumBGPPeerConfigHandler{},
		"cilium-cidrgroup":                &components.CiliumCIDRGroupHandler{},
		"cilium-clusterwidenetworkpolicy": &components.CiliumClusterwideNetworkPolicyHandler{},
		"cilium-egressgatewaypolicy":      &components.CiliumEgressGatewayPolicyHandler{},
		"cilium-loadbalancerippool":       &components.CiliumLoadBalancerIPPoolHandler{},
		"cilium-localredirectpolicy":      &components.CiliumLocalRedirectPolicyHandler{},
		"cilium-networkpolicy":            &components.CiliumNetworkPolicyHandler{},
		"cilium-nodeconfig":               &components.CiliumNodeConfigHandler{},
		"clusterexternalsecret":           &components.ClusterExternalSecretHandler{},
		"clusterissuer":                   &components.ClusterIssuerHandler{},
		"clusterrole":                     &components.ClusterRoleHandler{},
		"clusterrolebinding":              &components.ClusterRoleBindingHandler{},
		"clustersecretstore":              &components.ClusterSecretStoreHandler{},
		"cnpg-backup":                     &components.CnpgBackupHandler{},
		"cnpg-cluster":                    &components.CnpgClusterHandler{},
		"cnpg-clusterimagecatalog":        &components.CnpgClusterImageCatalogHandler{},
		"cnpg-database":                   &components.CnpgDatabaseHandler{},
		"cnpg-databaserole":               &components.CnpgDatabaseRoleHandler{},
		"cnpg-imagecatalog":               &components.CnpgImageCatalogHandler{},
		"cnpg-objectstore":                &components.CnpgObjectStoreHandler{},
		"cnpg-pooler":                     &components.CnpgPoolerHandler{},
		"cnpg-publication":                &components.CnpgPublicationHandler{},
		"cnpg-scheduledbackup":            &components.CnpgScheduledBackupHandler{},
		"cnpg-subscription":               &components.CnpgSubscriptionHandler{},
		"configmap":                       &components.ConfigMapHandler{},
		"crd":                             &components.CRDHandler{},
		"cronjob":                         &components.CronjobHandler{},
		"csidriver":                       &components.CSIDriverHandler{},
		"daemonset":                       &components.DaemonsetHandler{},
		"deployment":                      &components.DeploymentHandler{},
		"endpointslice":                   &components.EndpointSliceHandler{},
		"externalsecret":                  &components.ExternalSecretHandler{},
		"fluxcd-alert":                    &components.FluxcdAlertHandler{},
		"fluxcd-kustomization":            &components.FluxcdKustomizationHandler{},
		"fluxcd-provider":                 &components.FluxcdProviderHandler{},
		"fluxcd-receiver":                 &components.FluxcdReceiverHandler{},
		"gateway":                         &components.GatewayHandler{},
		"gatewayclass":                    &components.GatewayClassHandler{},
		"gitrepository":                   &components.GitRepositoryHandler{},
		"grpcroute":                       &components.GRPCRouteHandler{},
		"helmchart":                       &components.HelmChartHandler{},
		"helmrelease":                     &components.HelmReleaseHandler{},
		"helmrepository":                  &components.HelmRepositoryHandler{},
		"helmtemplate":                    &components.HelmTemplateHandler{},
		"horizontalpodautoscaler":         &components.HorizontalPodAutoscalerHandler{},
		"httproute":                       &components.HTTPRouteHandler{},
		"imagepolicy":                     &components.ImagePolicyHandler{},
		"imagerepository":                 &components.ImageRepositoryHandler{},
		"imageupdateautomation":           &components.ImageUpdateAutomationHandler{},
		"ingress":                         &components.IngressHandler{},
		"ingressclass":                    &components.IngressClassHandler{},
		"issuer":                          &components.IssuerHandler{},
		"job":                             &components.JobHandler{},
		"limitrange":                      &components.LimitRangeHandler{},
		"listenerset":                     &components.ListenerSetHandler{},
		"manifests":                       &components.ManifestsHandler{},
		"metallb-bfdprofile":              &components.MetalLBBFDProfileHandler{},
		"metallb-bgpadvertisement":        &components.MetalLBBGPAdvertisementHandler{},
		"metallb-bgppeer":                 &components.MetalLBBGPPeerHandler{},
		"metallb-community":               &components.MetalLBCommunityHandler{},
		"metallb-ipaddresspool":           &components.MetalLBIPAddressPoolHandler{},
		"metallb-l2advertisement":         &components.MetalLBL2AdvertisementHandler{},
		"mutatingwebhookconfiguration":    &components.MutatingWebhookConfigurationHandler{},
		"namespace":                       &components.NamespaceHandler{},
		"networkpolicy":                   &components.NetworkPolicyHandler{},
		"ocirepository":                   &components.OCIRepositoryHandler{},
		"passthrough":                     &components.PassthroughHandler{},
		"persistentvolume":                &components.PersistentVolumeHandler{},
		"persistentvolumeclaim":           &components.PersistentVolumeClaimHandler{},
		"pod":                             &components.PodHandler{},
		"poddisruptionbudget":             &components.PodDisruptionBudgetHandler{},
		"podmonitor":                      &components.PodMonitorHandler{},
		"podtemplate":                     &components.PodTemplateHandler{},
		"priorityclass":                   &components.PriorityClassHandler{},
		"prometheus-probe":                &components.PrometheusProbeHandler{},
		"prometheusrule":                  &components.PrometheusRuleHandler{},
		"referencegrant":                  &components.ReferenceGrantHandler{},
		"replicaset":                      &components.ReplicaSetHandler{},
		"replicationcontroller":           &components.ReplicationControllerHandler{},
		"replicationdestination":          &components.ReplicationDestinationHandler{},
		"replicationsource":               &components.ReplicationSourceHandler{},
		"resourcequota":                   &components.ResourceQuotaHandler{},
		"resourcesetinputprovider":        &components.ResourceSetInputProviderHandler{},
		"role":                            &components.RoleHandler{},
		"rolebinding":                     &components.RoleBindingHandler{},
		"runtimeclass":                    &components.RuntimeClassHandler{},
		"secret":                          &components.SecretHandler{},
		"secretstore":                     &components.SecretStoreHandler{},
		"service":                         &components.ServiceHandler{},
		"serviceaccount":                  &components.ServiceAccountHandler{},
		"servicecidr":                     &components.ServiceCIDRHandler{},
		"servicemonitor":                  &components.ServiceMonitorHandler{},
		"statefulset":                     &components.StatefulsetHandler{},
		"storageclass":                    &components.StorageClassHandler{},
		"tcproute":                        &components.TCPRouteHandler{},
		"tlsroute":                        &components.TLSRouteHandler{},
		"udproute":                        &components.UDPRouteHandler{},
		"validatingwebhookconfiguration":  &components.ValidatingWebhookConfigurationHandler{},
		"volumeattributesclass":           &components.VolumeAttributesClassHandler{},
	}
}

// ComponentLoweringRules returns the built-in component-position lowering rules
// (oam.ComponentLoweringRule) keyed by the component type they claim, for
// RegisterComponentLowering. No type here is also a key of ComponentHandlers.
//
// Each call returns a new map: a caller may drop or replace entries without
// affecting another caller, and a rule launcher adds later appears here
// without a change on the caller's side.
func ComponentLoweringRules() map[string]oam.ComponentLoweringRule {
	return map[string]oam.ComponentLoweringRule{
		"webservice": components.WebserviceRule{},
		"worker":     components.WorkerRule{},
		"helm":       components.HelmRule{},
		"postgresql": components.PostgresqlRule{},
		"oci":        components.OCIRule{},
	}
}

// TraitHandlers returns the built-in trait handlers keyed by type, for
// RegisterBuiltinTrait.
//
// Each call returns a new map: a caller may drop or replace entries without
// affecting another caller, and a trait type launcher adds later appears here
// without a change on the caller's side.
func TraitHandlers() map[string]oam.TraitHandler {
	return map[string]oam.TraitHandler{
		"ingress":              &traits.IngressHandler{},
		"httproute":            &traits.HTTPRouteHandler{},
		"certificate":          &traits.CertificateHandler{},
		"scaler":               &traits.ScalerHandler{},
		"pvc":                  &traits.PVCHandler{},
		"external-secret":      &traits.ExternalSecretHandler{},
		"configmap":            &traits.ConfigMapHandler{},
		"secret":               &traits.SecretHandler{},
		"networkpolicy":        &traits.NetworkPolicyHandler{},
		"cilium-networkpolicy": &traits.CiliumNetworkPolicyHandler{},
		"volsync":              &traits.VolSyncHandler{},
		"rbac":                 &traits.RBACHandler{},
		"prune-protection":     &traits.PruneProtectionHandler{},
		"force-replace":        &traits.ForceReplaceHandler{},
		"security-context":     &traits.SecurityContextHandler{},
		"topology-spread":      &traits.TopologySpreadHandler{},
	}
}

// TraitLoweringRules returns the built-in trait-position lowering rules
// (oam.TraitLoweringRule) keyed by the trait type they claim, for
// RegisterBuiltinTraitLowering. No type here is also a key of TraitHandlers.
//
// Each call returns a new map: a caller may drop or replace entries without
// affecting another caller, and a rule launcher adds later appears here
// without a change on the caller's side.
func TraitLoweringRules() map[string]oam.TraitLoweringRule {
	return map[string]oam.TraitLoweringRule{
		"expose": traits.ExposeRule{},
	}
}

// PolicyHandlers returns the built-in application policy handlers keyed by
// policy type, for RegisterPolicy.
//
// launcher has no handler for app-dependency, nor for the delivery policies
// reconciliation and health-checks (or the delivery traits fluxcd-patches and
// fluxcd-postbuild): a consumer that implements them registers its own.
//
// Each call returns a new map: a caller may drop or replace entries without
// affecting another caller, and a policy type launcher adds later appears here
// without a change on the caller's side.
func PolicyHandlers() map[string]oam.PolicyHandler {
	return map[string]oam.PolicyHandler{
		"dependency": &policies.DependencyHandler{},
		"placement":  &policies.PlacementHandler{},
	}
}
