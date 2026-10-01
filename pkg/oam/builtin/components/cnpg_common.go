package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// This file holds what the cnpg-pooler, cnpg-database and cnpg-objectstore
// kind components share (docs/oam/design-operator-cr-components.md).
// cnpg-cluster predates it and keeps its own copies; folding them together is
// a separate change.

// decodeCnpgSpec decodes a kind component's properties strictly into the
// upstream spec type T, under the package's null contract, exactly as
// CnpgClusterHandler.ToApplicationConfig does: the properties are read as
// their JSON serialization (jsonProperties), a null key is dropped and a null
// array element refused by path, and an unknown key or a wrongly typed value
// at any depth is an error. The unstripped tree is decoded too, so a key the
// type does not declare is refused even when its value is null. kind names the
// upstream type in the error ("postgresql.cnpg.io/v1 PoolerSpec").
//
// It returns the decoded spec and the stripped property tree, which
// refuseUncarriedCnpgValues and any authorship check read.
func decodeCnpgSpec[T any](props map[string]any, kind string) (*T, map[string]any, error) {
	stripped, raw, err := jsonProperties(props)
	if err != nil {
		return nil, nil, err
	}
	spec, _, err := builtin.DecodeStrictJSON[T](stripped)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "properties do not decode into a %s", kind)
	}
	if _, _, err := builtin.DecodeStrictJSON[T](raw); err != nil {
		return nil, nil, errors.Wrapf(err, "properties do not decode into a %s", kind)
	}
	return spec, stripped, nil
}

// refuseUncarriedCnpgValues is refuseUncarriedValues for any spec type: it
// refuses an authored 0 or false that spec's encoding omits on a field of
// defaulted (json path with [] for an array element -> CRD default), where the
// API server would apply that non-zero default instead, and two spellings of
// one field in the same object, of which encoding/json keeps only one.
func refuseUncarriedCnpgValues(authored map[string]any, spec any, defaulted map[string]string) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return errors.Wrap(err, "internal: encode the decoded spec")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var encoded any
	if err := dec.Decode(&encoded); err != nil {
		return errors.Wrap(err, "internal: decode the encoded spec")
	}
	return compareCarriedIn(authored, encoded, "", "", defaulted)
}

// compareCarriedIn is compareCarried with the defaulted-zero list passed in
// rather than read from cnpgClusterDefaultedZeroFields; the walk is the same.
func compareCarriedIn(authored, encoded any, path, field string, defaulted map[string]string) error {
	switch a := authored.(type) {
	case map[string]any:
		e, ok := encoded.(map[string]any)
		if !ok {
			return nil
		}
		join := func(base, k string) string {
			if base == "" {
				return k
			}
			return base + "." + k
		}
		claimed := make(map[string]string, len(a))
		var unmatched []string
		for _, k := range slices.Sorted(maps.Keys(a)) {
			child := join(path, k)
			ek, present := encodedKey(e, k)
			if !present {
				for _, prev := range unmatched {
					if strings.EqualFold(prev, k) {
						return errors.Errorf("%s: sets the same field as %s (field names match case-insensitively, so one value would be dropped)", child, join(path, prev))
					}
				}
				unmatched = append(unmatched, k)
				if def, ok := lookupFolded(defaulted, join(field, k)); ok && isOmittedZero(a[k]) {
					return errors.Errorf("%s: %v cannot be carried by the CloudNativePG API types (the field is omitted when zero, so the operator would apply its default %s)", child, a[k], def)
				}
				continue
			}
			if other, dup := claimed[ek]; dup {
				return errors.Errorf("%s: sets the same field as %s (field names match case-insensitively, so one value would be dropped)", child, other)
			}
			claimed[ek] = child
			if err := compareCarriedIn(a[k], e[ek], child, join(field, ek), defaulted); err != nil {
				return err
			}
		}
	case []any:
		e, ok := encoded.([]any)
		if !ok {
			return nil
		}
		for i := range min(len(a), len(e)) {
			if err := compareCarriedIn(a[i], e[i], fmt.Sprintf("%s[%d]", path, i), field+"[]", defaulted); err != nil {
				return err
			}
		}
	}
	return nil
}

// lookupFolded returns the value of m's key that matches field
// case-insensitively, as defaultedZeroField does for the Cluster's list.
func lookupFolded(m map[string]string, field string) (string, bool) {
	for _, known := range slices.Sorted(maps.Keys(m)) {
		if strings.EqualFold(known, field) {
			return m[known], true
		}
	}
	return "", false
}

// requireCnpgClusterRef refuses a spec.cluster reference with no name: the
// Pooler and Database CRDs require it, and the Go type would encode an
// unauthored one as `cluster: {name: ""}`. The referenced name must also be one
// CloudNativePG accepts for a Cluster (validateCnpgClusterName's rule), or the
// reference could never resolve.
func requireCnpgClusterRef(name string) error {
	if name == "" {
		return errors.New("cluster.name: required (the name of the CloudNativePG Cluster this object belongs to)")
	}
	if len(validation.IsDNS1035Label(name)) > 0 || len(name) > cnpgClusterNameMaxLength {
		return errors.Errorf("cluster.name %q: must be a DNS-1035 label of at most %d characters, as a CloudNativePG Cluster name is", name, cnpgClusterNameMaxLength)
	}
	return nil
}

// enforcePodTemplatePolicy applies the environment policy gates the workload
// kinds apply to their pod to an operator CR's raw pod template, which the
// operator copies into the pods it creates: host namespaces, hostPath volumes,
// the storage maximum on a generic ephemeral volume's claim, the pod-level
// hostProcess switch, pod-level resources, and, for every init and regular
// container, the registry allowlist on an authored image, the cpu and memory
// maxima, and the privileged, hostProcess and capability checks. Ephemeral
// containers are not policed here: a pod template cannot declare them, so the
// caller refuses them outright. label prefixes each error with the template's
// path.
func enforcePodTemplatePolicy(label string, ps *corev1.PodSpec, p oam.Policy) error {
	if ps == nil {
		return nil
	}
	cfg := PodSpecConfig{PodSpec: *ps}
	if err := enforceHostNamespaces(cfg, p); err != nil {
		return errors.Wrap(err, label)
	}
	if err := enforceHostPathVolumes(ps.Volumes, p.AllowHostPathVolumes()); err != nil {
		return errors.Wrap(err, label)
	}
	// A generic ephemeral volume provisions a claim for every pod, so its
	// storage request is capped as cnpg-cluster caps its ephemeralVolumeSource
	// claim.
	for _, v := range ps.Volumes {
		if v.Ephemeral == nil || v.Ephemeral.VolumeClaimTemplate == nil {
			continue
		}
		if q, ok := v.Ephemeral.VolumeClaimTemplate.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			where := fmt.Sprintf("volume %q ephemeral.volumeClaimTemplate.spec.resources.requests.storage", v.Name)
			if err := enforceMaxResource(q.String(), p.MaxStorageSize(), where); err != nil {
				return errors.Wrap(err, label)
			}
		}
	}
	// The pod-level hostProcess and resources checks are enforcePodHostProcess
	// and enforcePodResources, restated so their errors name the template's
	// own fields rather than the workload kinds' podSecurityContext and
	// podResources properties.
	if sc := ps.SecurityContext; !p.AllowPrivileged() && sc != nil && sc.WindowsOptions != nil &&
		sc.WindowsOptions.HostProcess != nil && *sc.WindowsOptions.HostProcess {
		return errors.Errorf("%s.securityContext.windowsOptions.hostProcess is not allowed by environment policy", label)
	}
	if r := ps.Resources; r != nil {
		for _, c := range []struct {
			list  corev1.ResourceList
			name  corev1.ResourceName
			max   string
			label string
		}{
			{r.Requests, corev1.ResourceCPU, p.MaxCPU(), "resources cpu request"},
			{r.Limits, corev1.ResourceCPU, p.MaxCPU(), "resources cpu limit"},
			{r.Requests, corev1.ResourceMemory, p.MaxMemory(), "resources memory request"},
			{r.Limits, corev1.ResourceMemory, p.MaxMemory(), "resources memory limit"},
		} {
			if err := enforceMaxResource(quantityString(c.list, c.name), c.max, c.label); err != nil {
				return errors.Wrap(err, label)
			}
		}
	}
	check := func(kind string, i int, name, image string, res corev1.ResourceRequirements, sc *corev1.SecurityContext) error {
		where := fmt.Sprintf("%s.%s[%d] %q", label, kind, i, name)
		if image != "" {
			if err := enforceAllowedRegistries(image, p.AllowedRegistries()); err != nil {
				return errors.Wrap(err, where)
			}
		}
		if err := enforceMaxContainerResources(res, p); err != nil {
			return errors.Wrap(err, where)
		}
		if err := enforcePrivileged(sc, p.AllowPrivileged()); err != nil {
			return errors.Wrap(err, where)
		}
		if err := enforceContainerCapabilities(sc, p.AllowedContainerCapabilities(), p.ForbiddenContainerCapabilities()); err != nil {
			return errors.Wrap(err, where)
		}
		return nil
	}
	for i, c := range ps.InitContainers {
		if err := check("initContainers", i, c.Name, c.Image, c.Resources, c.SecurityContext); err != nil {
			return err
		}
	}
	for i, c := range ps.Containers {
		if err := check("containers", i, c.Name, c.Image, c.Resources, c.SecurityContext); err != nil {
			return err
		}
	}
	return nil
}

// validateCnpgResources applies admission's checks on a resource block the
// operator copies onto a pod or container unchanged, as cnpg-cluster's
// Generate does: hugepages need cpu or memory, and a request may not exceed
// its limit (extended and hugepages resources need the two equal). Without it
// the build would emit a CR whose pods admission refuses.
func validateCnpgResources(label string, r corev1.ResourceRequirements) error {
	if err := validateHugePagesHaveCPUOrMemory("resources", r.Requests, r.Limits); err != nil {
		return errors.Wrap(err, label)
	}
	if err := validateResourceRequestLimit(r.Requests, r.Limits); err != nil {
		return errors.Wrap(err, label)
	}
	return nil
}

// validatePodTemplateResources runs validateCnpgResources on a raw pod
// template's pod-level resources and on every init and regular container.
func validatePodTemplateResources(label string, ps *corev1.PodSpec) error {
	if ps.Resources != nil {
		if err := validateCnpgResources(label, *ps.Resources); err != nil {
			return err
		}
	}
	for i, c := range ps.InitContainers {
		if err := validateCnpgResources(fmt.Sprintf("%s.initContainers[%d] %q", label, i, c.Name), c.Resources); err != nil {
			return err
		}
	}
	for i, c := range ps.Containers {
		if err := validateCnpgResources(fmt.Sprintf("%s.containers[%d] %q", label, i, c.Name), c.Resources); err != nil {
			return err
		}
	}
	return nil
}

// enforceMaxContainerResources caps the cpu and memory requests and limits of
// a corev1.ResourceRequirements as written, the direct form cnpg-cluster uses:
// the operator copies the block unchanged, so there is no default tier.
func enforceMaxContainerResources(res corev1.ResourceRequirements, p oam.Policy) error {
	for _, chk := range []struct {
		list  corev1.ResourceList
		name  corev1.ResourceName
		max   string
		label string
	}{
		{res.Requests, corev1.ResourceCPU, p.MaxCPU(), "cpu request"},
		{res.Limits, corev1.ResourceCPU, p.MaxCPU(), "cpu limit"},
		{res.Requests, corev1.ResourceMemory, p.MaxMemory(), "memory request"},
		{res.Limits, corev1.ResourceMemory, p.MaxMemory(), "memory limit"},
	} {
		if err := enforceMaxResource(quantityString(chk.list, chk.name), chk.max, chk.label); err != nil {
			return err
		}
	}
	return nil
}
