package components

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components share whose object makes the
// Prometheus operator run pods (go-kure/launcher#790): alertmanager and
// thanosruler. The
// operator builds a StatefulSet from the object's spec, so the fields of that
// spec that shape the pods are held as a workload kind's own are: each kind
// maps its spec into one monitoringWorkload, and the two functions below read
// that value, with or without an environment policy
// (validateMonitoringWorkload) and under one (enforceMonitoringWorkloadPolicy).
//
// What is held is what the object's author wrote, and, where the operator
// fills a value the policy has a dimension for into what the author left unset,
// the value it fills: a replica count, a memory request. Each such value is
// read from the operator's source at the version the linked module is cut
// from, and is cited where the kind maps it; it is held, not written into the
// object. The images of the containers the operator generates are its own
// choice where the spec names none, for them or in a listed entry that patches
// them; under a policy with allowed registries that choice cannot be held, so
// it is refused. The arguments the operator derives are not held.

// monitoringWorkloadDefaultedZeros is a workload kind's defaulted-zero list for
// refuseUncarriedSpecValues, for ps, the decoded spec's pod fields the list
// depends on (its hostNetwork, which the operator copies to the pods). It
// holds the rows of the pod kinds' list (podSpecDefaultedZeros) under the
// containers, init containers and volumes the spec lists, which are of the
// Kubernetes types and reach the pods as written, a container port's hostPort
// under hostNetwork included, and own: the fields of the operator's own types
// that the encoding omits when empty and to which the CRD gives another
// default, or that the operator copies to a pod field the API server defaults,
// each mapped to that default. The pod kinds' rows for the pod spec's own
// fields are not held as such: these specs carry no restartPolicy, and their
// dnsPolicy and schedulerName are fields of the operator's types, in own
// where an authored "" cannot be carried.
func monitoringWorkloadDefaultedZeros(ps *corev1.PodSpec, own map[string]string) defaultedZeroFields {
	fields := podSpecDefaultedZeros("", ps).fields
	maps.DeleteFunc(fields, func(path, _ string) bool {
		return !strings.HasPrefix(path, "containers[].") && !strings.HasPrefix(path, "initContainers[].") && !strings.HasPrefix(path, "volumes[].")
	})
	maps.Copy(fields, own)
	return defaultedZeroFields{api: "Prometheus operator", defaulter: "API server", fields: fields}
}

// fieldValue is one authored string with the path of the property that holds
// it.
type fieldValue struct {
	path, value string
}

// refuseUnservableExternalURL refuses an externalUrl the monitored binary exits
// on at startup: the Prometheus operator passes a nonempty one unchanged as
// --web.external-url, and the binary parses it with net/url and fails to start
// where that fails. Where schemes names any, a URL of another scheme is
// refused too, as Alertmanager refuses one not of http or https
// (go-kure/launcher#948). url.Parse lower-cases the scheme, so HTTPS is https
// here as in the binary. binary names the binary in the message. An empty
// value is not read: the operator then passes no flag, and the binary derives
// the URL. No message names the value or its scheme: it is authored text, and
// url.Parse's error repeats it (validateURLScheme, manifestsource.go).
func refuseUnservableExternalURL(binary, value string, schemes []string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return errors.Errorf("externalUrl: not a URL Go's net/url can parse: the Prometheus operator passes it to %s, which then exits at startup; name a valid URL, or leave it unset", binary)
	}
	if len(schemes) > 0 && !slices.Contains(schemes, u.Scheme) {
		return errors.Errorf("externalUrl: not a URL of scheme %s: the Prometheus operator passes it to %s, which exits at startup on any other; name such a URL, or leave it unset", strings.Join(schemes, " or "), binary)
	}
	return nil
}

// fieldResources is one authored resource block with the path of the property
// that holds it.
type fieldResources struct {
	path      string
	resources corev1.ResourceRequirements
}

// monitoringWorkload is what one such object asks the operator to run, in the
// terms the environment policy is stated in. It shares the spec's own slices
// and maps: both functions only read it.
type monitoringWorkload struct {
	// pod holds the fields of the spec that the policy on a pod reads, under the
	// pod spec's own names, and no other field: the operator copies them into
	// the pod template of its StatefulSet. A container listed under the name of
	// one the operator generates is a patch of that container, and is held like
	// any other.
	pod corev1.PodSpec
	// generated names the containers the operator generates, by the property
	// that lists them (containers, initContainers): a listed entry of one of
	// these names is merged into that container, and may name no image. Every
	// kind must set it: where it is nil, every listed entry without an image
	// is refused, a patch of the operator's own containers included. A list the
	// operator merges into none of its containers has no key.
	generated map[string][]string
	// generatedPorts are the ports the operator gives the containers it
	// generates, by container name, in the order it lists them, each with
	// its name, number and protocol.
	generatedPorts map[string][]corev1.ContainerPort
	// serviceName names the Service the spec makes the StatefulSet's
	// governing one, nil where the operator creates its own.
	serviceName *string
	// images are the full image references the spec names outside pod. An
	// empty value names no image and is not listed.
	images []fieldValue
	// unsetImages name the images of the containers the operator generates
	// that the spec leaves to it: unset or empty in their own field, and named
	// by no listed entry that patches the container.
	unsetImages []string
	// replicas is the number of pods the operator runs: the count the spec
	// asks for in total, or the operator's own where the spec leaves it unset.
	// replicasPath names the property, or the properties, it was read from.
	replicas     *int64
	replicasPath string
	// storage is the spec's storage block, nil where it has none.
	storage *monitoringv1.StorageSpec
	// resources are the resource blocks of the containers the operator
	// generates from fields outside pod, as it runs them: the spec's block
	// with the requests and limits of a listed entry that patches the container
	// merged over it, key by key (mergedResources).
	resources []fieldResources
	// mergedPatches names, by the property that lists it, the generated
	// container whose patch's resources are held merged in resources: the
	// patch's block alone is then not checked against the API's rules, as the
	// operator never runs it alone.
	mergedPatches map[string]string
	// memoryRequests is the memory request the operator fills into a block of
	// resources that names none, by the path of the block. The block is checked
	// with it filled.
	memoryRequests map[string]string
	// literals are the paths of the credentials the spec holds in the clear.
	literals []string
}

// validateMonitoringWorkload refuses what the build refuses of a workload with
// or without an environment policy: an image reference without a tag or a
// digest, or tagged latest (ValidateImageRef), on every image the workload
// names, and a resource block whose request exceeds its limit
// (validateCnpgResources). A name listed twice in one list is refused: the
// operator keeps the last entry of a name only, so an earlier one would be
// held and not run (MergePatchContainers, pkg/k8s/merge.go at
// prometheus-operator v0.94.1). A name shared by an init container and a
// container of the pods is refused (refuseSharedContainerNames), and so are a
// dnsPolicy of None without a nameserver and a pod-level HostProcess without
// hostNetwork, which the API refuses of a pod (podspec.go holds both on the
// pod kinds), as are two volumes of one name and a serviceName that is not a
// DNS-1035 label, the rule of every Service name here. A listed container named for one
// the operator generates is merged into it, its ports by number (runPorts),
// and may name no image; any other listed container is added to the pods as
// written, and one that names no image is refused, since no pod runs it. A
// container whose ports, as the pods run them, name two ports alike is
// refused (refuseDuplicatePortNames), since the API refuses two ports of one
// name. An image volume that names no image is not checked. The blocks of
// resources are checked as the operator runs them: a patch merged in one (mergedPatches) is not checked alone, and a
// block that names no memory request is checked with the request the
// operator fills (memoryRequests), which satisfies the API's rule that
// hugepages need cpu or memory. Such a block that names a memory limit is
// refused where the filled request exceeds it: the operator fills it whatever
// the limit, and the API refuses a pod whose request exceeds its limit.
func validateMonitoringWorkload(w monitoringWorkload) error {
	for _, image := range w.images {
		if err := ValidateImageRef(image.value); err != nil {
			return errors.Wrap(err, image.path)
		}
	}
	for _, list := range []struct {
		name       string
		containers []corev1.Container
	}{{"initContainers", w.pod.InitContainers}, {"containers", w.pod.Containers}} {
		first := map[string]int{}
		for i, c := range list.containers {
			where := fmt.Sprintf("%s[%d] %q", list.name, i, c.Name)
			if j, seen := first[c.Name]; seen {
				return errors.Errorf("%s: the name is listed already at %s[%d], and the Prometheus operator keeps only the last entry of a name; list each container once", where, list.name, j)
			}
			first[c.Name] = i
			switch {
			case c.Image != "":
				if err := ValidateImageRef(c.Image); err != nil {
					return errors.Wrap(err, where)
				}
			case len(w.generated[list.name]) == 0:
				return errors.Errorf("%s: names no image, and the Prometheus operator merges no entry of %s into a container of its own; name an image", where, list.name)
			case !slices.Contains(w.generated[list.name], c.Name):
				return errors.Errorf("%s: names no image, and the Prometheus operator generates no container of that name to merge it into; name an image, or the container it patches (%s)", where, strings.Join(w.generated[list.name], ", "))
			}
			// A patch of a container the operator gives no ports keeps its
			// ports as listed: the merge takes the patch's list whole where
			// the generated container has none (mergeMap, apimachinery
			// strategicpatch/patch.go:1374-1386 at v0.37.0).
			merging := slices.Contains(w.generated[list.name], c.Name) && len(w.generatedPorts[c.Name]) > 0
			if err := refuseDuplicatePortNames(where, merging, runPorts(merging, w.generatedPorts[c.Name], c.Ports)); err != nil {
				return err
			}
			if patched, merged := w.mergedPatches[list.name]; merged && patched == c.Name {
				continue
			}
			if err := validateNonNegativeResources(where, c.Resources); err != nil {
				return err
			}
			if err := validateCnpgResources(where, c.Resources); err != nil {
				return err
			}
		}
	}
	if err := refuseSharedContainerNames(w); err != nil {
		return err
	}
	// The operator adds the listed volumes to the pods as written
	// (makeStatefulSetSpec, pkg/alertmanager/statefulset.go:198 at v0.94.1),
	// and the API refuses a pod with two volumes of one name.
	first := map[string]int{}
	for i, v := range w.pod.Volumes {
		if j, seen := first[v.Name]; seen {
			return errors.Errorf("volumes[%d] %q: the name is listed already at volumes[%d], and the API refuses a pod with two volumes of one name, which the Prometheus operator adds to the pods as listed; list each volume once", i, v.Name, j)
		}
		first[v.Name] = i
	}
	// The operator fails the reconcile where it cannot get the Service the
	// spec names (EnsureCustomGoverningService, pkg/k8s/network.go:135-140
	// at v0.94.1). The name is held to the rule every Service name of this
	// package is (validateServiceName), a DNS-1035 label: the API refuses a
	// Service of another name before Kubernetes 1.36, by default (the
	// RelaxedServiceNameValidation feature gate; go-kure/launcher#959).
	if w.serviceName != nil {
		if err := validateServiceName("serviceName", *w.serviceName); err != nil {
			return errors.Errorf("%w; the API refuses a Service of such a name before Kubernetes 1.36 (by default), so the Prometheus operator fails to find the governing Service and builds no pods", err)
		}
	}
	// The API's pod rules across fields the spec carries apart: the operator
	// copies both into the pod template, and the API then refuses the pods.
	if w.pod.DNSPolicy == corev1.DNSNone && (w.pod.DNSConfig == nil || len(w.pod.DNSConfig.Nameservers) == 0) {
		return errors.New("dnsPolicy: None requires dnsConfig.nameservers with at least one entry; the API refuses the pods the Prometheus operator builds without one")
	}
	if sc := w.pod.SecurityContext; sc != nil && sc.WindowsOptions != nil && sc.WindowsOptions.HostProcess != nil && *sc.WindowsOptions.HostProcess && !w.pod.HostNetwork {
		return errors.New("securityContext.windowsOptions.hostProcess: hostNetwork must be true when hostProcess is true; the API refuses the pods the Prometheus operator builds otherwise")
	}
	if err := validateImageVolumeRefs("", &w.pod); err != nil {
		return err
	}
	for _, r := range w.resources {
		if err := validateNonNegativeResources(resourcesHolder(r.path), r.resources); err != nil {
			return err
		}
		block := r.resources
		if q, ok := w.memoryRequests[r.path]; ok {
			if _, named := block.Requests[corev1.ResourceMemory]; !named {
				filled := resource.MustParse(q)
				if limit, limited := block.Limits[corev1.ResourceMemory]; limited && filled.Cmp(limit) > 0 {
					return errors.Errorf("%s: memory: the unset request the Prometheus operator fills as %s must not exceed limit %s; name a request no larger than the limit", r.path, q, limit.String())
				}
				block.Requests = maps.Clone(block.Requests)
				if block.Requests == nil {
					block.Requests = corev1.ResourceList{}
				}
				block.Requests[corev1.ResourceMemory] = filled
			}
		}
		if err := validateResourcesAt(r.path, block); err != nil {
			return err
		}
	}
	return nil
}

// runPort is a port of a container as the pods run it, with what gives the
// port its name: ports[j] of the listed entry, or the operator.
type runPort struct {
	port corev1.ContainerPort
	from string
}

// runPorts are the ports of a listed container as the pods run them, with
// merge set for a patch of a generated container that has ports. The
// operator adds a container it does not generate as listed
// (MergePatchContainers, pkg/k8s/merge.go:65-70 at prometheus-operator
// v0.94.1). It merges a patch of one it generates into the generated one with
// a strategic merge (merge.go:44-62), which merges ports by number
// (patchMergeKey containerPort): each port of the patch, in its order, is
// merged into the first port of its number, the operator's or one an earlier
// port of the patch added, and replaces the name and protocol it names; a
// port of a number no port has is added (mergeSliceWithoutSpecialElements and
// findMapInSliceBasedOnKeyValue, apimachinery strategicpatch/patch.go:
// 1606-1667 at v0.37.0, the version the operator builds with). The merge
// keeps every port, in an order of its own.
func runPorts(merge bool, generated, listed []corev1.ContainerPort) []runPort {
	var ports []runPort
	if merge {
		for _, p := range generated {
			ports = append(ports, runPort{p, "the Prometheus operator's port"})
		}
	}
	for j, p := range listed {
		from := fmt.Sprintf("ports[%d]", j)
		i := -1
		if merge {
			i = slices.IndexFunc(ports, func(q runPort) bool { return q.port.ContainerPort == p.ContainerPort })
		}
		if i < 0 {
			ports = append(ports, runPort{p, from})
			continue
		}
		if p.Name != "" {
			ports[i].port.Name, ports[i].from = p.Name, from
		}
		if p.Protocol != "" {
			ports[i].port.Protocol = p.Protocol
		}
	}
	return ports
}

// refuseDuplicatePortNames refuses a container whose ports, as the pods run
// them (runPorts), name two ports alike: the API requires the names of a
// container's ports to be unique. An unnamed port names none. merge is as
// runPorts was given it.
func refuseDuplicatePortNames(where string, merge bool, ports []runPort) error {
	first := map[string]int{}
	for i, p := range ports {
		if p.port.Name == "" {
			continue
		}
		j, seen := first[p.port.Name]
		if !seen {
			first[p.port.Name] = i
			continue
		}
		later, earlier := p, ports[j]
		if !merge {
			return errors.Errorf("%s: %s %q: the name is that of %s already, and the API refuses a container with two ports of one name, which the Prometheus operator gives the pods as listed; name the port otherwise", where, later.from, later.port.Name, earlier.from)
		}
		// The operator's ports are named apart, as each kind refuses a spec
		// that names two of them alike before this runs
		// (validateAlertmanagerPortName), so one of the two is the patch's;
		// name that one.
		if !strings.HasPrefix(later.from, "ports[") {
			later, earlier = earlier, later
		}
		return errors.Errorf("%s: %s %q at %s: the Prometheus operator merges the patch's ports into the container's by number, which leaves another port of that name, %s at %s, and the API refuses a container with two ports of one name; name the port otherwise, or give it the number of the port it is to replace", where, later.from, later.port.Name, portAt(later.port), earlier.from, portAt(earlier.port))
	}
	return nil
}

// portAt is a port's number and protocol; the API defaults an unset protocol
// to TCP.
func portAt(p corev1.ContainerPort) string {
	protocol := p.Protocol
	if protocol == "" {
		protocol = corev1.ProtocolTCP
	}
	return fmt.Sprintf("%d/%s", p.ContainerPort, protocol)
}

// refuseSharedContainerNames refuses a name shared by an init container and a
// container of the pods: the API requires the names of both lists to be unique
// together. The pods' init containers are the ones the operator generates and
// the listed initContainers, and their containers likewise; a listed entry of
// a generated container's name in its own list is a patch of it, and adds no
// name.
func refuseSharedContainerNames(w monitoringWorkload) error {
	for i, c := range w.pod.InitContainers {
		if slices.Contains(w.generated["containers"], c.Name) {
			return errors.Errorf("initContainers[%d] %q: the name is that of a container the Prometheus operator generates, and the API refuses a pod whose init containers and containers share a name; name the init container otherwise", i, c.Name)
		}
	}
	initNames := map[string]string{}
	for _, name := range w.generated["initContainers"] {
		initNames[name] = "the init container the Prometheus operator generates"
	}
	for i, c := range w.pod.InitContainers {
		if _, ok := initNames[c.Name]; !ok {
			initNames[c.Name] = fmt.Sprintf("initContainers[%d]", i)
		}
	}
	for i, c := range w.pod.Containers {
		if what, ok := initNames[c.Name]; ok {
			return errors.Errorf("containers[%d] %q: the name is also that of %s, and the API refuses a pod whose init containers and containers share a name; name the container otherwise", i, c.Name, what)
		}
	}
	return nil
}

// validateResourcesAt is validateCnpgResources for a resource block the spec
// names by path, which ends in the property `resources`. The checks name the
// block by that word themselves, so the error is prefixed with what holds the
// block, and with nothing where the spec itself does.
func validateResourcesAt(path string, r corev1.ResourceRequirements) error {
	holder := resourcesHolder(path)
	if holder == "" {
		if err := validateHugePagesHaveCPUOrMemory("resources", r.Requests, r.Limits); err != nil {
			return err
		}
		return validateResourceRequestLimit(r.Requests, r.Limits)
	}
	return validateCnpgResources(holder, r)
}

// resourcesHolder is what holds the resource block the spec names by path:
// the path less its last word, resources, and nothing where the spec itself
// holds the block.
func resourcesHolder(path string) string {
	return strings.TrimSuffix(strings.TrimSuffix(path, "resources"), ".")
}

// validateNonNegativeResources refuses a negative request or limit in a block
// of resources, prefixed with what holds the block, and with nothing where the
// spec itself does. The API admits the operator's object with it, as the
// CRD's quantity pattern allows a sign; the operator copies the block into the
// container it builds, and the API refuses a container with a negative
// quantity (ValidateResourceQuantityValue, k8s.io/kubernetes
// pkg/apis/core/validation/validation.go), so the pods are never created.
func validateNonNegativeResources(holder string, r corev1.ResourceRequirements) error {
	for _, list := range []struct {
		name string
		rl   corev1.ResourceList
	}{{"requests", r.Requests}, {"limits", r.Limits}} {
		for _, name := range slices.Sorted(maps.Keys(list.rl)) {
			if q := list.rl[name]; q.Sign() < 0 {
				err := errors.Errorf("resources: %s: %s %s is below 0: the Prometheus operator puts it into the container it builds, and the API refuses a negative quantity", name, strings.TrimSuffix(list.name, "s"), q.String())
				if holder == "" {
					return err
				}
				return errors.Wrap(err, holder)
			}
		}
	}
	return nil
}

// enforceMonitoringWorkloadPolicy holds the workload to the environment policy
// p, which is never nil. It refuses or passes, and fills no default:
//
//   - a credential in the clear, under a policy that forbids explicit secrets;
//   - an image outside the allowed registries;
//   - an image of a container the operator generates that the spec leaves to
//     it, under a policy that lists allowed registries: the operator would
//     choose the image, and no allowlist reaches that choice;
//   - more pods than the replica maximum, the operator's count included where
//     the spec leaves it unset;
//   - a claim that requests more than the storage maximum, in the storage
//     block's arm the operator uses: emptyDir, then ephemeral, then
//     volumeClaimTemplate, so a claim template is held only where no arm
//     before it is set;
//   - a resource block over the cpu or memory maximum, the memory request the
//     operator fills included where the block names none;
//   - the pod fields, as a workload kind's pod template is held
//     (enforcePodTemplatePolicy): host namespaces, hostPath volumes, privilege
//     and capabilities, and the images, resources and claims of the listed
//     containers and volumes.
//
// The size limit of an emptyDir is not held, here or on any kind. No replica,
// resource or storage default of the policy is applied, and nothing is
// written: the operator's own values are held, not filled in.
func enforceMonitoringWorkloadPolicy(w monitoringWorkload, p oam.Policy) error {
	if len(w.literals) > 0 && !oam.ExplicitSecretsAllowed(p) {
		return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, fmt.Sprintf("%s: holds a credential in the object, and the environment policy forbids explicit secrets; name the key of a Secret created out of band instead", w.literals[0]))
	}
	allowed := p.AllowedRegistries()
	for _, image := range w.images {
		if err := enforceAllowedRegistries(image.value, allowed); err != nil {
			return errors.Wrap(err, image.path)
		}
	}
	if len(allowed) > 0 && len(w.unsetImages) > 0 {
		return oam.NewPolicyRefusal(oam.RefusalRegistry, fmt.Sprintf("%s: unset, so the Prometheus operator chooses the image the pods run, which the allowed registries %v cannot hold; name an image from one of them", w.unsetImages[0], allowed))
	}
	if max := p.MaxReplicas(); w.replicas != nil && max != nil && *w.replicas > int64(*max) {
		return oam.NewPolicyRefusal(oam.RefusalReplicaMaximum, fmt.Sprintf("%s %d exceeds enforced maximum %d", w.replicasPath, *w.replicas, *max))
	}
	if claim, ok := storageClaim(w.storage); ok {
		if q, ok := claim.resources.Requests[corev1.ResourceStorage]; ok {
			if err := enforceMaxStorageAt(q.String(), p.MaxStorageSize(), claim.path); err != nil {
				return err
			}
		}
	}
	for _, r := range w.resources {
		if err := enforceMaxContainerResources(r.resources, p); err != nil {
			return errors.Wrap(err, r.path)
		}
		if q, ok := w.memoryRequests[r.path]; ok {
			if _, named := r.resources.Requests[corev1.ResourceMemory]; !named {
				if err := enforceMaxResource(q, p.MaxMemory(), "memory request"); err != nil {
					return errors.Wrap(err, fmt.Sprintf("%s, whose unset memory request the Prometheus operator fills as %s", r.path, q))
				}
			}
		}
	}
	return enforcePodTemplatePolicy("", &w.pod, p)
}

// storageClaim returns the claim the operator makes for the pods' data from
// the storage block s, with the path of its storage request, and false where
// it makes none: s is nil, or names an emptyDir, or an ephemeral volume
// without a claim template. The operator reads the arms in that order and
// uses the first that is set, the volumeClaimTemplate last; an arm after the
// one it uses makes no claim.
func storageClaim(s *monitoringv1.StorageSpec) (fieldResources, bool) {
	switch {
	case s == nil, s.EmptyDir != nil:
		return fieldResources{}, false
	case s.Ephemeral != nil:
		if s.Ephemeral.VolumeClaimTemplate == nil {
			return fieldResources{}, false
		}
		return fieldResources{"storage.ephemeral.volumeClaimTemplate.spec.resources.requests.storage", corev1.ResourceRequirements{Requests: s.Ephemeral.VolumeClaimTemplate.Spec.Resources.Requests}}, true
	default:
		return fieldResources{"storage.volumeClaimTemplate.spec.resources.requests.storage", corev1.ResourceRequirements{Requests: s.VolumeClaimTemplate.Spec.Resources.Requests}}, true
	}
}

// patchOf returns the index of the listed entry of containers that the
// operator merges into the container of that name it generates, and -1 where
// none is named so. The operator keeps the last entry of a name
// (MergePatchContainers, pkg/k8s/merge.go at prometheus-operator v0.94.1);
// validateMonitoringWorkload refuses a name listed twice.
func patchOf(containers []corev1.Container, name string) int {
	for i := len(containers) - 1; i >= 0; i-- {
		if containers[i].Name == name {
			return i
		}
	}
	return -1
}

// patchedImage returns the image the listed entry of containers that patches
// the generated container name writes into it, and "" where no entry patches
// it or the patch names none: the operator's own image then stays.
func patchedImage(containers []corev1.Container, name string) string {
	if i := patchOf(containers, name); i >= 0 {
		return containers[i].Image
	}
	return ""
}

// mergedResources returns the resource block of a generated container as the
// operator runs it: base, the block it builds the container with, and the
// requests and limits of the entry that patches the container merged over
// it, key by key, as a strategic merge of the two maps does
// (MergePatchContainers, pkg/k8s/merge.go at prometheus-operator v0.94.1). It
// shares neither map with its arguments.
func mergedResources(base, patch corev1.ResourceRequirements) corev1.ResourceRequirements {
	merge := func(base, patch corev1.ResourceList) corev1.ResourceList {
		if len(base) == 0 && len(patch) == 0 {
			return nil
		}
		out := corev1.ResourceList{}
		maps.Copy(out, base)
		maps.Copy(out, patch)
		return out
	}
	return corev1.ResourceRequirements{Requests: merge(base.Requests, patch.Requests), Limits: merge(base.Limits, patch.Limits)}
}
