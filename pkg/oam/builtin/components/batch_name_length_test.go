package components_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// batchNameLimits are the two batch kinds whose object the API server refuses
// over a length no component name is held to: a CronJob over 52 characters, a
// Job over 63.
var batchNameLimits = []struct {
	typ, kind string
	limit     int
	handler   oam.ComponentHandler
	props     map[string]any
	// named returns the kind's config carrying objectName.
	named func(objectName string) stack.ApplicationConfig
}{
	{
		typ: "cronjob", kind: "CronJob", limit: 52, handler: &components.CronjobHandler{},
		props: map[string]any{"image": "busybox:1", "schedule": "0 2 * * *"},
		named: func(objectName string) stack.ApplicationConfig {
			return &components.CronjobConfig{Name: "nightly", ObjectName: objectName, Image: "busybox:1", Schedule: "0 2 * * *"}
		},
	},
	{
		typ: "job", kind: "Job", limit: 63, handler: &components.JobHandler{},
		props: map[string]any{"image": "busybox:1"},
		named: func(objectName string) stack.ApplicationConfig {
			return &components.JobConfig{Name: "nightly", ObjectName: objectName, Image: "busybox:1"}
		},
	},
}

// TestBatchKinds_ComponentNameLength: the component name is the object's name,
// so a `cronjob` or `job` component named past its kind's limit is refused when
// the component is read, and the longest name the kind takes builds. The
// refusal names the component.
func TestBatchKinds_ComponentNameLength(t *testing.T) {
	for _, tc := range batchNameLimits {
		t.Run(tc.typ, func(t *testing.T) {
			longest := strings.Repeat("a", tc.limit)
			cfg, err := tc.handler.ToApplicationConfig(&oam.Component{Name: longest, Type: tc.typ, Properties: tc.props}, "default")
			if err != nil {
				t.Fatalf("a %d-character name: err = %v, want it accepted", tc.limit, err)
			}
			objs, err := cfg.Generate(stack.NewApplication(longest, "default", cfg))
			if err != nil || len(objs) != 1 || (*objs[0]).GetName() != longest {
				t.Fatalf("a %d-character name: objects %v, err %v; want the one %s under it", tc.limit, objs, err, tc.kind)
			}

			tooLong := longest + "a"
			want := tc.typ + ` "` + tooLong + `": the component name is the ` + tc.kind + `'s name, which must be at most ` + strconv.Itoa(tc.limit) + ` characters`
			_, err = tc.handler.ToApplicationConfig(&oam.Component{Name: tooLong, Type: tc.typ, Properties: tc.props}, "default")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("a %d-character name: err = %v\nwant one containing %q", tc.limit+1, err, want)
			}
		})
	}
}

// TestBatchKinds_GenerateRepeatsNameLength: the configs are exported, so
// Generate cannot rely on ToApplicationConfig having run. It holds the name the
// object takes to the limit, the Application's or the config's ObjectName, and
// says which of the two it was.
func TestBatchKinds_GenerateRepeatsNameLength(t *testing.T) {
	const source = oam.ObjectNameProperty + ` (or the Naming hook's answer for role "object"): `
	for _, tc := range batchNameLimits {
		tooLong := strings.Repeat("a", tc.limit+1)
		t.Run(tc.typ+" named after the application", func(t *testing.T) {
			cfg := tc.named("")
			want := tc.typ + ` "` + tooLong + `": the component name is the ` + tc.kind + `'s name, which must be at most ` + strconv.Itoa(tc.limit) + ` characters`
			_, err := cfg.Generate(stack.NewApplication(tooLong, "default", cfg))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v\nwant one containing %q", err, want)
			}
		})
		t.Run(tc.typ+" named by its ObjectName", func(t *testing.T) {
			cfg := tc.named(tooLong)
			want := source + `"` + tooLong + `" is not a valid ` + tc.kind + ` name, which must be at most ` + strconv.Itoa(tc.limit) + ` characters`
			_, err := cfg.Generate(stack.NewApplication("nightly", "default", cfg))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v\nwant one containing %q", err, want)
			}
			longest := tc.named(strings.Repeat("a", tc.limit))
			if _, err := longest.Generate(stack.NewApplication("nightly", "default", longest)); err != nil {
				t.Fatalf("an ObjectName of %d characters: err = %v, want it accepted", tc.limit, err)
			}
		})
	}
}
