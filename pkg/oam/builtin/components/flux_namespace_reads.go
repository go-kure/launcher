package components

import "github.com/fluxcd/pkg/apis/meta"

// Every component config that moves to the Flux namespace (SetFluxNamespace)
// also reports the ConfigMaps and Secrets its Flux object reads by name from the
// namespace it lands in (FluxNamespaceReads, pkg/oam.fluxNamespaceReader). The
// transform moves a trait's ConfigMap or Secret the object reads along with it
// (go-kure/launcher#740); one it does not read stays in the application
// namespace. A config that reads none returns two empty lists, so a test can
// hold every Flux config to the contract.
//
// Each method lists every same-namespace ConfigMap or Secret reference of its
// spec type. A ServiceAccount (serviceAccountName) is read from that namespace
// too, but no trait emits one a Flux object could name, so it is not listed.

// fluxReads collects the names one FluxNamespaceReads reports.
type fluxReads struct {
	configMaps, secrets []string
}

func (r *fluxReads) configMap(name string) {
	if name != "" {
		r.configMaps = append(r.configMaps, name)
	}
}

func (r *fluxReads) secret(name string) {
	if name != "" {
		r.secrets = append(r.secrets, name)
	}
}

func (r *fluxReads) secretRef(ref *meta.LocalObjectReference) {
	if ref != nil {
		r.secret(ref.Name)
	}
}

// FluxNamespaceReads reports the HelmRelease's valuesFrom ConfigMaps and
// Secrets, its kubeConfig ConfigMap or Secret, and the chart template's
// verification Secret when the HelmChart helm-controller creates lands in the
// HelmRelease's namespace (sourceRef names no namespace). The values ConfigMap
// of valuesMode: configMap is this config's own object and moves with it.
func (c *HelmReleaseConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	var r fluxReads
	for _, v := range c.Spec.ValuesFrom {
		switch v.Kind {
		case "ConfigMap":
			r.configMap(v.Name)
		case "Secret":
			r.secret(v.Name)
		}
	}
	if k := c.Spec.KubeConfig; k != nil {
		if k.ConfigMapRef != nil {
			r.configMap(k.ConfigMapRef.Name)
		}
		if k.SecretRef != nil {
			r.secret(k.SecretRef.Name)
		}
	}
	if ch := c.Spec.Chart; ch != nil && ch.Spec.SourceRef.Namespace == "" && ch.Spec.Verify != nil {
		r.secretRef(ch.Spec.Verify.SecretRef)
	}
	return r.configMaps, r.secrets
}

// FluxNamespaceReads reports the HelmRepository's secretRef and certSecretRef.
func (c *HelmRepositoryConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	var r fluxReads
	r.secretRef(c.Spec.SecretRef)
	r.secretRef(c.Spec.CertSecretRef)
	return r.configMaps, r.secrets
}

// FluxNamespaceReads reports the OCIRepository's secretRef, certSecretRef,
// proxySecretRef, and its verification secretRef and trustedRootSecretRef.
func (c *OCIRepositoryConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	var r fluxReads
	r.secretRef(c.Spec.SecretRef)
	r.secretRef(c.Spec.CertSecretRef)
	r.secretRef(c.Spec.ProxySecretRef)
	if v := c.Spec.Verify; v != nil {
		r.secretRef(v.SecretRef)
		r.secretRef(v.TrustedRootSecretRef)
	}
	return r.configMaps, r.secrets
}

// FluxNamespaceReads reports the GitRepository's secretRef, proxySecretRef and
// verification secretRef.
func (c *GitRepositoryConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	var r fluxReads
	r.secretRef(c.Spec.SecretRef)
	r.secretRef(c.Spec.ProxySecretRef)
	if v := c.Spec.Verification; v != nil {
		r.secret(v.SecretRef.Name)
	}
	return r.configMaps, r.secrets
}

// FluxNamespaceReads reports the Bucket's secretRef, certSecretRef,
// proxySecretRef, and its STS secretRef and certSecretRef.
func (c *BucketConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	var r fluxReads
	r.secretRef(c.Spec.SecretRef)
	r.secretRef(c.Spec.CertSecretRef)
	r.secretRef(c.Spec.ProxySecretRef)
	if s := c.Spec.STS; s != nil {
		r.secretRef(s.SecretRef)
		r.secretRef(s.CertSecretRef)
	}
	return r.configMaps, r.secrets
}

// FluxNamespaceReads reports the HelmChart's verification secretRef.
func (c *HelmChartConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	var r fluxReads
	if v := c.Spec.Verify; v != nil {
		r.secretRef(v.SecretRef)
	}
	return r.configMaps, r.secrets
}

// FluxNamespaceReads reports nothing: the oci component's OCIRepository and
// Kustomization name no ConfigMap or Secret.
func (c *OCIConfig) FluxNamespaceReads() (configMaps, secrets []string) { return nil, nil }
