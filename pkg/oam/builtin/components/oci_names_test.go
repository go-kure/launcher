package components_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// These tests pin the names an oci component gives its Kustomization and the
// source it keeps to itself (go-kure/launcher#787), on the rule driven
// directly: kustomizationName and source.objectName name the objects, the
// members keep the component's name, and the Kustomization's sourceRef follows
// the source's object name.

// ociNamed is validOCIProps with the given source.objectName and
// kustomizationName; an empty one is left out.
func ociNamed(sourceObjectName, kustomizationName string) map[string]any {
	props := validOCIProps()
	if sourceObjectName != "" {
		props["source"].(map[string]any)["objectName"] = sourceObjectName
	}
	if kustomizationName != "" {
		props["kustomizationName"] = kustomizationName
	}
	return props
}

func TestOCIRule_AuthoredObjectNames(t *testing.T) {
	for _, tt := range []struct {
		name                                string
		props                               map[string]any
		wantSource, wantKust, wantSourceRef string
	}{
		{"neither", ociNamed("", ""), "checkout", "checkout", "checkout"},
		{"the source", ociNamed("artifact", ""), "artifact", "checkout", "artifact"},
		{"the Kustomization", ociNamed("", "delivery"), "checkout", "delivery", "checkout"},
		{"both", ociNamed("artifact", "delivery"), "artifact", "delivery", "artifact"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for name, comps := range map[string][]oam.Component{
				"in a document":      lowerOCI(t, ociLowering("shop"), ociDocument(*ociComponent(tt.props)), "checkout"),
				"without a document": mustLowerOCIAlone(t, ociComponent(tt.props)),
			} {
				if len(comps) != 2 || comps[0].Type != "ocirepository" || comps[1].Type != "fluxcd-kustomization" {
					t.Fatalf("%s: lowered to %+v, want the source and the Kustomization", name, comps)
				}
				source, kust := comps[0], comps[1]
				if source.Name != "checkout" || kust.Name != "checkout" {
					t.Errorf("%s: members are named %q and %q, want both to keep the component's name", name, source.Name, kust.Name)
				}
				if got := source.ObjectName(); got != tt.wantSource {
					t.Errorf("%s: the source's object is named %q, want %q", name, got, tt.wantSource)
				}
				if got := kust.ObjectName(); got != tt.wantKust {
					t.Errorf("%s: the Kustomization's object is named %q, want %q", name, got, tt.wantKust)
				}
				// The reference follows the source's object, and neither property
				// reaches a member.
				wantSourceProps := map[string]any{"url": "oci://registry.example.com/charts/checkout", "ref": map[string]any{"tag": "0.3.0"}}
				if !reflect.DeepEqual(source.Properties, wantSourceProps) {
					t.Errorf("%s: the source's properties are %v, want %v", name, source.Properties, wantSourceProps)
				}
				wantKustProps := map[string]any{"path": "./", "prune": true, "sourceRef": map[string]any{"kind": "OCIRepository", "name": tt.wantSourceRef}}
				if !reflect.DeepEqual(kust.Properties, wantKustProps) {
					t.Errorf("%s: the Kustomization's properties are %v, want %v", name, kust.Properties, wantKustProps)
				}
			}
		})
	}
}

// A component that writes source.objectName keeps its source whatever other
// component has the same artifact, and is no consumer of a shared one: of three
// components on one artifact the two that name nothing share a source, and the
// third keeps its own, with its annotations.
func TestOCIRule_SourceObjectNameKeepsTheSource(t *testing.T) {
	const url = "oci://registry.example.com/org/platform"
	named := ociProps(url, "1.4.0", "")
	named["source"].(map[string]any)["objectName"] = "platform-base"
	app := ociDocument(
		oam.Component{Name: "base", Type: "oci", Properties: named, Annotations: map[string]string{"gokure.dev/tier": "infra"}},
		oam.Component{Name: "addons", Type: "oci", Properties: ociProps(url, "1.4.0", "")},
		oam.Component{Name: "extras", Type: "oci", Properties: ociProps(url, "1.4.0", "")},
	)
	shared := helmSourceName("shop", ociSourceIdentity(url, "1.4.0", "1h0m0s"))
	lctx := ociLowering("shop")

	base := lowerOCI(t, lctx, app, "base")
	if len(base) != 2 || base[0].Type != "ocirepository" || base[0].Name != "base" || base[0].ObjectName() != "platform-base" {
		t.Fatalf("base lowered to %+v, want its own source, named platform-base", base)
	}
	if base[0].Annotations["gokure.dev/tier"] != "infra" {
		t.Errorf("base's source carries annotations %v, want the component's", base[0].Annotations)
	}
	if ref := base[1].Properties["sourceRef"].(map[string]any)["name"]; ref != "platform-base" {
		t.Errorf("base's Kustomization reads source %v, want platform-base", ref)
	}

	addons := lowerOCI(t, lctx, app, "addons")
	if len(addons) != 2 || addons[0].Name != shared || addons[1].Properties["sourceRef"].(map[string]any)["name"] != shared {
		t.Fatalf("addons lowered to %+v, want the shared source %s and a Kustomization reading it", addons, shared)
	}
	extras := lowerOCI(t, lctx, app, "extras")
	if len(extras) != 1 || extras[0].Properties["sourceRef"].(map[string]any)["name"] != shared {
		t.Fatalf("extras lowered to %+v, want only a Kustomization reading the shared source %s", extras, shared)
	}

	// With base out of the count, one unnamed component is alone on the artifact
	// and keeps its own source.
	pair := ociDocument(app.Spec.Components[0], app.Spec.Components[1])
	alone := lowerOCI(t, ociLowering("shop"), pair, "addons")
	if len(alone) != 2 || alone[0].Name != "addons" || alone[0].ObjectName() != "addons" {
		t.Fatalf("addons, alone beside a component that names its source, lowered to %+v, want its own source", alone)
	}
}

func TestOCIRule_ObjectNameRefusals(t *testing.T) {
	both := ociNamed("artifact", "")
	both["source"].(map[string]any)["name"] = "artifact-source"
	notAString := validOCIProps()
	notAString["source"].(map[string]any)["objectName"] = 5
	kustNotAString := validOCIProps()
	kustNotAString["kustomizationName"] = []any{"a"}
	emptySource := validOCIProps()
	emptySource["source"].(map[string]any)["objectName"] = ""
	emptyKust := validOCIProps()
	emptyKust["kustomizationName"] = ""

	for _, tt := range []struct {
		name  string
		props map[string]any
		want  string
	}{
		{"source.objectName beside source.name", both,
			"oci: source.objectName and source.name are both set, and each names the OCIRepository in another way: " +
				"write source.objectName to rename the source the component keeps to itself, which stays with the Kustomization and carries the component's annotations and its prune-protection and force-replace traits; " +
				"write source.name for a source generated on its own, which components that write the same name share"},
		{"source.objectName that is no string", notAString, "oci: source.objectName"},
		{"kustomizationName that is no string", kustNotAString, "oci: kustomizationName"},
		{"an empty source.objectName", emptySource,
			`oci: naming the source: source.objectName "" cannot be the name for role "oci-source": it is empty; write a valid name, or leave the property out for the default "checkout"`},
		{"an empty kustomizationName", emptyKust,
			`oci: naming the Kustomization: kustomizationName "" cannot be the name for role "oci-kustomization": it is empty; write a valid name, or leave the property out for the default "checkout"`},
		{"a source.objectName that is no subdomain", ociNamed("Not_A_Name", ""),
			`oci: naming the source: source.objectName "Not_A_Name" cannot be the name for role "oci-source": not a valid DNS-1123 subdomain: `},
		{"a kustomizationName over 253 characters", ociNamed("", strings.Repeat("k", 254)),
			`cannot be the name for role "oci-kustomization": not a valid DNS-1123 subdomain: must be no more than 253`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := components.OCIRule{}.LowerComponent(ociComponent(tt.props), ociLowering("shop"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tt.want)
			}
		})
	}
}

// The two properties are in the schema, so a document that writes them passes
// property validation.
func TestOCIRule_SchemaDeclaresTheNameProperties(t *testing.T) {
	schema := components.OCIRule{}.PropertySchema()
	if _, ok := schema["kustomizationName"]; !ok {
		t.Error("the oci schema does not declare kustomizationName")
	}
	source := schema["source"]
	for _, key := range []string{"name", "objectName"} {
		if _, ok := source.Properties[key]; !ok {
			t.Errorf("the oci schema does not declare source.%s", key)
		}
	}
}
