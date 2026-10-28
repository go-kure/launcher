package components

import (
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
)

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
// Secrets, its kubeConfig ConfigMap or Secret, and the Secrets of the object
// helm-controller generates from its chart template, when that lands in the
// HelmRelease's namespace. For kind HelmChart that is the verification Secret,
// when sourceRef names no namespace or names that one. For kind OCIRepository,
// which always lands there, it is every Secret the OCIRepository component
// reports: secretRef, certSecretRef, proxySecretRef, and the verification's
// secretRef and trustedRootSecretRef. The values ConfigMap of valuesMode:
// configMap is this config's own object and moves with it.
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
	switch ch := c.Spec.Chart; {
	case ch == nil:
	case ch.Kind == sourcev1.OCIRepositoryKind:
		r.secretRef(ch.Spec.SecretRef)
		r.secretRef(ch.Spec.CertSecretRef)
		r.secretRef(ch.Spec.ProxySecretRef)
		if v := ch.Spec.Verify; v != nil {
			r.secretRef(v.SecretRef)
			r.secretRef(v.TrustedRootSecretRef)
		}
	case helmChartTemplateNamespace(ch, c.fluxNamespace()) == c.fluxNamespace() && ch.Spec.Verify != nil:
		r.secretRef(ch.Spec.Verify.SecretRef)
	}
	return r.configMaps, r.secrets
}

// helmChartTemplateNamespace returns the namespace helm-controller generates
// the object of a chart template in, for a HelmRelease in hrNamespace: for kind
// HelmChart the namespace of its sourceRef when one is set, else hrNamespace
// (HelmRelease.GetHelmChartTemplateReference, helm-controller api v1.7.1
// v2/helmrelease_types.go:1633-1649). Kind OCIRepository always lands in
// hrNamespace.
func helmChartTemplateNamespace(ch *helmv2.HelmChartTemplate, hrNamespace string) string {
	if (ch.Kind == "" || ch.Kind == sourcev1.HelmChartKind) && ch.Spec.SourceRef != nil && ch.Spec.SourceRef.Namespace != "" {
		return ch.Spec.SourceRef.Namespace
	}
	return hrNamespace
}

// FluxNamespaceReads reports the Kustomization's decryption Secret, its
// kubeConfig ConfigMap or Secret, and the ConfigMaps and Secrets its
// postBuild.substituteFrom names.
func (c *FluxcdKustomizationConfig) FluxNamespaceReads() (configMaps, secrets []string) {
	var r fluxReads
	if d := c.Spec.Decryption; d != nil {
		r.secretRef(d.SecretRef)
	}
	if k := c.Spec.KubeConfig; k != nil {
		if k.ConfigMapRef != nil {
			r.configMap(k.ConfigMapRef.Name)
		}
		if k.SecretRef != nil {
			r.secret(k.SecretRef.Name)
		}
	}
	if pb := c.Spec.PostBuild; pb != nil {
		for _, from := range pb.SubstituteFrom {
			switch from.Kind {
			case "ConfigMap":
				r.configMap(from.Name)
			case "Secret":
				r.secret(from.Name)
			}
		}
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

// FluxSourceRequiredSecrets reports the Secrets the HelmRepository cannot
// become Ready without: its secretRef and certSecretRef. Satisfies
// pkg/oam.fluxSourceRequiredReader.
func (c *HelmRepositoryConfig) FluxSourceRequiredSecrets() []string {
	var r fluxReads
	r.secretRef(c.Spec.SecretRef)
	r.secretRef(c.Spec.CertSecretRef)
	return r.secrets
}

// FluxSourceRequiredSecrets reports the Secrets the OCIRepository cannot
// become Ready without: its secretRef, certSecretRef and proxySecretRef, and
// for a verification its secretRef, and its trustedRootSecretRef only under
// the cosign provider, the one source-controller reads it for. The provider is
// the one the source is generated with (effectiveFluxVerifyProvider). Unlike
// FluxNamespaceReads, which moves a trait Secret the source may read, this is
// what a delivery that waits on the source waits for (go-kure/launcher#1039).
// It is what a consumer of a generated source waits for; FluxRequiredReads is
// what the OCIRepository itself waits for, and differs from it under
// suspend. Satisfies pkg/oam.fluxSourceRequiredReader.
func (c *OCIRepositoryConfig) FluxSourceRequiredSecrets() []string {
	var r fluxReads
	ociRequiredSecrets(&c.Spec, &r)
	return r.secrets
}

// ociRequiredSecrets adds the Secrets an OCIRepository of spec cannot become
// Ready without (FluxSourceRequiredSecrets): the one rule for the
// ocirepository component and a HelmRelease's chart template of kind
// OCIRepository, which helm-controller generates from that spec.
func ociRequiredSecrets(spec *sourcev1.OCIRepositorySpec, r *fluxReads) {
	r.secretRef(spec.SecretRef)
	r.secretRef(spec.CertSecretRef)
	r.secretRef(spec.ProxySecretRef)
	if v := spec.Verify; v != nil {
		r.secretRef(v.SecretRef)
		if effectiveFluxVerifyProvider(v.Provider) == "cosign" {
			r.secretRef(v.TrustedRootSecretRef)
		}
	}
}

// Every Flux config also reports what its object itself cannot become Ready
// without (FluxRequiredReads, pkg/oam.fluxRequiredReader): the ConfigMaps and
// Secrets, in the namespace it lands in, whose absence keeps it not Ready. A
// delivery whose groups wait for their Flux objects
// (TransformContext.GroupsWaitForFluxObjects) refuses such an object a trait
// makes in a later group, which would never converge (go-kure/launcher#1079).
// It is a subset of FluxNamespaceReads. What a reference is read only under a
// condition launcher cannot settle is left out, so the check admits it:
//   - a suspended object (spec.suspend) is never reconciled, so it carries no
//     condition and a wait reads it Current at once: it requires nothing;
//   - a valuesFrom or substituteFrom reference marked optional;
//   - postBuild.substituteFrom at all: kustomize-controller loads it per
//     rendered resource and skips one that disables substitution
//     (kustomize.toolkit.fluxcd.io/substitute: disabled), which only the
//     artifact says;
//   - a HelmRepository of type oci, which source-controller makes a static
//     object that never sets Ready;
//   - a ResourceSetInputProvider (kind_flux.go), Alert and Provider.

// FluxRequiredReads reports the HelmRelease's valuesFrom ConfigMaps and
// Secrets not marked optional, its kubeConfig ConfigMap or Secret, and what
// the object generated from its chart template requires where it lands in the
// HelmRelease's namespace: for kind OCIRepository what the ocirepository
// component requires (ociRequiredSecrets), for kind HelmChart its verification
// Secret. None when suspended.
func (c *HelmReleaseConfig) FluxRequiredReads() (configMaps, secrets []string) {
	if c.Spec.Suspend {
		return nil, nil
	}
	var r fluxReads
	for _, v := range c.Spec.ValuesFrom {
		if v.Optional {
			continue
		}
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
	switch ch := c.Spec.Chart; {
	case ch == nil:
	case ch.Kind == sourcev1.OCIRepositoryKind:
		ociRequiredSecrets(&ch.Spec.OCIRepositorySpec, &r)
	case helmChartTemplateNamespace(ch, c.fluxNamespace()) == c.fluxNamespace() && ch.Spec.Verify != nil:
		r.secretRef(ch.Spec.Verify.SecretRef)
	}
	return r.configMaps, r.secrets
}

// FluxRequiredReads reports the Kustomization's decryption Secret and its
// kubeConfig ConfigMap or Secret. Not its postBuild.substituteFrom, read only
// for a rendered resource that does not disable substitution. None when
// suspended.
func (c *FluxcdKustomizationConfig) FluxRequiredReads() (configMaps, secrets []string) {
	if c.Spec.Suspend {
		return nil, nil
	}
	var r fluxReads
	if d := c.Spec.Decryption; d != nil {
		r.secretRef(d.SecretRef)
	}
	if k := c.Spec.KubeConfig; k != nil {
		if k.ConfigMapRef != nil {
			r.configMap(k.ConfigMapRef.Name)
		}
		if k.SecretRef != nil {
			r.secret(k.SecretRef.Name)
		}
	}
	return r.configMaps, r.secrets
}

// FluxRequiredReads reports the HelmRepository's secretRef and certSecretRef.
// None for type oci, which source-controller makes a static object that never
// sets Ready, so a wait never holds on it; an OCI chart pulled through it
// still reads them (FluxSourceRequiredSecrets). None when suspended.
func (c *HelmRepositoryConfig) FluxRequiredReads() (configMaps, secrets []string) {
	if c.Spec.Suspend || c.Spec.Type == sourcev1.HelmRepositoryTypeOCI {
		return nil, nil
	}
	return nil, c.FluxSourceRequiredSecrets()
}

// FluxRequiredReads reports what FluxSourceRequiredSecrets does, and none when
// suspended.
func (c *OCIRepositoryConfig) FluxRequiredReads() (configMaps, secrets []string) {
	if c.Spec.Suspend {
		return nil, nil
	}
	return nil, c.FluxSourceRequiredSecrets()
}

// FluxRequiredReads reports every Secret the GitRepository reads
// (FluxNamespaceReads), and none when suspended.
func (c *GitRepositoryConfig) FluxRequiredReads() (configMaps, secrets []string) {
	if c.Spec.Suspend {
		return nil, nil
	}
	return c.FluxNamespaceReads()
}

// FluxRequiredReads reports every Secret the Bucket reads
// (FluxNamespaceReads), and none when suspended.
func (c *BucketConfig) FluxRequiredReads() (configMaps, secrets []string) {
	if c.Spec.Suspend {
		return nil, nil
	}
	return c.FluxNamespaceReads()
}

// FluxRequiredReads reports the HelmChart's verification Secret
// (FluxNamespaceReads), and none when suspended.
func (c *HelmChartConfig) FluxRequiredReads() (configMaps, secrets []string) {
	if c.Spec.Suspend {
		return nil, nil
	}
	return c.FluxNamespaceReads()
}

// A Flux config whose object names the source it cannot become Ready without
// also reports it (FluxRequiredSource, pkg/oam.fluxRequiredSourceReader): the
// field that names it, its kind in source.toolkit.fluxcd.io, its name, and the
// namespace the reference writes, "" for the object's own. A delivery whose
// groups wait for their Flux objects refuses such a source a component makes
// in a later group, which would never converge (go-kure/launcher#1088). None
// when the object is suspended, as for FluxRequiredReads. The imageupdateautomation
// kind reports its sourceRef through fluxKind.requiredSource.

// FluxRequiredSource reports the HelmRelease's chartRef, or the sourceRef of
// its chart template of kind HelmChart: helm-controller generates the HelmChart
// in that reference's namespace, else the HelmRelease's, and the HelmChart
// reads its source from its own. None for a chart template of kind
// OCIRepository, which names no source, and when suspended.
func (c *HelmReleaseConfig) FluxRequiredSource() (field, kind, name, namespace string) {
	switch ch := c.Spec.Chart; {
	case c.Spec.Suspend:
	case c.Spec.ChartRef != nil:
		ref := c.Spec.ChartRef
		return "chartRef", ref.Kind, ref.Name, ref.Namespace
	case ch != nil && (ch.Kind == "" || ch.Kind == sourcev1.HelmChartKind) && ch.Spec.SourceRef != nil:
		ref := ch.Spec.SourceRef
		return "chart.spec.sourceRef", ref.Kind, ref.Name, ref.Namespace
	}
	return "", "", "", ""
}

// FluxRequiredSource reports the Kustomization's sourceRef, and none when
// suspended.
func (c *FluxcdKustomizationConfig) FluxRequiredSource() (field, kind, name, namespace string) {
	if c.Spec.Suspend {
		return "", "", "", ""
	}
	ref := c.Spec.SourceRef
	return "sourceRef", ref.Kind, ref.Name, ref.Namespace
}

// FluxRequiredSource reports the HelmChart's sourceRef, which names a source
// in the HelmChart's own namespace, and none when suspended.
func (c *HelmChartConfig) FluxRequiredSource() (field, kind, name, namespace string) {
	if c.Spec.Suspend {
		return "", "", "", ""
	}
	return "sourceRef", c.Spec.SourceRef.Kind, c.Spec.SourceRef.Name, ""
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
