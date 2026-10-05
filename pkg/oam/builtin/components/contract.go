package components

import (
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// This file holds the contract metadata of every built-in component handler and
// lowering rule (oam.ContractDescriber), and the types each rule lowers into
// (oam.LoweringTargetDeclarer). The scheme is builtin.ContractVersion's: the
// family is the type name. No component requires a ClusterProfile capability.

// contract is the metadata of the built-in component type typ.
func contract(typ string) oam.ContractMetadata {
	return oam.ContractMetadata{Family: typ, Version: builtin.ContractVersion}
}

// ContractMetadata implements oam.ContractDescriber.
func (h *DeploymentHandler) ContractMetadata() oam.ContractMetadata { return contract("deployment") }

// ContractMetadata implements oam.ContractDescriber.
func (h *CronjobHandler) ContractMetadata() oam.ContractMetadata { return contract("cronjob") }

// ContractMetadata implements oam.ContractDescriber.
func (h *JobHandler) ContractMetadata() oam.ContractMetadata { return contract("job") }

// ContractMetadata implements oam.ContractDescriber.
func (h *DaemonsetHandler) ContractMetadata() oam.ContractMetadata { return contract("daemonset") }

// ContractMetadata implements oam.ContractDescriber.
func (h *StatefulsetHandler) ContractMetadata() oam.ContractMetadata { return contract("statefulset") }

// ContractMetadata implements oam.ContractDescriber.
func (h *ServiceHandler) ContractMetadata() oam.ContractMetadata { return contract("service") }

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgClusterHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-cluster")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgPoolerHandler) ContractMetadata() oam.ContractMetadata { return contract("cnpg-pooler") }

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgDatabaseHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-database")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgObjectStoreHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-objectstore")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *HelmReleaseHandler) ContractMetadata() oam.ContractMetadata { return contract("helmrelease") }

// ContractMetadata implements oam.ContractDescriber.
func (h *HelmTemplateHandler) ContractMetadata() oam.ContractMetadata {
	return contract(helmTemplateType)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *PassthroughHandler) ContractMetadata() oam.ContractMetadata { return contract("passthrough") }

// ContractMetadata implements oam.ContractDescriber.
func (h *CRDHandler) ContractMetadata() oam.ContractMetadata { return contract("crd") }

// ContractMetadata implements oam.ContractDescriber.
func (h *ManifestsHandler) ContractMetadata() oam.ContractMetadata { return contract("manifests") }

// ContractMetadata implements oam.ContractDescriber.
func (h *FluxcdKustomizationHandler) ContractMetadata() oam.ContractMetadata {
	return contract(fluxcdKustomizationType)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *HelmRepositoryHandler) ContractMetadata() oam.ContractMetadata {
	return contract("helmrepository")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *OCIRepositoryHandler) ContractMetadata() oam.ContractMetadata {
	return contract("ocirepository")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *GitRepositoryHandler) ContractMetadata() oam.ContractMetadata {
	return contract("gitrepository")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *BucketHandler) ContractMetadata() oam.ContractMetadata { return contract("bucket") }

// ContractMetadata implements oam.ContractDescriber.
func (h *HelmChartHandler) ContractMetadata() oam.ContractMetadata { return contract("helmchart") }

// ContractMetadata implements oam.ContractDescriber.
func (h *ServiceAccountHandler) ContractMetadata() oam.ContractMetadata {
	return contract("serviceaccount")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *PersistentVolumeClaimHandler) ContractMetadata() oam.ContractMetadata {
	return contract("persistentvolumeclaim")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *ConfigMapHandler) ContractMetadata() oam.ContractMetadata { return contract("configmap") }

// ContractMetadata implements oam.ContractDescriber.
func (h *NamespaceHandler) ContractMetadata() oam.ContractMetadata { return contract("namespace") }

// ContractMetadata implements oam.ContractDescriber.
func (h *LimitRangeHandler) ContractMetadata() oam.ContractMetadata { return contract("limitrange") }

// ContractMetadata implements oam.ContractDescriber.
func (h *ResourceQuotaHandler) ContractMetadata() oam.ContractMetadata {
	return contract("resourcequota")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *PersistentVolumeHandler) ContractMetadata() oam.ContractMetadata {
	return contract("persistentvolume")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *PodHandler) ContractMetadata() oam.ContractMetadata { return contract("pod") }

// ContractMetadata implements oam.ContractDescriber.
func (h *ReplicaSetHandler) ContractMetadata() oam.ContractMetadata { return contract("replicaset") }

// ContractMetadata implements oam.ContractDescriber.
func (h *ReplicationControllerHandler) ContractMetadata() oam.ContractMetadata {
	return contract("replicationcontroller")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *PodTemplateHandler) ContractMetadata() oam.ContractMetadata { return contract("podtemplate") }

// ContractMetadata implements oam.ContractDescriber.
func (h *StorageClassHandler) ContractMetadata() oam.ContractMetadata {
	return contract("storageclass")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *VolumeAttributesClassHandler) ContractMetadata() oam.ContractMetadata {
	return contract("volumeattributesclass")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *PriorityClassHandler) ContractMetadata() oam.ContractMetadata {
	return contract("priorityclass")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *RuntimeClassHandler) ContractMetadata() oam.ContractMetadata {
	return contract("runtimeclass")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *IngressClassHandler) ContractMetadata() oam.ContractMetadata {
	return contract("ingressclass")
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CSIDriverHandler) ContractMetadata() oam.ContractMetadata { return contract("csidriver") }

// ContractMetadata implements oam.ContractDescriber.
func (h *IngressHandler) ContractMetadata() oam.ContractMetadata { return contract("ingress") }

// ContractMetadata implements oam.ContractDescriber.
func (h *HTTPRouteHandler) ContractMetadata() oam.ContractMetadata { return contract("httproute") }

// ContractMetadata implements oam.ContractDescriber.
func (WebserviceRule) ContractMetadata() oam.ContractMetadata { return contract("webservice") }

// LoweringTargets implements oam.LoweringTargetDeclarer: the members under the
// component's name (a "deployment", a "service" and the component's own
// "serviceaccount"), the spread the rule adds to the "deployment" when the
// author set none, and the "pvc" trait of each claim a volume generates.
func (WebserviceRule) LoweringTargets() oam.LoweringTargets {
	return oam.LoweringTargets{
		ComponentTypes: []string{"deployment", "service", "serviceaccount"},
		TraitTypes:     []string{"topology-spread", "pvc"},
	}
}

// ContractMetadata implements oam.ContractDescriber.
func (WorkerRule) ContractMetadata() oam.ContractMetadata { return contract("worker") }

// LoweringTargets implements oam.LoweringTargetDeclarer: the members under the
// component's name (a "deployment" and the component's own "serviceaccount"),
// the spread the rule adds to the "deployment" when the author set none, and
// the "pvc" trait of each claim a volume generates.
func (WorkerRule) LoweringTargets() oam.LoweringTargets {
	return oam.LoweringTargets{
		ComponentTypes: []string{"deployment", "serviceaccount"},
		TraitTypes:     []string{"topology-spread", "pvc"},
	}
}

// ContractMetadata implements oam.ContractDescriber.
func (HelmRule) ContractMetadata() oam.ContractMetadata { return contract(helmType) }

// LoweringTargets implements oam.LoweringTargetDeclarer: the release
// ("helmrelease") or the rendered chart ("helmtemplate"), the Flux source the
// rule generates for an inline chart location, the "configmap" trait that
// carries a release's values and the "secret" trait that carries its
// secretValues.
func (HelmRule) LoweringTargets() oam.LoweringTargets {
	return oam.LoweringTargets{
		ComponentTypes: []string{
			"helmrelease", helmTemplateType,
			"helmrepository", "ocirepository", "gitrepository", "bucket",
		},
		TraitTypes: []string{"configmap", "secret"},
	}
}

// ContractMetadata implements oam.ContractDescriber.
func (OCIRule) ContractMetadata() oam.ContractMetadata { return contract(ociType) }

// LoweringTargets implements oam.LoweringTargetDeclarer: the source of the
// artifact ("ocirepository") and the Kustomization that reconciles it.
func (OCIRule) LoweringTargets() oam.LoweringTargets {
	return oam.LoweringTargets{
		ComponentTypes: []string{"ocirepository", fluxcdKustomizationType},
	}
}

// ContractMetadata implements oam.ContractDescriber.
func (PostgresqlRule) ContractMetadata() oam.ContractMetadata { return contract("postgresql") }

// LoweringTargets implements oam.LoweringTargetDeclarer: the CloudNativePG kinds,
// and the policies that order and place what the rule split the component into.
func (PostgresqlRule) LoweringTargets() oam.LoweringTargets {
	return oam.LoweringTargets{
		ComponentTypes: []string{"cnpg-cluster", "cnpg-objectstore", "cnpg-pooler", "cnpg-database"},
		PolicyTypes:    []string{"dependency", "placement"},
	}
}
