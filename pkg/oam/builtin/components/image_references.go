package components

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// The configs below implement oam.ImageReferenced: the images they carry, which
// the Transformer holds to the policy's level after the handler has built the
// config (go-kure/launcher#1644). A handler holds them to the level of its
// component as it parses; this is the second reading, so that a stricter level
// is enforced when a type that wraps the handler hands it a component that does
// not carry the level. The prefixes are the paths the parse refusals use.

// ImageReferences implements oam.ImageReferenced.
func (c *PodTemplateConfig) ImageReferences() []oam.ImageReference {
	return podSpecImageReferences("template.spec.", &c.Template.Spec)
}

// ImageReferences implements oam.ImageReferenced.
func (c *DeploymentConfig) ImageReferences() []oam.ImageReference {
	return podSpecImageReferences("template.spec.", &c.Spec.Template.Spec)
}

// ImageReferences implements oam.ImageReferenced.
func (c *DaemonsetConfig) ImageReferences() []oam.ImageReference {
	return podSpecImageReferences("template.spec.", &c.Spec.Template.Spec)
}

// ImageReferences implements oam.ImageReferenced.
func (c *StatefulsetConfig) ImageReferences() []oam.ImageReference {
	return podSpecImageReferences("template.spec.", &c.Spec.Template.Spec)
}

// ImageReferences implements oam.ImageReferenced.
func (c *ReplicaSetConfig) ImageReferences() []oam.ImageReference {
	return podSpecImageReferences("template.spec.", &c.Spec.Template.Spec)
}

// ImageReferences implements oam.ImageReferenced. A ReplicationController whose
// template is not authored names no image.
func (c *ReplicationControllerConfig) ImageReferences() []oam.ImageReference {
	if c.Spec.Template == nil {
		return nil
	}
	return podSpecImageReferences("template.spec.", &c.Spec.Template.Spec)
}

// ImageReferences implements oam.ImageReferenced.
func (c *JobConfig) ImageReferences() []oam.ImageReference {
	return podSpecImageReferences("template.spec.", &c.Spec.Template.Spec)
}

// ImageReferences implements oam.ImageReferenced.
func (c *CronjobConfig) ImageReferences() []oam.ImageReference {
	return podSpecImageReferences("jobTemplate.spec.template.spec.", &c.Spec.JobTemplate.Spec.Template.Spec)
}

// ImageReferences implements oam.ImageReferenced: the main image, each init
// container's and sidecar's, and each image volume's reference.
func (c *RoleDeploymentConfig) ImageReferences() []oam.ImageReference {
	refs := []oam.ImageReference{{Reference: c.Image}}
	for i, ic := range c.InitContainers {
		refs = append(refs, oam.ImageReference{Field: fmt.Sprintf("initContainers[%d] %q", i, ic.Name), Reference: ic.Image})
	}
	for i, sc := range c.Sidecars {
		refs = append(refs, oam.ImageReference{Field: fmt.Sprintf("sidecars[%d] %q", i, sc.Name), Reference: sc.Image})
	}
	refs = append(refs, podSpecImageReferences("", &corev1.PodSpec{Volumes: c.Volumes})...)
	return refs
}
