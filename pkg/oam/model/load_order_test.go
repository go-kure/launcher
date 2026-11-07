package model

import (
	"strings"
	"testing"
)

// orderFault is one defect that a model document can carry. want is a text that the
// error of that defect holds and the error of no other defect in the list holds.
type orderFault struct {
	name string
	from string
	to   string
	want string
}

// orderFaults lists the checks of Load in the order that the README and the Load doc
// comment give: the apiVersion and kind checks, the null and duplicate-key checks, the
// metadata, spec.celEnv, the inputs, the values and the parts.
var orderFaults = []orderFault{
	{"apiVersion", "apiVersion: launcher.gokure.dev/v1alpha1", "apiVersion: nope", "apiVersion"},
	{"kind", "kind: Model\n", "kind: Package\n", "kind"},
	{"null entry", "    image:\n      type: string\n      required: true\n", "    image:\n      types: [string, null]\n", "spec.inputs.image.types[1]"},
	{"metadata", "name: webapp.acme.example", "name: webapp", "metadata.name"},
	{"celEnv", "celEnv: \"1\"", "celEnv: \"2\"", "spec.celEnv"},
	{"input", "    replicas:\n      type: integer\n", "    replicas:\n      type: bogus\n", "spec.inputs.replicas"},
	{"value", "    - name: full\n", "    - name: 1full\n", "spec.values[0]"},
	{"part", "    - id: app\n", "    - id: 1app\n", "spec.parts[0]"},
}

func withFaults(t *testing.T, faults ...orderFault) []byte {
	t.Helper()
	doc := validModel
	for _, f := range faults {
		if !strings.Contains(doc, f.from) {
			t.Fatalf("test bug: %q is not in the document", f.from)
		}
		doc = strings.Replace(doc, f.from, f.to, 1)
	}
	return []byte(doc)
}

// Load returns the first failure in the order that the README gives. Each defect alone
// must fail with its own text (the control: it shows that Load detects that defect).
// Then each defect, written together with the next one in the list, must be the one
// that Load reports. The next defect never shows in that error.
func TestLoad_ChecksInTheDocumentedOrder(t *testing.T) {
	for _, f := range orderFaults {
		t.Run("alone/"+f.name, func(t *testing.T) {
			_, err := loadNoPanic(withFaults(t, f))
			if err == nil || !strings.Contains(err.Error(), f.want) {
				t.Fatalf("a document with only the %s defect must fail with %q: %v", f.name, f.want, err)
			}
		})
	}
	for i := 0; i+1 < len(orderFaults); i++ {
		first, second := orderFaults[i], orderFaults[i+1]
		t.Run(first.name+" before "+second.name, func(t *testing.T) {
			_, err := loadNoPanic(withFaults(t, second, first))
			if err == nil {
				t.Fatal("Load accepted a document with two defects")
			}
			if !strings.Contains(err.Error(), first.want) || strings.Contains(err.Error(), second.want) {
				t.Fatalf("the first failure must name %q and not %q: %v", first.want, second.want, err)
			}
		})
	}
}
