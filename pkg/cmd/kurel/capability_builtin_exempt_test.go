package kurel

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// builtinCapPolicy is a Policy whose capability lists are set per test.
type builtinCapPolicy struct {
	oam.NoopPolicy
	forbidden, allowed, required []string
}

func (p *builtinCapPolicy) ForbiddenCapabilities() []string { return p.forbidden }
func (p *builtinCapPolicy) AllowedCapabilities() []string   { return p.allowed }
func (p *builtinCapPolicy) RequiredCapabilities() []string  { return p.required }

// The capability lists judge the traits a non-builtin component rule emits. The
// traits launcher's own rules write (topology-spread on a worker and a webservice,
// pvc on a volume, configmap on helm values) are launcher's opinions, so a policy
// that lists what an author may write gives the same outcome for them as before.
func TestCapabilityLists_BuiltinRuleTraitsAreNotJudged(t *testing.T) {
	const doc = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: shop
spec:
  components:
    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 9090
        volumes:
          - name: cache
            type: pvc
            mountPath: /cache
            size: 1Gi
    - name: queue
      type: worker
      properties:
        image: ghcr.io/example/queue:v1.0.0
        volumes:
          - name: cache
            type: pvc
            mountPath: /cache
            size: 1Gi
    - name: podinfo
      type: helm
      properties:
        chart: podinfo
        source:
          kind: HelmRepository
          name: podinfo
        valuesMode: configMap
        values:
          replicaCount: 2
`
	for name, policy := range map[string]*builtinCapPolicy{
		"allowed list that names no builtin trait": {allowed: []string{"expose"}},
		"forbidden pvc":                  {forbidden: []string{"pvc"}},
		"forbidden topology-spread":      {forbidden: []string{"topology-spread"}},
		"forbidden configmap and secret": {forbidden: []string{"configmap", "secret"}},
	} {
		t.Run(name, func(t *testing.T) {
			tr := newBuiltinTransformer()
			app, err := oam.ParseWithExtraTypes([]byte(doc), nil, tr.LowerableTypes())
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if _, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Domain: kurelDomain, Policy: policy}); err != nil {
				t.Errorf("a builtin rule's own trait was judged by the capability lists: %v", err)
			}
		})
	}
}
