package kurel

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// An authored trait object name (go-kure/launcher#787) is held against the other
// names the transform resolves, as a default name is: Transform refuses two
// traits that name one object, and names both.

// webBesideAPI is component web, whose trait is given, beside component
// api carrying the same trait with default names.
func webBesideAPI(webTrait, apiTrait string) string {
	return duplicateApp(`    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
      traits:
`+webTrait+`    - name: api
      type: webservice
      properties:
        image: ghcr.io/example/api:v1.0.0
        port: 8080
      traits:
`+apiTrait, "", "")
}

const (
	scalerTrait = `        - type: scaler
          properties:
            minReplicas: 2
            maxReplicas: 4
            enablePDB: true
`
	rbacTrait = `        - type: rbac
          properties:
            clusterWide: true
            rules:
              - apiGroups: [""]
                resources: [pods]
                verbs: [get]
`
	networkPolicyTrait = `        - type: networkpolicy
          properties:
            ingress: []
`
)

// transformErr transforms appYAML with kurel's builtin transformer and ctx, and
// returns Transform's own verdict.
func transformErr(t *testing.T, appYAML string, ctx oam.TransformContext) error {
	t.Helper()
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(appYAML), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if err := transformer.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("validating: %v", err)
	}
	ctx.Domain = kurelDomain
	_, err = transformer.Transform(app, ctx)
	return err
}

func TestAuthoredTraitName_CollisionWithAnotherComponentRefused(t *testing.T) {
	for _, tc := range []struct {
		name, trait, override string
		want                  string // the first object the two traits both name, and who names it
	}{
		{
			name: "scaler hpaName", trait: scalerTrait, override: "            hpaName: api-hpa\n",
			want: `component "api" trait "scaler": name collision: HorizontalPodAutoscaler.autoscaling "default/api-hpa" is named by ` +
				`component "web" traits[0] "scaler" (role "hpa", set by hpaName) and by component "api" traits[0] "scaler" (role "hpa", its default); give one of them another name`,
		},
		{
			name: "scaler pdbName", trait: scalerTrait, override: "            pdbName: api-pdb\n",
			want: `component "api" trait "scaler": name collision: PodDisruptionBudget.policy "default/api-pdb" is named by ` +
				`component "web" traits[0] "scaler" (role "pdb", set by pdbName) and by component "api" traits[0] "scaler" (role "pdb", its default); give one of them another name`,
		},
		{
			// One name for all four objects: the first of them is reported.
			name: "rbac name", trait: rbacTrait, override: "            name: api\n",
			want: `component "api" trait "rbac": rbac: name collision: Role.rbac.authorization.k8s.io "default/api" is named by ` +
				`component "web" traits[0] "rbac" (role "rbac", set by name) and by component "api" traits[0] "rbac" (role "rbac", its default); give one of them another name`,
		},
		{
			name: "networkpolicy name", trait: networkPolicyTrait, override: "            name: api-allow\n",
			want: `component "api" trait "networkpolicy": name collision: NetworkPolicy.networking.k8s.io "default/api-allow" is named by ` +
				`component "web" traits[0] "networkpolicy" (role "networkpolicy", set by name) and by component "api" traits[0] "networkpolicy" (role "networkpolicy", its default); give one of them another name`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := collisionCheck(t, webBesideAPI(tc.trait, tc.trait)); err != nil {
				t.Fatalf("default names: %v, want two components with the same trait accepted", err)
			}
			err := transformErr(t, webBesideAPI(tc.trait+tc.override, tc.trait), oam.TransformContext{})
			if err == nil {
				t.Fatalf("an authored name equal to component api's object was accepted, want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v\nwant it to contain %q", err, tc.want)
			}
		})
	}
}
