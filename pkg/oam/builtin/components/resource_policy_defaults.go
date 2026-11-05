package components

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/oam"
)

// capPolicyRequestDefaults runs before the policy request defaults are applied
// (applyDefaultQuantity). For cpu and memory it fixes a request at the limit the
// author set when the author wrote no request, the policy has a default request
// and that default is above the limit: the default would otherwise render a
// request above its limit, which the apiserver refuses (go-kure/launcher#1217).
// An authored request is never touched, and a default at or under the limit is
// left for applyDefaultQuantity. An unparsable policy default is left for it to
// report. A request it fixes is reported to log as the policy's, with the detail
// oam.DetailCappedToLimit: the value is the author's limit, the cause the default.
func (r *ResourceRequirements) capPolicyRequestDefaults(p oam.Policy, log *oam.PolicyDefaultLog) {
	for _, d := range []struct {
		name     corev1.ResourceName
		dflt     string
		path     string
		accessor string
	}{
		{corev1.ResourceCPU, p.DefaultCPURequest(), "resources.requests.cpu", "DefaultCPURequest"},
		{corev1.ResourceMemory, p.DefaultMemoryRequest(), "resources.requests.memory", "DefaultMemoryRequest"},
	} {
		if _, ok := r.Requests[d.name]; ok || d.dflt == "" {
			continue
		}
		limit, ok := r.Limits[d.name]
		if !ok {
			continue
		}
		q, err := resource.ParseQuantity(d.dflt)
		if err != nil || limit.Cmp(q) >= 0 {
			continue
		}
		if r.Requests == nil {
			r.Requests = corev1.ResourceList{}
		}
		r.Requests[d.name] = limit.DeepCopy()
		log.ReportPolicyDefault(d.path, d.accessor, oam.DetailCappedToLimit)
	}
}
