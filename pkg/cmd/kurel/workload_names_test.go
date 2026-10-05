package kurel

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the names of the objects a webservice and a worker generate
// (go-kure/launcher#787) through kurel's own transformer: the Deployment, the
// Service of a webservice, and the ServiceAccount. Each is named by the author
// (deploymentObjectName, serviceObjectName, serviceAccountObjectName), else by
// the Naming hook (roles workload-deployment, workload-service,
// workload-serviceaccount), else after the component, and every reference
// launcher writes to one of them follows the name it got, while the labels, the
// selectors and the names derived from the component keep the component's.

// workloadNamesDoc holds a webservice "web" and a worker "jobs", each with the
// traits that write a reference to one of its objects: scaler (the Deployment),
// rbac (the account) and, on web, httproute (the Service). webProps and
// jobsProps are appended to their properties.
func workloadNamesDoc(webProps, jobsProps string) string {
	const scalerAndRBAC = `        - type: scaler
          properties:
            minReplicas: 2
            maxReplicas: 4
            enablePDB: true
        - type: rbac
          properties:
            rules:
              - apiGroups: [""]
                resources: [pods]
                verbs: [get]
`
	return helmNamesApp(`    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
` + webProps + `      traits:
` + scalerAndRBAC + `        - type: httproute
          properties:
            parentRefs:
              - name: gw
            rules:
              - backendRefs:
                  - port: 8080
    - name: jobs
      type: worker
      properties:
        image: ghcr.io/example/jobs:v1.0.0
` + jobsProps + `      traits:
` + scalerAndRBAC)
}

// workloadNamesContext is a transform context that gives the httproute trait
// its traffic sources, so web's route gets a synthesized ingress NetworkPolicy,
// and whose Naming hook is hook.
func workloadNamesContext(hook func(oam.NameRequest) (string, bool)) oam.TransformContext {
	return oam.TransformContext{
		Naming: hook,
		Capabilities: map[string]oam.CapabilityBinding{"httproute": {Rendering: map[string]any{
			"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "gateway-system"}}},
		}}},
	}
}

// generatedDocs returns every generated object as the document kurel writes.
func generatedDocs(t *testing.T, apps []oam.GeneratedApplication) []map[string]any {
	t.Helper()
	var docs []map[string]any
	for _, a := range apps {
		for _, p := range a.Objects {
			if p == nil {
				continue
			}
			doc, err := runtime.DefaultUnstructuredConverter.ToUnstructured(*p)
			if err != nil {
				t.Fatalf("converting an object of application %q: %v", a.Name, err)
			}
			docs = append(docs, doc)
		}
	}
	return docs
}

// isWorkloadRole reports whether role is one of the three workload roles.
func isWorkloadRole(role oam.NameRole) bool {
	return role == oam.NameRoleWorkloadDeployment || role == oam.NameRoleWorkloadService || role == oam.NameRoleWorkloadServiceAccount
}

// workloadRoleRequests keeps the requests of the three workload roles.
func workloadRoleRequests(requests []oam.NameRequest) []oam.NameRequest {
	var out []oam.NameRequest
	for _, req := range requests {
		if isWorkloadRole(req.Role) {
			out = append(out, req)
		}
	}
	return out
}

// workloadNames are the names of the objects one component generates; service
// is empty for a worker.
type workloadNames struct{ deployment, service, account string }

// assertWorkloadReferences checks the objects component generates under names,
// and that every reference launcher writes to one of them carries its name:
// the scaler's target, the pods' account and the rbac subject, the route's
// backend. What launcher derives from the component keeps the component name:
// the selectors, and the names of the traits' objects and of the synthesized
// policy.
func assertWorkloadReferences(t *testing.T, docs []map[string]any, component string, names workloadNames) {
	t.Helper()

	deployment := namedDoc(t, docs, "Deployment", names.deployment)
	wantNestedString(t, deployment, component, "spec", "selector", "matchLabels", "app")
	wantNestedString(t, deployment, component, "spec", "template", "metadata", "labels", "app")
	wantNestedString(t, deployment, names.account, "spec", "template", "spec", "serviceAccountName")

	// The scaler scales the Deployment, whatever the Service and the account
	// of the same component are named.
	hpa := namedDoc(t, docs, "HorizontalPodAutoscaler", component+"-hpa")
	wantNestedString(t, hpa, "Deployment", "spec", "scaleTargetRef", "kind")
	wantNestedString(t, hpa, names.deployment, "spec", "scaleTargetRef", "name")
	pdb := namedDoc(t, docs, "PodDisruptionBudget", component+"-pdb")
	wantNestedString(t, pdb, component, "spec", "selector", "matchLabels", "app")

	namedDoc(t, docs, "ServiceAccount", names.account)
	namedDoc(t, docs, "Role", component)
	binding := namedDoc(t, docs, "RoleBinding", component)
	subjects, _, _ := unstructured.NestedSlice(binding, "subjects")
	if len(subjects) != 1 {
		t.Fatalf("RoleBinding %s subjects = %d, want 1", component, len(subjects))
	}
	wantNestedString(t, subjects[0].(map[string]any), "ServiceAccount", "kind")
	wantNestedString(t, subjects[0].(map[string]any), names.account, "name")

	if names.service == "" {
		return
	}
	service := namedDoc(t, docs, "Service", names.service)
	wantNestedString(t, service, component, "spec", "selector", "app")
	route := namedDoc(t, docs, "HTTPRoute", component+"-httproute")
	rules, _, _ := unstructured.NestedSlice(route, "spec", "rules")
	if len(rules) != 1 {
		t.Fatalf("HTTPRoute rules = %d, want 1", len(rules))
	}
	refs, _, _ := unstructured.NestedSlice(rules[0].(map[string]any), "backendRefs")
	if len(refs) != 1 {
		t.Fatalf("HTTPRoute backendRefs = %d, want 1", len(refs))
	}
	wantNestedString(t, refs[0].(map[string]any), names.service, "name")
	// The route's backend is still the component: its synthesized policy is
	// the component's, selecting the component's pods.
	policy := namedDoc(t, docs, "NetworkPolicy", component+"-allow-ingress-traffic")
	wantNestedString(t, policy, component, "spec", "podSelector", "matchLabels", kurelComponentLabel)
	wantNestedString(t, policy, component, "metadata", "labels", kurelComponentLabel)
}

// assertWorkloadObjects checks that the document's Deployments, Services and
// ServiceAccounts are exactly the ones named.
func assertWorkloadObjects(t *testing.T, docs []map[string]any, web, jobs workloadNames) {
	t.Helper()
	for kind, want := range map[string][]string{
		"Deployment":     {web.deployment, jobs.deployment},
		"Service":        {web.service},
		"ServiceAccount": {web.account, jobs.account},
	} {
		if got := kindNames(docs, kind); !sameSet(got, want) {
			t.Errorf("the %s objects are %v, want %v", kind, got, want)
		}
	}
	assertWorkloadReferences(t, docs, "web", web)
	assertWorkloadReferences(t, docs, "jobs", jobs)
}

var (
	workloadDefaultWeb  = workloadNames{deployment: "web", service: "web", account: "web"}
	workloadDefaultJobs = workloadNames{deployment: "jobs", account: "jobs"}
	workloadRenamedWeb  = workloadNames{deployment: "web-workload", service: "web-entry", account: "web-identity"}
	workloadRenamedJobs = workloadNames{deployment: "jobs-workload", account: "jobs-identity"}
)

// A hook that declines every name changes nothing: the output is the one
// without a hook, object for object. It is asked for each of the names with the
// component that owns it, in the order the rule emits the objects.
func TestWorkloadNames_DecliningHookChangesNothing(t *testing.T) {
	doc := workloadNamesDoc("", "")
	cluster, apps := namingTransform(t, doc, workloadNamesContext(nil))
	without, withoutDocs := generatedNames(cluster, apps), generatedDocs(t, apps)
	assertWorkloadObjects(t, withoutDocs, workloadDefaultWeb, workloadDefaultJobs)

	var requests []oam.NameRequest
	cluster, apps = namingTransform(t, doc, workloadNamesContext(declineEveryName(&requests)))
	if with := generatedNames(cluster, apps); !slices.Equal(without, with) {
		t.Errorf("a hook that declines every name changed the names:\nwithout: %s\nwith:    %s",
			strings.Join(without, "\n         "), strings.Join(with, "\n         "))
	}
	if withDocs := generatedDocs(t, apps); !reflect.DeepEqual(withoutDocs, withDocs) {
		t.Errorf("a hook that declines every name changed the generated objects")
	}

	want := []oam.NameRequest{
		{Application: "shop", Component: "web", Role: oam.NameRoleWorkloadDeployment, Kind: "Deployment.apps", Default: "web"},
		{Application: "shop", Component: "web", Role: oam.NameRoleWorkloadService, Kind: "Service", Default: "web"},
		{Application: "shop", Component: "web", Role: oam.NameRoleWorkloadServiceAccount, Kind: "ServiceAccount", Default: "web"},
		{Application: "shop", Component: "jobs", Role: oam.NameRoleWorkloadDeployment, Kind: "Deployment.apps", Default: "jobs"},
		{Application: "shop", Component: "jobs", Role: oam.NameRoleWorkloadServiceAccount, Kind: "ServiceAccount", Default: "jobs"},
	}
	if got := workloadRoleRequests(requests); !slices.Equal(got, want) {
		t.Errorf("the hook was asked\n  %+v\nwant\n  %+v", got, want)
	}
	// The members are the rule's to name: the hook is not asked for them under
	// the object role, as it is for an authored kind component.
	for _, req := range requests {
		if req.Role == oam.NameRoleObject {
			t.Errorf("the hook was asked for an object name: %+v", req)
		}
	}
}

// The hook names each object, every one of a component differently. The objects
// take the names, each reference follows the name its target got, and nothing
// else moves: the groups, the traits' objects and every other generated object
// are the ones without a hook.
func TestWorkloadNames_HookAnswerIsUsed(t *testing.T) {
	doc := workloadNamesDoc("", "")
	hook := renameBy(map[string]string{
		"workload-deployment web":      workloadRenamedWeb.deployment,
		"workload-service web":         workloadRenamedWeb.service,
		"workload-serviceaccount web":  workloadRenamedWeb.account,
		"workload-deployment jobs":     workloadRenamedJobs.deployment,
		"workload-serviceaccount jobs": workloadRenamedJobs.account,
	})
	cluster, apps := namingTransform(t, doc, workloadNamesContext(hook))
	assertWorkloadObjects(t, generatedDocs(t, apps), workloadRenamedWeb, workloadRenamedJobs)

	// Apart from the five renamed objects the list of generated names is the
	// one without a hook, application for application.
	plainCluster, plainApps := namingTransform(t, doc, workloadNamesContext(nil))
	want := generatedNames(plainCluster, plainApps)
	for i, line := range want {
		for from, to := range map[string]string{
			"web: Deployment default/web":       "web: Deployment default/" + workloadRenamedWeb.deployment,
			"web: Service default/web":          "web: Service default/" + workloadRenamedWeb.service,
			"web: ServiceAccount default/web":   "web: ServiceAccount default/" + workloadRenamedWeb.account,
			"jobs: Deployment default/jobs":     "jobs: Deployment default/" + workloadRenamedJobs.deployment,
			"jobs: ServiceAccount default/jobs": "jobs: ServiceAccount default/" + workloadRenamedJobs.account,
		} {
			if line == from {
				want[i] = to
			}
		}
	}
	if got := generatedNames(cluster, apps); !slices.Equal(got, want) {
		t.Errorf("generated names:\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// An authored name is used as written and the hook is not asked for it, through
// the whole build: kurel writes the objects under the authored names, with the
// references following.
func TestWorkloadNames_AuthoredWinsAndHookIsNotAsked(t *testing.T) {
	doc := workloadNamesDoc(
		"        deploymentObjectName: web-workload\n        serviceObjectName: web-entry\n        serviceAccountObjectName: web-identity\n",
		"        deploymentObjectName: jobs-workload\n        serviceAccountObjectName: jobs-identity\n")

	var requests []oam.NameRequest
	hook := func(req oam.NameRequest) (string, bool) {
		requests = append(requests, req)
		return "theirs", isWorkloadRole(req.Role)
	}
	_, apps := namingTransform(t, doc, workloadNamesContext(hook))
	if got := workloadRoleRequests(requests); len(got) != 0 {
		t.Errorf("the hook was asked for names the author set:\n  %+v", got)
	}
	assertWorkloadObjects(t, generatedDocs(t, apps), workloadRenamedWeb, workloadRenamedJobs)

	docs, err := buildWithProfile(t, doc, objectNameRouteProfile)
	if err != nil {
		t.Fatalf("kurel build: %v", err)
	}
	assertWorkloadObjects(t, docs, workloadRenamedWeb, workloadRenamedJobs)
}

// Each name is its own: naming one object leaves the other two of the component
// at the component name.
func TestWorkloadNames_OneNameAtATime(t *testing.T) {
	for _, tc := range []struct {
		name, webProps string
		web            workloadNames
	}{
		{"the Deployment", "        deploymentObjectName: web-workload\n", workloadNames{"web-workload", "web", "web"}},
		{"the Service", "        serviceObjectName: web-entry\n", workloadNames{"web", "web-entry", "web"}},
		{"the ServiceAccount", "        serviceAccountObjectName: web-identity\n", workloadNames{"web", "web", "web-identity"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, apps := namingTransform(t, workloadNamesDoc(tc.webProps, ""), workloadNamesContext(nil))
			assertWorkloadObjects(t, generatedDocs(t, apps), tc.web, workloadDefaultJobs)
		})
	}
}

// A route on another component names a Service, so a route to a webservice whose
// Service is renamed is written with the Service's name: it then resolves to the
// webservice, whose synthesized policy admits the traffic to its pods. Written
// with the component name it names a Service the document does not own, an
// external one, which gets no policy without a backendSelector. The address is
// the author's to change.
func TestWorkloadNames_RouteFromAnotherComponent(t *testing.T) {
	doc := func(backend string) string {
		return helmNamesApp(`    - name: web
      type: webservice
      properties:
        image: ghcr.io/example/web:v1.0.0
        port: 8080
        serviceObjectName: web-entry
    - name: front
      type: webservice
      properties:
        image: ghcr.io/example/front:v1.0.0
        port: 8080
      traits:
        - type: httproute
          properties:
            parentRefs:
              - name: gw
            rules:
              - backendRefs:
                  - name: ` + backend + `
                    port: 8080
`)
	}
	t.Run("by the Service's name it is the component", func(t *testing.T) {
		docs, err := buildWithProfile(t, doc("web-entry"), objectNameRouteProfile)
		if err != nil {
			t.Fatalf("kurel build: %v", err)
		}
		policy := namedDoc(t, docs, "NetworkPolicy", "web-allow-ingress-traffic")
		wantNestedString(t, policy, "web", "spec", "podSelector", "matchLabels", kurelComponentLabel)
		wantNestedString(t, policy, "web", "metadata", "labels", kurelComponentLabel)
	})
	t.Run("by the component name it is an external Service", func(t *testing.T) {
		docs, err := buildWithProfile(t, doc("web"), objectNameRouteProfile)
		if err != nil {
			t.Fatalf("kurel build: %v", err)
		}
		if got := kindNames(docs, "NetworkPolicy"); len(got) != 0 {
			t.Errorf("NetworkPolicy objects = %v, want none: the route names no Service of the document and gives no selector", got)
		}
	})
}

// With serviceAccountName the pods run as an existing account: the component
// generates none, the hook is not asked for one, and the pods and the rbac
// subject carry the authored name. The other names are still asked.
func TestWorkloadNames_ExistingAccountIsNotNamed(t *testing.T) {
	doc := workloadNamesDoc("        serviceAccountName: shared\n", "        serviceAccountName: shared\n")
	var requests []oam.NameRequest
	_, apps := namingTransform(t, doc, workloadNamesContext(declineEveryName(&requests)))
	want := []oam.NameRequest{
		{Application: "shop", Component: "web", Role: oam.NameRoleWorkloadDeployment, Kind: "Deployment.apps", Default: "web"},
		{Application: "shop", Component: "web", Role: oam.NameRoleWorkloadService, Kind: "Service", Default: "web"},
		{Application: "shop", Component: "jobs", Role: oam.NameRoleWorkloadDeployment, Kind: "Deployment.apps", Default: "jobs"},
	}
	if got := workloadRoleRequests(requests); !slices.Equal(got, want) {
		t.Errorf("the hook was asked\n  %+v\nwant\n  %+v", got, want)
	}
	docs := generatedDocs(t, apps)
	if got := kindNames(docs, "ServiceAccount"); len(got) != 0 {
		t.Errorf("ServiceAccount objects = %v, want none beside serviceAccountName", got)
	}
	for _, component := range []string{"web", "jobs"} {
		wantNestedString(t, namedDoc(t, docs, "Deployment", component), "shared", "spec", "template", "spec", "serviceAccountName")
		subjects, _, _ := unstructured.NestedSlice(namedDoc(t, docs, "RoleBinding", component), "subjects")
		if len(subjects) != 1 {
			t.Fatalf("RoleBinding %s subjects = %d, want 1", component, len(subjects))
		}
		wantNestedString(t, subjects[0].(map[string]any), "shared", "name")
	}
}

func TestWorkloadNames_Refusals(t *testing.T) {
	// An authored kind component of typ whose object is named name.
	kind := func(typ, name, props string) string {
		return `    - name: other
      type: ` + typ + `
      properties:
        objectName: ` + name + `
` + props
	}
	const servicePorts = "        selector:\n          app: elsewhere\n        ports:\n          - name: http\n            port: 80\n"
	workload := func(name, typ, props string) string {
		port := ""
		if typ == "webservice" {
			port = "        port: 8080\n"
		}
		return `    - name: ` + name + `
      type: ` + typ + `
      properties:
        image: ghcr.io/example/app:v1.0.0
` + port + props
	}
	answer := func(role oam.NameRole, name string) oam.TransformContext {
		return oam.TransformContext{Naming: func(req oam.NameRequest) (string, bool) { return name, req.Role == role }}
	}
	for _, tc := range []struct {
		name, doc string
		ctx       oam.TransformContext
		want      string
	}{
		{
			name: "serviceAccountObjectName beside serviceAccountName",
			doc:  helmNamesApp(workload("web", "webservice", "        serviceAccountName: shared\n        serviceAccountObjectName: web-identity\n")),
			want: `serviceAccountObjectName and serviceAccountName are both set: serviceAccountName names an existing account, ` +
				`so the component generates none for serviceAccountObjectName to name; remove one of them`,
		},
		{
			name: "an authored Service name that is no DNS-1035 label",
			doc:  helmNamesApp(workload("web", "webservice", "        serviceObjectName: web.v1\n")),
			want: `naming the Service: serviceObjectName "web.v1" cannot be the name for role "workload-service": not a valid DNS-1035 label: `,
		},
		{
			name: "a hook answer for the Service that is no DNS-1035 label",
			doc:  helmNamesApp(workload("web", "webservice", "")), ctx: answer(oam.NameRoleWorkloadService, "1web"),
			want: `naming the Service: the Naming hook returned "1web" for role "workload-service" in place of "web": not a valid DNS-1035 label: `,
		},
		{
			name: "a hook answer for the Deployment that is no subdomain",
			doc:  helmNamesApp(workload("jobs", "worker", "")), ctx: answer(oam.NameRoleWorkloadDeployment, "Not_A_Name"),
			want: `naming the Deployment: the Naming hook returned "Not_A_Name" for role "workload-deployment" in place of "jobs": not a valid DNS-1123 subdomain: `,
		},
		{
			name: "a hook answer for the ServiceAccount over 253 characters",
			doc:  helmNamesApp(workload("jobs", "worker", "")), ctx: answer(oam.NameRoleWorkloadServiceAccount, strings.Repeat("a", 254)),
			want: `for role "workload-serviceaccount" in place of "jobs": not a valid DNS-1123 subdomain: must be no more than 253`,
		},
		{
			name: "two Services given one name",
			doc: helmNamesApp(workload("web", "webservice", "        serviceObjectName: entry\n") +
				workload("api", "webservice", "        serviceObjectName: entry\n")),
			want: `naming the Service: name collision: Service "entry" is named by ` +
				`component "web" (role "workload-service", set by serviceObjectName) and by ` +
				`component "api" (role "workload-service", set by serviceObjectName); give one of them another name`,
		},
		{
			name: "a webservice and a worker given one Deployment name",
			doc: helmNamesApp(workload("web", "webservice", "        deploymentObjectName: workload\n") +
				workload("jobs", "worker", "        deploymentObjectName: workload\n")),
			want: `naming the Deployment: name collision: Deployment.apps "workload" is named by ` +
				`component "web" (role "workload-deployment", set by deploymentObjectName) and by ` +
				`component "jobs" (role "workload-deployment", set by deploymentObjectName); give one of them another name`,
		},
		{
			name: "one answer for every ServiceAccount",
			doc:  helmNamesApp(workload("web", "webservice", "") + workload("jobs", "worker", "")), ctx: answer(oam.NameRoleWorkloadServiceAccount, "identity"),
			want: `naming the ServiceAccount: name collision: ServiceAccount "identity" is named by ` +
				`component "web" (role "workload-serviceaccount", returned by the Naming hook in place of "web") and by ` +
				`component "jobs" (role "workload-serviceaccount", returned by the Naming hook in place of "jobs"); give one of them another name`,
		},
		{
			name: "one component's Deployment named as another's default",
			doc: helmNamesApp(workload("web", "webservice", "        deploymentObjectName: jobs\n") +
				workload("jobs", "worker", "")),
			want: `naming the Deployment: name collision: Deployment.apps "jobs" is named by ` +
				`component "web" (role "workload-deployment", set by deploymentObjectName) and by ` +
				`component "jobs" (role "workload-deployment", its default); give one of them another name`,
		},
		{
			name: "an authored Service named as the webservice's",
			doc:  helmNamesApp(workload("web", "webservice", "        serviceObjectName: entry\n") + kind("service", "entry", servicePorts)),
			want: `component "other": name collision: Service "default/entry" is named by ` +
				`component "web" (role "workload-service", set by serviceObjectName) and by ` +
				`component "other" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "an authored ServiceAccount named as the worker's default",
			doc:  helmNamesApp(workload("jobs", "worker", "") + kind("serviceaccount", "jobs", "")),
			want: `component "other": name collision: ServiceAccount "default/jobs" is named by ` +
				`component "jobs" (role "workload-serviceaccount", its default) and by ` +
				`component "other" (role "object", set by properties.objectName); give one of them another name`,
		},
		{
			name: "an authored Deployment named as the webservice's",
			doc: helmNamesApp(workload("web", "webservice", "        deploymentObjectName: workload\n") +
				kind("deployment", "workload", "        image: ghcr.io/example/app:v1.0.0\n")),
			want: `component "other": name collision: Deployment.apps "default/workload" is named by ` +
				`component "web" (role "workload-deployment", set by deploymentObjectName) and by ` +
				`component "other" (role "object", set by properties.objectName); give one of them another name`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := transformErr(t, tc.doc, tc.ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %s", err, tc.want)
			}
		})
	}
}
