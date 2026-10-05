package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests pin the names a webservice and a worker give the objects they
// generate (go-kure/launcher#787), on the rules driven directly:
// deploymentObjectName, serviceObjectName and serviceAccountObjectName name the
// objects, the members keep the component's name, and the pods'
// serviceAccountName follows the account's object name.

// roleNamed is a role component's properties with the given object names; an
// empty one is left out.
func roleNamed(deployment, service, account string) map[string]any {
	props := map[string]any{"image": "ghcr.io/org/app:v1"}
	for key, name := range map[string]string{
		"deploymentObjectName":     deployment,
		"serviceObjectName":        service,
		"serviceAccountObjectName": account,
	} {
		if name != "" {
			props[key] = name
		}
	}
	return props
}

// lowerRole runs the rule of typ on a component named "app" with props.
func lowerRole(typ string, props map[string]any) ([]oam.Component, error) {
	comp := &oam.Component{Name: "app", Type: typ, Properties: props}
	var res oam.LoweringResult
	var err error
	if typ == "webservice" {
		res, err = components.WebserviceRule{}.LowerComponent(comp, oam.LoweringContext{})
	} else {
		res, err = components.WorkerRule{}.LowerComponent(comp, oam.LoweringContext{})
	}
	return res.Components, err
}

func TestRoleRules_AuthoredObjectNames(t *testing.T) {
	for _, tt := range []struct {
		name                                     string
		deployment, service, account             string
		wantDeployment, wantService, wantAccount string
	}{
		{name: "none", wantDeployment: "app", wantService: "app", wantAccount: "app"},
		{name: "the Deployment", deployment: "app-workload", wantDeployment: "app-workload", wantService: "app", wantAccount: "app"},
		{name: "the Service", service: "app-entry", wantDeployment: "app", wantService: "app-entry", wantAccount: "app"},
		{name: "the ServiceAccount", account: "app-identity", wantDeployment: "app", wantService: "app", wantAccount: "app-identity"},
		{name: "all three", deployment: "app-workload", service: "app-entry", account: "app-identity",
			wantDeployment: "app-workload", wantService: "app-entry", wantAccount: "app-identity"},
	} {
		for _, typ := range []string{"webservice", "worker"} {
			if typ == "worker" && tt.service != "" {
				// A worker generates no Service and declares no name for one.
				continue
			}
			t.Run(tt.name+"/"+typ, func(t *testing.T) {
				members, err := lowerRole(typ, roleNamed(tt.deployment, tt.service, tt.account))
				if err != nil {
					t.Fatalf("LowerComponent: %v", err)
				}
				want := map[string]string{"deployment": tt.wantDeployment, "serviceaccount": tt.wantAccount}
				if typ == "webservice" {
					want["service"] = tt.wantService
				}
				if len(members) != len(want) {
					t.Fatalf("lowered to %d members, want %d: %+v", len(members), len(want), members)
				}
				for _, m := range members {
					if m.Name != "app" {
						t.Errorf("the %s member is named %q, want it to keep the component's name", m.Type, m.Name)
					}
					if got := m.ObjectName(); got != want[m.Type] {
						t.Errorf("the %s member's object is named %q, want %q", m.Type, got, want[m.Type])
					}
				}
				// The pods run as the account by its object's name, and no name
				// property reaches the deployment member.
				dep := members[0]
				if dep.Type != "deployment" {
					t.Fatalf("the first member is a %s, want the deployment", dep.Type)
				}
				if got := dep.Properties["serviceAccountName"]; got != tt.wantAccount {
					t.Errorf("the deployment member's serviceAccountName is %v, want %q", got, tt.wantAccount)
				}
				for _, key := range []string{"deploymentObjectName", "serviceObjectName", "serviceAccountObjectName"} {
					if _, forwarded := dep.Properties[key]; forwarded {
						t.Errorf("%s reached the deployment member", key)
					}
				}
			})
		}
	}
}

// With serviceAccountName the pods run as an existing account: no account is
// generated, so there is none to name, and the other names still apply.
func TestRoleRules_ExistingAccountIsNotNamed(t *testing.T) {
	for _, typ := range []string{"webservice", "worker"} {
		t.Run(typ, func(t *testing.T) {
			props := roleNamed("app-workload", "", "")
			props["serviceAccountName"] = "shared"
			members, err := lowerRole(typ, props)
			if err != nil {
				t.Fatalf("LowerComponent: %v", err)
			}
			for _, m := range members {
				if m.Type == "serviceaccount" {
					t.Fatalf("a serviceaccount member was emitted beside serviceAccountName: %+v", members)
				}
			}
			if got := members[0].ObjectName(); got != "app-workload" {
				t.Errorf("the Deployment is named %q, want app-workload", got)
			}
			if got := members[0].Properties["serviceAccountName"]; got != "shared" {
				t.Errorf("the deployment member's serviceAccountName is %v, want the authored shared", got)
			}

			props["serviceAccountObjectName"] = "app-identity"
			_, err = lowerRole(typ, props)
			const want = "serviceAccountObjectName and serviceAccountName are both set: serviceAccountName names an existing account, " +
				"so the component generates none for serviceAccountObjectName to name; remove one of them"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v\nwant one containing %q", err, want)
			}
		})
	}
}

func TestRoleRules_ObjectNameRefusals(t *testing.T) {
	with := func(key string, value any) map[string]any {
		props := roleNamed("", "", "")
		props[key] = value
		return props
	}
	for _, tt := range []struct {
		name  string
		typ   string
		props map[string]any
		want  string
	}{
		{"a deploymentObjectName that is no string", "worker", with("deploymentObjectName", 5),
			"deploymentObjectName: must be a string, got int"},
		{"a serviceObjectName that is no string", "webservice", with("serviceObjectName", []any{"a"}),
			"serviceObjectName: must be a string, got []interface {}"},
		{"a serviceAccountObjectName that is no string", "worker", with("serviceAccountObjectName", true),
			"serviceAccountObjectName: must be a string, got bool"},
		{"an empty deploymentObjectName", "webservice", with("deploymentObjectName", ""),
			`naming the Deployment: deploymentObjectName "" cannot be the name for role "workload-deployment": it is empty; write a valid name, or leave the property out for the default "app"`},
		{"an empty serviceAccountObjectName", "worker", with("serviceAccountObjectName", ""),
			`naming the ServiceAccount: serviceAccountObjectName "" cannot be the name for role "workload-serviceaccount": it is empty; write a valid name, or leave the property out for the default "app"`},
		{"a deploymentObjectName that is no subdomain", "worker", with("deploymentObjectName", "Not_A_Name"),
			`naming the Deployment: deploymentObjectName "Not_A_Name" cannot be the name for role "workload-deployment": not a valid DNS-1123 subdomain: `},
		{"a serviceObjectName that is a subdomain but no label", "webservice", with("serviceObjectName", "app.v1"),
			`naming the Service: serviceObjectName "app.v1" cannot be the name for role "workload-service": not a valid DNS-1035 label: `},
		{"a serviceObjectName over 63 characters", "webservice", with("serviceObjectName", strings.Repeat("s", 64)),
			`cannot be the name for role "workload-service": not a valid DNS-1035 label: must be no more than 63`},
		{"a serviceAccountObjectName over 253 characters", "webservice", with("serviceAccountObjectName", strings.Repeat("a", 254)),
			`cannot be the name for role "workload-serviceaccount": not a valid DNS-1123 subdomain: must be no more than 253`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lowerRole(tt.typ, tt.props)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tt.want)
			}
		})
	}
}

// An explicit null is no name: the object keeps the component's.
func TestRoleRules_NullObjectNameIsNoName(t *testing.T) {
	props := roleNamed("", "", "")
	props["deploymentObjectName"] = nil
	props["serviceObjectName"] = nil
	props["serviceAccountObjectName"] = nil
	members, err := lowerRole("webservice", props)
	if err != nil {
		t.Fatalf("LowerComponent: %v", err)
	}
	for _, m := range members {
		if got := m.ObjectName(); got != "app" {
			t.Errorf("the %s member's object is named %q, want app", m.Type, got)
		}
	}
}

// The properties are in the schemas, so a document that writes them passes
// property validation. A worker generates no Service and declares no name for
// one.
func TestRoleRules_SchemaDeclaresTheNameProperties(t *testing.T) {
	web, worker := components.WebserviceRule{}.PropertySchema(), components.WorkerRule{}.PropertySchema()
	for _, key := range []string{"deploymentObjectName", "serviceObjectName", "serviceAccountObjectName"} {
		if _, ok := web[key]; !ok {
			t.Errorf("the webservice schema does not declare %s", key)
		}
	}
	for _, key := range []string{"deploymentObjectName", "serviceAccountObjectName"} {
		if _, ok := worker[key]; !ok {
			t.Errorf("the worker schema does not declare %s", key)
		}
	}
	if _, ok := worker["serviceObjectName"]; ok {
		t.Error("the worker schema declares serviceObjectName, and a worker generates no Service")
	}
}
