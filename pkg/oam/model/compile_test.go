package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// celBase returns a valid model with one part and six inputs that cover the schema
// shapes the CEL environment types: a required string, an integer with a default, an
// optional string, an optional list, an object with free keys, and an object that
// holds a list.
func celBase(t *testing.T) *Model {
	t.Helper()
	m := &Model{
		APIVersion: "launcher.gokure.dev/v1alpha1",
		Kind:       Kind,
		Metadata:   Metadata{Name: "webapp.acme.example", Version: "v2"},
		Spec: Spec{
			CELEnv: CELEnvV1,
			Inputs: map[string]oam.PropertySchema{
				"image":    {Type: oam.PropertyTypeString, Required: true},
				"replicas": {Type: oam.PropertyTypeInteger, Default: 2},
				"nick":     {Type: oam.PropertyTypeString},
				"tags":     {Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeString}},
				"labels":   {Type: oam.PropertyTypeObject, AdditionalProperties: true},
				"config": {Type: oam.PropertyTypeObject, Properties: map[string]oam.PropertySchema{
					"items": {Type: oam.PropertyTypeArray, Items: &oam.PropertySchema{Type: oam.PropertyTypeString}},
				}},
			},
			Parts: []Part{{ID: "app", Type: "deployment.apps@v1"}},
		},
	}
	if err := validate(m); err != nil {
		t.Fatalf("test bug: the base model does not load: %v", err)
	}
	return m
}

func oneValue(t *testing.T, expr string) *Model {
	t.Helper()
	m := celBase(t)
	m.Spec.Values = []Value{{Name: "v", Expr: expr}}
	return m
}

func partWhen(t *testing.T, expr string) *Model {
	t.Helper()
	m := celBase(t)
	m.Spec.Parts[0].When = expr
	return m
}

func partIn(t *testing.T, expr string) *Model {
	t.Helper()
	m := celBase(t)
	m.Spec.Parts[0].ForEach = &ForEach{As: "item", In: expr}
	return m
}

// diagnosticText returns the text of an error without the source lines that CEL
// echoes under each issue (` | <expression>` and the caret line). The echo holds the
// expression the test wrote, so a cause that appears in the expression would match it
// even when the refusal came from another rule.
func diagnosticText(err error) string {
	var keep []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

// wantRefusal compiles m and requires a failure that wraps ErrInvalid, names the
// path, and names the cause. A bare "it failed" passes on a stub that refuses
// everything, so every refusal test names both. The cause is read in the diagnostic
// text, never in the echoed source line.
func wantRefusal(t *testing.T, m *Model, path, cause string) {
	t.Helper()
	cm, err := compileModel(m)
	if err == nil {
		t.Fatalf("compileModel accepted the model: %+v", cm)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("a compile failure must wrap ErrInvalid: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path %q", err, path)
	}
	if !strings.Contains(diagnosticText(err), cause) {
		t.Errorf("error %q does not name the cause %q", err, cause)
	}
}

// The premise of diagnosticText: CEL echoes the expression under its issue, and the
// helper drops that echo. Without the first half the helper would fix nothing, and
// without the second a cause could still match the echo.
func TestDiagnosticTextDropsTheSourceEcho(t *testing.T) {
	_, err := compileModel(oneValue(t, "${inputs.nope + dyn(1)}"))
	if err == nil {
		t.Fatal("test bug: an undeclared input compiled")
	}
	if !strings.Contains(err.Error(), "dyn(1)") {
		t.Fatalf("premise failed: the error does not echo the expression: %q", err)
	}
	if strings.Contains(diagnosticText(err), "dyn(1)") {
		t.Errorf("diagnosticText kept the echo: %q", diagnosticText(err))
	}
	if !strings.Contains(diagnosticText(err), "undeclared reference") {
		t.Errorf("diagnosticText lost the diagnostic: %q", diagnosticText(err))
	}
}

func wantCompiled(t *testing.T, m *Model) *compiledModel {
	t.Helper()
	cm, err := compileModel(m)
	if err != nil {
		t.Fatalf("compileModel: %v", err)
	}
	if cm == nil {
		t.Fatal("compileModel returned no model and no error")
	}
	return cm
}

// The positive control. Every refusal test below reads as evidence only because this
// environment accepts the standard library, the three extensions, self, and an earlier
// value. A refusal that comes from a broken environment would pass them all.
func TestCompileAcceptsTheEnvironment(t *testing.T) {
	m := celBase(t)
	m.Spec.Values = []Value{
		{Name: "full", Expr: "${inputs.image + ':latest'}"},
		{Name: "up", Expr: "${inputs.image.upperAscii()}"},
		{Name: "who", Expr: "${self.name + '-' + self.application}"},
		{Name: "nums", Expr: "${[1, 2, 3].slice(1, 3)}"},
		{Name: "next", Expr: "${cel.bind(x, inputs.replicas, x + 1)}"},
		{Name: "brace", Expr: "${'}' + inputs.image}"},
		{Name: "doubled", Expr: "${values.nums.map(n, n * 2)}"},
		{Name: "tags", Expr: "${inputs.tags.map(t, t.upperAscii())}"},
	}
	m.Spec.Parts[0].When = "${inputs.replicas > 1}"
	m.Spec.Parts[0].ForEach = &ForEach{As: "item", In: "${inputs.tags}"}
	cm := wantCompiled(t, m)
	for name, want := range map[string]string{
		"full": "string", "up": "string", "who": "string", "nums": "list(int)", "next": "int",
		"brace": "string", "doubled": "list(int)", "tags": "list(string)",
	} {
		if got := cm.valueType(name); got != want {
			t.Errorf("value %s has type %q, want %q", name, got, want)
		}
	}
	if got := cm.elemType("app"); got != "string" {
		t.Errorf("forEach.in element type = %q, want string", got)
	}
}

func TestCompileRefusals(t *testing.T) {
	cases := []struct {
		name  string
		model func(t *testing.T) *Model
		path  string
		cause string
	}{
		{"unknown function", func(t *testing.T) *Model { return oneValue(t, "${inputs.image.frobnicate()}") }, "spec.values[0].expr", "frobnicate"},
		{"wrong operand types", func(t *testing.T) *Model { return oneValue(t, "${inputs.replicas + 'x'}") }, "spec.values[0].expr", "no matching overload"},
		{"unknown input", func(t *testing.T) *Model { return oneValue(t, "${inputs.nope}") }, "spec.values[0].expr", "undeclared reference"},
		{"parts are not in scope in 2a", func(t *testing.T) *Model { return oneValue(t, "${parts.app}") }, "spec.values[0].expr", "undeclared reference"},
		{"self has name and application only", func(t *testing.T) *Model { return oneValue(t, "${self.version}") }, "spec.values[0].expr", "undeclared reference"},
		{"syntax error", func(t *testing.T) *Model { return oneValue(t, "${1 +}") }, "spec.values[0].expr", "Syntax error"},
		{"value without a hole", func(t *testing.T) *Model { return oneValue(t, "inputs.image") }, "spec.values[0].expr", "exactly one"},
		{"text before the hole", func(t *testing.T) *Model { return oneValue(t, "x${inputs.image}") }, "spec.values[0].expr", "exactly one"},
		{"text after the hole", func(t *testing.T) *Model { return oneValue(t, "${inputs.image}x") }, "spec.values[0].expr", "exactly one"},
		{"empty hole", func(t *testing.T) *Model { return oneValue(t, "${}") }, "spec.values[0].expr", "empty"},
		{"blank hole", func(t *testing.T) *Model { return oneValue(t, "${  }") }, "spec.values[0].expr", "empty"},
		{"has on an optional input", func(t *testing.T) *Model { return oneValue(t, "${has(inputs.nick) ? 1 : 2}") }, "spec.values[0].expr", "== null"},
		{"has on a required input", func(t *testing.T) *Model { return oneValue(t, "${has(inputs.image)}") }, "spec.values[0].expr", "== null"},
		{"math extension is not in celEnv 1", func(t *testing.T) *Model { return oneValue(t, "${math.greatest(1, 2)}") }, "spec.values[0].expr", "math"},
		{"encoders extension is not in celEnv 1", func(t *testing.T) *Model { return oneValue(t, "${base64.encode(b'a')}") }, "spec.values[0].expr", "base64"},
		{"sets extension is not in celEnv 1", func(t *testing.T) *Model { return oneValue(t, "${sets.contains([1], [1])}") }, "spec.values[0].expr", "sets"},
		{"when is not a bool", func(t *testing.T) *Model { return partWhen(t, "${inputs.image}") }, "spec.parts[0].when", "bool"},
		{"when without a hole", func(t *testing.T) *Model { return partWhen(t, "inputs.replicas > 1") }, "spec.parts[0].when", "exactly one"},
		{"when does not see the loop variable", func(t *testing.T) *Model {
			m := partIn(t, "${[1, 2]}")
			m.Spec.Parts[0].When = "${item == 1}"
			return m
		}, "spec.parts[0].when", "undeclared reference"},
		{"forEach.in is a string", func(t *testing.T) *Model { return partIn(t, "${inputs.image}") }, "spec.parts[0].forEach.in", "list"},
		{"forEach.in is a map", func(t *testing.T) *Model { return partIn(t, "${inputs.labels}") }, "spec.parts[0].forEach.in", "list"},
		{"forEach.in is a list nested in an object", func(t *testing.T) *Model { return partIn(t, "${inputs.config.items}") }, "spec.parts[0].forEach.in", "list"},
		{"forEach.in without a hole", func(t *testing.T) *Model { return partIn(t, "inputs.tags") }, "spec.parts[0].forEach.in", "exactly one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { wantRefusal(t, tc.model(t), tc.path, tc.cause) })
	}
}

func TestCompileReportsFirstFailureInDocumentOrder(t *testing.T) {
	m := celBase(t)
	m.Spec.Values = []Value{{Name: "a", Expr: "${1}"}, {Name: "b", Expr: "${inputs.nope}"}}
	m.Spec.Parts[0].When = "${inputs.alsonope}"
	_, err := compileModel(m)
	if err == nil || !strings.Contains(err.Error(), "spec.values[1].expr") || strings.Contains(err.Error(), "spec.parts") {
		t.Fatalf("the first failure in document order is spec.values[1].expr: %v", err)
	}
}

func TestLaterValueRefused(t *testing.T) {
	t.Run("value 1 reads value 2", func(t *testing.T) {
		m := celBase(t)
		m.Spec.Values = []Value{{Name: "first", Expr: "${values.second + 1}"}, {Name: "second", Expr: "${1}"}}
		wantRefusal(t, m, "spec.values[0].expr", "values.second is not computed yet")
	})
	t.Run("a value reads itself", func(t *testing.T) {
		m := celBase(t)
		m.Spec.Values = []Value{{Name: "loop", Expr: "${values.loop}"}}
		wantRefusal(t, m, "spec.values[0].expr", "values.loop is not computed yet")
	})
	t.Run("a value reads an earlier value", func(t *testing.T) {
		m := celBase(t)
		m.Spec.Values = []Value{{Name: "first", Expr: "${1}"}, {Name: "second", Expr: "${values.first + 1}"}}
		cm := wantCompiled(t, m)
		if got := cm.valueType("second"); got != "int" {
			t.Errorf("value second has type %q, want int", got)
		}
	})
}

// A comprehension over a map walks keys in an order the language does not fix. A
// comprehension over a dyn value has no known element type. celEnv 1 allows a
// comprehension over a list only, so a typed list is the one range a model can use.
func TestMapRangeRefused(t *testing.T) {
	for _, expr := range []string{
		"inputs.labels.map(k, k)",
		"inputs.labels.filter(k, k != '')",
		"inputs.labels.exists(k, k == 'a')",
		"cel.bind(m, inputs.labels, m.all(k, k != ''))",
		"{'a': 1}.map(k, k)",
	} {
		t.Run(expr, func(t *testing.T) {
			wantRefusal(t, oneValue(t, "${"+expr+"}"), "spec.values[0].expr", "ranges over a list in celEnv 1")
		})
	}
}

func TestDynRangeRefused(t *testing.T) {
	for _, expr := range []string{
		"inputs.config.items.map(i, i)",
		"inputs.config.items.exists(i, i == 'a')",
	} {
		t.Run(expr, func(t *testing.T) {
			wantRefusal(t, oneValue(t, "${"+expr+"}"), "spec.values[0].expr", "ranges over a list in celEnv 1")
		})
	}
}

func TestDynCallRefused(t *testing.T) {
	for _, expr := range []string{
		"dyn(inputs.image)",
		"dyn({'a': 1}).map(k, k)",
		"[dyn(1)]",
	} {
		t.Run(expr, func(t *testing.T) {
			wantRefusal(t, oneValue(t, "${"+expr+"}"), "spec.values[0].expr", "dyn() is not available in celEnv 1")
		})
	}
	t.Run("in when", func(t *testing.T) {
		wantRefusal(t, partWhen(t, "${dyn(true)}"), "spec.parts[0].when", "dyn() is not available in celEnv 1")
	})
	t.Run("in forEach.in", func(t *testing.T) {
		wantRefusal(t, partIn(t, "${dyn([1])}"), "spec.parts[0].forEach.in", "dyn() is not available in celEnv 1")
	})
}

// An optional input (not required, no default) is null when absent. A model tests that
// with `== null`. has() stays refused (see TestCompileRefusals).
func TestNullableInputs(t *testing.T) {
	m := celBase(t)
	m.Spec.Values = []Value{
		{Name: "absent", Expr: "${inputs.nick == null}"},
		{Name: "pick", Expr: "${inputs.nick == null ? 'anon' : inputs.nick}"},
		{Name: "tags", Expr: "${inputs.tags.map(t, t)}"},
		{Name: "image", Expr: "${inputs.image}"},
		{Name: "replicas", Expr: "${inputs.replicas}"},
	}
	cm := wantCompiled(t, m)
	for name, want := range map[string]string{
		"absent": "bool", "pick": "string", "tags": "list(string)", "image": "string", "replicas": "int",
	} {
		if got := cm.valueType(name); got != want {
			t.Errorf("value %s has type %q, want %q", name, got, want)
		}
	}
}

func TestForEachElementType(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"${inputs.tags}", "string"},
		{"${[1, 2]}", "int"},
		{"${[1.5, 2.5]}", "double"},
		{"${inputs.tags.map(t, size(t))}", "int"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			cm := wantCompiled(t, partIn(t, tc.in))
			if got := cm.elemType("app"); got != tc.want {
				t.Errorf("element type = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("a part without forEach", func(t *testing.T) {
		cm := wantCompiled(t, celBase(t))
		if got := cm.elemType("app"); got != "" {
			t.Errorf("element type of a part without forEach = %q, want empty", got)
		}
	})
}

// bindNest builds n cel.bind calls, each inside the body of the one before.
func bindNest(n int) string {
	expr := fmt.Sprintf("v%d", n)
	for i := n; i >= 1; i-- {
		init := "1"
		if i > 1 {
			init = fmt.Sprintf("v%d", i-1)
		}
		expr = fmt.Sprintf("cel.bind(v%d, %s, %s)", i, init, expr)
	}
	return expr
}

func TestBindDepth(t *testing.T) {
	cm := wantCompiled(t, oneValue(t, "${"+bindNest(8)+"}"))
	if got := cm.valueType("v"); got != "int" {
		t.Errorf("depth 8 has type %q, want int", got)
	}
	wantRefusal(t, oneValue(t, "${"+bindNest(9)+"}"), "spec.values[0].expr", "nesting")
}

// sortByChain builds [seed] followed by n sortBy calls. Each sortBy macro expands into a
// bind-shaped comprehension (ext/lists.go in cel-go), and the next call's target is that
// comprehension's initializer.
func sortByChain(seed string, n int) string {
	return seed + strings.Repeat(".sortBy(x, x)", n)
}

// The nesting limit counts every bind-shaped comprehension, not only the explicit
// cel.bind calls. In celEnv 1 the macros that expand to one are cel.bind and sortBy; the
// README names both. These rows pin that rule. The code already behaves this way, so
// they guard the README and are not evidence for a code change.
func TestBindDepthCountsGeneratedBinds(t *testing.T) {
	cm := wantCompiled(t, oneValue(t, "${"+sortByChain("[1]", 8)+"}"))
	if got := cm.valueType("v"); got != "list(int)" {
		t.Errorf("8 chained sortBy calls have type %q, want list(int)", got)
	}
	wantRefusal(t, oneValue(t, "${"+sortByChain("[1]", 9)+"}"), "spec.values[0].expr", "nesting")

	// An explicit bind and a generated one add up.
	outer := func(inner string) string {
		return "${cel.bind(a, 1, cel.bind(b, a, cel.bind(c, b, cel.bind(d, c, " + inner + "))))}"
	}
	cm = wantCompiled(t, oneValue(t, outer(sortByChain("[d]", 4))))
	if got := cm.valueType("v"); got != "list(int)" {
		t.Errorf("4 binds and 4 sortBy calls have type %q, want list(int)", got)
	}
	wantRefusal(t, oneValue(t, outer(sortByChain("[d]", 5))), "spec.values[0].expr", "nesting")
}

func TestExpressionSize(t *testing.T) {
	literal := func(n int) string { return "'" + strings.Repeat("a", n-2) + "'" }
	if got := len(literal(4096)); got != 4096 {
		t.Fatalf("test bug: literal has %d code points", got)
	}
	cm := wantCompiled(t, oneValue(t, "${"+literal(4096)+"}"))
	if got := cm.valueType("v"); got != "string" {
		t.Errorf("an expression of 4096 code points has type %q, want string", got)
	}
	wantRefusal(t, oneValue(t, "${"+literal(4097)+"}"), "spec.values[0].expr", "size")
}

func TestRecursionDepth(t *testing.T) {
	nest := func(n int) string { return strings.Repeat("(", n) + "1" + strings.Repeat(")", n) }
	wantCompiled(t, oneValue(t, "${"+nest(8)+"}"))
	// The parser counts depth its own way, so the test pins a depth far above the limit
	// and does not guess the exact boundary.
	wantRefusal(t, oneValue(t, "${"+nest(200)+"}"), "spec.values[0].expr", "recursion")
}

// Load checks shape and compiles nothing. A broken expression loads, and compileModel
// is where it fails. The Load half passes at the red commit, because Load already
// behaves so, and discriminates nothing there. The compileModel half fails at red.
// The Load half pins the boundary against a later change.
func TestLoadStillCompilesNothing(t *testing.T) {
	m, err := Load(replace(t, "${inputs.image + ':latest'}", "${inputs.nope +}"))
	if err != nil || m == nil {
		t.Fatalf("Load must accept a model with a broken expression: %v", err)
	}
	wantRefusal(t, m, "spec.values[0].expr", "Syntax error")
}
