package components

import (
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestContainerEntrySchemaMatchesParser pins the published key set of one
// `initContainers` and one `sidecars` entry to the key sets their parsers hand
// rejectUnknownKeys (go-kure/launcher#321), and checks no key an init container
// refuses is advertised for it. A key published but never parsed, or parsed
// but never published, leaves both halves internally consistent otherwise.
func TestContainerEntrySchemaMatchesParser(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema oam.PropertySchema
		want   []string
	}{
		{"initContainers", schemaInitContainers(), initContainerPropertyKeys},
		{"sidecars", schemaSidecars(), sidecarPropertyKeys},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := tc.schema.Items
			if item == nil {
				t.Fatal("no Items")
			}
			if item.AdditionalProperties {
				t.Error("entry schema is open (AdditionalProperties: true)")
			}
			got := make([]string, 0, len(item.Properties))
			for k := range item.Properties {
				got = append(got, k)
			}
			slices.Sort(got)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("entry keys = %v\nparser accepts %v", got, want)
			}
		})
	}
	initItem := schemaInitContainers().Items
	for k := range initContainerRejectedKeys {
		if _, ok := initItem.Properties[k]; ok {
			t.Errorf("initContainers schema publishes rejected key %q", k)
		}
		if slices.Contains(initContainerPropertyKeys, k) {
			t.Errorf("initContainerPropertyKeys accepts rejected key %q", k)
		}
		if !slices.Contains(sidecarPropertyKeys, k) {
			t.Errorf("rejected init key %q is not a sidecar key either; the explanation would point authors at a sidecar that refuses it too", k)
		}
	}
}

// TestContainerEntrySchema_EveryKeyDescribed: every property, nested property
// and array item of both entry schemas carries a Description.
func TestContainerEntrySchema_EveryKeyDescribed(t *testing.T) {
	var walk func(path string, s oam.PropertySchema)
	walk = func(path string, s oam.PropertySchema) {
		if s.Description == "" {
			t.Errorf("%s: no Description", path)
		}
		for k, p := range s.Properties {
			walk(path+"."+k, p)
		}
		if s.Items != nil {
			walk(path+".[]", *s.Items)
		}
	}
	walk("initContainers", schemaInitContainers())
	walk("sidecars", schemaSidecars())
}
