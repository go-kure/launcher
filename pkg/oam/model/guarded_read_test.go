package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin the guarded read of go-kure/launcher#1678: a part reads an earlier part that has
// a when only in the true branch of a conditional whose test holds every top-level && term of
// that when, or in a part whose own when holds them. Compile checks it, so a model that
// registers is safe for every input set.

const guardedInputs = `  inputs:
    db: {type: boolean, required: true}
    x: {type: boolean, required: true}
    opt: {type: boolean}
`

func guardedDoc(parts string) string {
	return strings.Replace(modelHeader, "  parts:\n", guardedInputs+"  parts:\n", 1) + parts
}

// condDB is a postgresql part with the given when expression.
func condDB(when string) string {
	return "    - id: db\n      name: db\n      type: " + pgType + "\n      when: \"${ " + when + " }\"\n      properties: {}\n"
}

// reader is a configmap part whose data.x is the given expression. head is extra lines
// of the part (for example a when).
func reader(head, expr string) string {
	return "    - id: cfg\n      name: cfg\n      type: configmap@v1\n" + head + "      properties:\n        data:\n          x: \"" + expr + "\"\n"
}

func compileDoc(t *testing.T, doc string) error {
	t.Helper()
	m, err := model.Load([]byte(doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = model.Compile(m)
	return err
}

func wantInvalid(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("Compile accepted it")
	}
	if !errors.Is(err, model.ErrInvalid) {
		t.Errorf("error %q does not wrap ErrInvalid", err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestCompile_AcceptsAGuardedRead(t *testing.T) {
	for name, tc := range map[string]struct{ when, expr string }{
		"the when as the test":                  {"inputs.db", "${ inputs.db ? parts.db.name : 'none' }"},
		"a null else branch":                    {"inputs.db", "${ inputs.db ? parts.db.name : null }"},
		"an extra term after":                   {"inputs.db", "${ inputs.db && inputs.x ? parts.db.name : null }"},
		"an extra term before":                  {"inputs.db", "${ inputs.x && inputs.db ? parts.db.name : null }"},
		"two terms in another order":            {"inputs.db && inputs.x", "${ inputs.x && inputs.db ? parts.db.name : null }"},
		"a parenthesised test":                  {"inputs.db", "${ (inputs.db) ? parts.db.name : null }"},
		"a derived output":                      {"inputs.db", "${ inputs.db ? parts.db.app_secret.name : 'none' }"},
		"an index read":                         {"inputs.db", "${ inputs.db ? parts.db['name'] : 'none' }"},
		"text around the hole":                  {"inputs.db", "host-${ inputs.db ? parts.db.name : 'none' }"},
		"a comprehension in the guarded branch": {"inputs.db", "${ inputs.db ? ['a', 'b'].map(s, s + parts.db.name) : [] }"},
		// The initializer of a bind runs outside the scope of the new variable, so a test there is read as usual.
		"a bind initializer": {"inputs.db", "${ cel.bind(n, inputs.db ? parts.db.name : 'none', n) }"},
		// A leading dot names the model's own variable, so it is no other name.
		"a leading dot on the name": {".inputs.db", "${ .inputs.db ? parts.db.name : 'none' }"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := compileDoc(t, guardedDoc(condDB(tc.when)+reader("", tc.expr))); err != nil {
				t.Fatalf("Compile: %v", err)
			}
		})
	}
}

// A reader emitted only when the part is needs no conditional for its other sites.
func TestCompile_AReadersWhenCarriesTheGuard(t *testing.T) {
	for name, tc := range map[string]struct {
		head, expr string
		want       []string
	}{
		"its when holds the terms":     {"      when: \"${ inputs.db && inputs.x }\"\n", "${ parts.db.name }", nil},
		"in any order":                 {"      when: \"${ inputs.x && inputs.db }\"\n", "${ parts.db.app_secret.name }", nil},
		"its own when reads it":        {"      when: \"${ inputs.db && parts.db.name == 'db' }\"\n", "x", []string{"spec.parts[1].when", "parts.db", "not readable"}},
		"its when lacks the terms":     {"      when: \"${ inputs.x }\"\n", "${ parts.db.name }", []string{"spec.parts[1].properties.data.x", "parts.db", "not readable"}},
		"its when has only one of two": {"      when: \"${ inputs.db }\"\n", "${ parts.db.name }", nil},
	} {
		t.Run(name, func(t *testing.T) {
			db := condDB("inputs.db")
			if name == "its when has only one of two" {
				db = condDB("inputs.db && inputs.x")
				tc.want = []string{"parts.db", "not readable"}
			}
			err := compileDoc(t, guardedDoc(db+reader(tc.head, tc.expr)))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Compile: %v", err)
				}
				return
			}
			wantInvalid(t, err, tc.want...)
		})
	}
}

func TestCompile_RefusesAnUnguardedReadOfAPartWithAWhen(t *testing.T) {
	for name, tc := range map[string]struct{ when, expr string }{
		"no conditional":                        {"inputs.db", "${ parts.db.name }"},
		"an unrelated test":                     {"inputs.db", "${ inputs.x ? parts.db.name : null }"},
		"the false branch":                      {"inputs.db", "${ inputs.db ? null : parts.db.name }"},
		"a negated test":                        {"inputs.db", "${ !inputs.db ? parts.db.name : null }"},
		"a test weaker than when":               {"inputs.db && inputs.x", "${ inputs.db ? parts.db.name : null }"},
		"an || test":                            {"inputs.db", "${ inputs.db || inputs.x ? parts.db.name : null }"},
		"a ternary in a comprehension":          {"inputs.db", "${ ['a'].map(s, inputs.db ? parts.db.name : s) }"},
		"a when that is a comprehension":        {"['a'].exists(s, s == 'a')", "${ ['a'].exists(s, s == 'a') ? parts.db.name : null }"},
		"values hidden by a local variable":     {"inputs.db", "${ ['a'].map(values, parts.db.name) }"},
		"a literal of another type":             {"1.0 / 2.0 == 0.0", "${ 1 / 2 == 0 ? parts.db.name : 'none' }"},
		"a literal with the other sign of zero": {"1.0 / -0.0 > 0.0", "${ 1.0 / 0.0 > 0.0 ? parts.db.name : 'none' }"},
		"a leading dot":                         {"inputs.db", "${ .parts.db.name }"},
		"a leading dot in the true branch of a test that is not the when": {"inputs.db", "${ inputs.x ? .parts.db.name : null }"},
		"the key of an index": {"inputs.db", "${ parts.db['name'] }"},
	} {
		t.Run(name, func(t *testing.T) {
			err := compileDoc(t, guardedDoc(condDB(tc.when)+reader("", tc.expr)))
			wantInvalid(t, err, "spec.parts[1].properties.data.x", "parts.db", "not readable", "when")
		})
	}
}

// Null is an error in text around a hole, so the refusal names two forms: a null else branch for
// a whole value, and a string else branch for text, which gives its string when the part is skipped.
func TestCompile_TheRefusalNamesAFormForAWholeValueAndAFormForText(t *testing.T) {
	err := compileDoc(t, guardedDoc(condDB("inputs.db")+reader("", "host-${ parts.db.name }")))
	wantInvalid(t, err, "spec.parts[1].properties.data.x", "parts.db", "not readable",
		"? parts.db.name : null)", "? parts.db.name : 'none')", "skipped")
}

// The null form the refusal names for a whole value stays refused in text around a hole.
func TestCompile_ANullElseBranchIsRefusedInTextAroundAHole(t *testing.T) {
	err := compileDoc(t, guardedDoc(condDB("inputs.db")+reader("", "host-${ inputs.db ? parts.db.name : null }")))
	wantInvalid(t, err, "spec.parts[1].properties.data.x", "not allowed in text")
}

// 1 and 1.0 are equal in CEL and are not the same condition: 1 / 2 is 0 and 1.0 / 2.0 is 0.5.
// 0.0 and -0.0 are equal in CEL and divide to different infinities.
func TestCompile_ALiteralThatDiffersIsNoGuardInAReadersWhen(t *testing.T) {
	for name, tc := range map[string]struct{ db, when string }{
		"another type":             {"1.0 / 2.0 == 0.0", "1 / 2 == 0"},
		"the other sign of zero":   {"1.0 / -0.0 > 0.0", "1.0 / 0.0 > 0.0"},
		"the same literal is fine": {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.db == "" {
				doc := guardedDoc(condDB("1.0 / 0.0 > 0.0") + reader("      when: \"${ 1.0 / 0.0 > 0.0 }\"\n", "${ parts.db.name }"))
				if err := compileDoc(t, doc); err != nil {
					t.Fatalf("Compile: %v", err)
				}
				return
			}
			err := compileDoc(t, guardedDoc(condDB(tc.db)+reader("      when: \"${ "+tc.when+" }\"\n", "${ parts.db.name }")))
			wantInvalid(t, err, "spec.parts[1].properties.data.x", "parts.db", "not readable")
		})
	}
}

func TestCompile_AGuardedReadOfAForEachPartIsStillRefused(t *testing.T) {
	many := "    - id: many\n      name: \"m-${ item }\"\n      type: configmap@v1\n      when: \"${ inputs.db }\"\n      forEach:\n        as: item\n        in: \"${ ['a', 'b'] }\"\n      properties: {}\n"
	err := compileDoc(t, guardedDoc(many+reader("", "${ inputs.db ? parts.many.name : null }")))
	wantInvalid(t, err, "spec.parts[1].properties.data.x", "parts.many", "not readable", "forEach")
}

// An output stays one unguarded read of a part with no when.
func TestCompile_AnOutputOfAPartWithAWhenIsStillRefused(t *testing.T) {
	doc := strings.Replace(guardedDoc(condDB("inputs.db")), "  outputs: {}\n", "  outputs:\n    own: \"${ parts.db.name }\"\n", 1)
	wantInvalid(t, compileDoc(t, doc), "spec.outputs.own", "parts.db", "not readable")
}

// A when that can be null is no guard: the part is skipped when the input is absent, and the same
// input as a test is null, which fails at build.
func TestCompile_RefusesAReadOfAPartWhoseWhenCanBeNull(t *testing.T) {
	values := func(parts string) string {
		return strings.Replace(guardedDoc(parts), "  parts:\n", "  values:\n    - name: v\n      expr: \"${ inputs.opt }\"\n  parts:\n", 1)
	}
	t.Run("an optional input", func(t *testing.T) {
		err := compileDoc(t, guardedDoc(condDB("inputs.opt")+reader("", "${ inputs.opt ? parts.db.name : null }")))
		wantInvalid(t, err, "spec.parts[1].properties.data.x", "parts.db", "can be null", "== true")
	})
	t.Run("an optional input with a leading dot", func(t *testing.T) {
		err := compileDoc(t, guardedDoc(condDB(".inputs.opt")+reader("", "${ .inputs.opt ? parts.db.name : null }")))
		wantInvalid(t, err, "spec.parts[1].properties.data.x", "parts.db", "can be null", "== true")
	})
	t.Run("a value that holds an optional input", func(t *testing.T) {
		err := compileDoc(t, values(condDB("values.v")+reader("", "${ values.v ? parts.db.name : null }")))
		wantInvalid(t, err, "parts.db", "can be null", "== true")
	})
	// CEL types this conditional as a plain bool, and with db false and opt absent it gives null.
	t.Run("a conditional that returns an optional input", func(t *testing.T) {
		err := compileDoc(t, guardedDoc(condDB("inputs.db ? true : inputs.opt")+reader("", "${ (inputs.db ? true : inputs.opt) ? parts.db.name : 'none' }")))
		wantInvalid(t, err, "spec.parts[1].properties.data.x", "parts.db", "can be null", "== true")
	})
	t.Run("a conditional of values that are never null", func(t *testing.T) {
		if err := compileDoc(t, guardedDoc(condDB("inputs.db ? inputs.x : false")+reader("", "${ (inputs.db ? inputs.x : false) ? parts.db.name : 'none' }"))); err != nil {
			t.Fatalf("Compile: %v", err)
		}
	})
	t.Run("an && chain with an optional input fails instead of giving null", func(t *testing.T) {
		if err := compileDoc(t, guardedDoc(condDB("inputs.db && inputs.opt")+reader("", "${ inputs.db && inputs.opt ? parts.db.name : 'none' }"))); err != nil {
			t.Fatalf("Compile: %v", err)
		}
	})
	// The checker types the value as a plain bool; its expression gives null with db false and opt absent.
	t.Run("a value whose expression returns an optional input", func(t *testing.T) {
		doc := values(condDB("values.enabled") + reader("", "${ values.enabled ? parts.db.name : 'none' }"))
		doc = strings.Replace(doc, "      expr: \"${ inputs.opt }\"", "      expr: \"${ inputs.db ? true : inputs.opt }\"", 1)
		doc = strings.Replace(doc, "- name: v", "- name: enabled", 1)
		wantInvalid(t, compileDoc(t, doc), "spec.parts[1].properties.data.x", "parts.db", "can be null", "== true")
	})
	t.Run("a value that is never null", func(t *testing.T) {
		doc := values(condDB("values.enabled") + reader("", "${ values.enabled ? parts.db.name : 'none' }"))
		doc = strings.Replace(doc, "      expr: \"${ inputs.opt }\"", "      expr: \"${ inputs.db && inputs.x }\"", 1)
		doc = strings.Replace(doc, "- name: v", "- name: enabled", 1)
		if err := compileDoc(t, doc); err != nil {
			t.Fatalf("Compile: %v", err)
		}
	})
	t.Run("a reader whose when holds it", func(t *testing.T) {
		err := compileDoc(t, guardedDoc(condDB("inputs.opt")+reader("      when: \"${ inputs.opt }\"\n", "${ parts.db.name }")))
		wantInvalid(t, err, "parts.db", "can be null", "== true")
	})
	t.Run("the part alone still registers", func(t *testing.T) {
		if err := compileDoc(t, guardedDoc(condDB("inputs.opt")+reader("", "x"))); err != nil {
			t.Fatalf("Compile: %v", err)
		}
	})
	t.Run("compared with true it is a guard", func(t *testing.T) {
		if err := compileDoc(t, guardedDoc(condDB("inputs.opt == true")+reader("", "${ inputs.opt == true ? parts.db.name : null }"))); err != nil {
			t.Fatalf("Compile: %v", err)
		}
	})
}

// A name a loop variable can hide, such as the type name bool, may mean one thing in the part that
// owns the when and another in a reader, so a when that reads one is no guard.
func TestCompile_AWhenThatReadsAnotherNameIsNoGuard(t *testing.T) {
	loop := "      forEach:\n        as: bool\n        in: \"${ [true] }\"\n"
	for name, tc := range map[string]struct{ when, head, expr string }{
		"the test":              {"type(true) == type(bool)", loop, "${ type(true) == type(bool) ? parts.db.name : 'none' }"},
		"the reader's own when": {"type(true) == type(bool)", loop + "      when: \"${ type(true) == type(bool) }\"\n", "${ parts.db.name }"},
	} {
		t.Run(name, func(t *testing.T) {
			err := compileDoc(t, guardedDoc(condDB(tc.when)+reader(tc.head, tc.expr)))
			wantInvalid(t, err, "parts.db", "not readable", "inputs, self, values and parts")
		})
	}
}

// A key of an object input is tested with `in`, and has() is refused with that advice.
func TestCompile_HasIsRefusedWithTheIdiom(t *testing.T) {
	doc := strings.Replace(modelHeader, "  parts:\n", "  inputs:\n    obj: {type: object}\n  parts:\n", 1) +
		reader("", "${ has(inputs.obj.key) }")
	wantInvalid(t, compileDoc(t, doc), "has()", "inputs.x == null", "'key' in inputs.obj")
}

func guardedScope(db bool) model.Scope {
	sc := expandScope
	sc.Inputs = map[string]any{"db": db, "x": true, "opt": nil}
	return sc
}

const guardedWorker = `
    - id: worker
      name: worker
      type: ` + webType + `
      properties:
        image: ghcr.io/acme/worker:v1
        env:
          - name: DB_PASSWORD
            valueFrom:
              secretKeyRef:
                name: "${ inputs.db ? parts.db.app_secret.name : 'none' }"
                key: password
`

// The reader gets the name the build gives the part, for the part's own object and for a
// derived output, and gets the else branch when the part is skipped.
func TestExpand_AGuardedReadGivesTheBuildsName(t *testing.T) {
	c := compileOK(t, guardedDoc(condDB("inputs.db")+guardedWorker))
	t.Run("emitted", func(t *testing.T) {
		comps, err := c.Expand(expandTransformer(t), guardedScope(true), nil, nil)
		if err != nil {
			t.Fatalf("Expand: %v", err)
		}
		if len(comps) != 2 {
			t.Fatalf("Expand gave %d components, want 2", len(comps))
		}
		if got := secretRefName(t, comps[1]); got != "db-app" {
			t.Fatalf("secretKeyRef.name = %v, want db-app", got)
		}
		if !built(t, comps, nil)[[2]string{"Cluster", "db"}] {
			t.Fatalf("the build made no Cluster named db, so db-app is not that Cluster's Secret")
		}
	})
	t.Run("skipped", func(t *testing.T) {
		comps, err := c.Expand(expandTransformer(t), guardedScope(false), nil, nil)
		if err != nil {
			t.Fatalf("Expand: %v", err)
		}
		if len(comps) != 1 || comps[0].Name != "worker" {
			t.Fatalf("Expand gave %v, want the worker only", comps)
		}
		if got := secretRefName(t, comps[0]); got != "none" {
			t.Fatalf("secretKeyRef.name = %v, want none", got)
		}
	})
}

// The read-write Service of an emitted postgresql part is read in text, and a skipped part gives the
// string of the else branch.
func TestExpand_AGuardedReadOfTheServiceInText(t *testing.T) {
	c := compileOK(t, guardedDoc(condDB("inputs.db")+reader("", "host-${ inputs.db ? parts.db.rw_service.name : 'none' }")))
	for _, tc := range []struct {
		db   bool
		want string
	}{{true, "host-db-rw"}, {false, "host-none"}} {
		comps, err := c.Expand(expandTransformer(t), guardedScope(tc.db), nil, nil)
		if err != nil {
			t.Fatalf("db=%v: Expand: %v", tc.db, err)
		}
		if got := prop(t, comps[len(comps)-1].Properties, "data", "x"); got != tc.want {
			t.Errorf("db=%v: data.x = %v, want %s", tc.db, got, tc.want)
		}
	}
}

// A part that is not emitted gets no query, no bound outputs and no published entry, and a part
// that is emitted gets one query.
func TestExpand_ASkippedPartLeavesNoOutputs(t *testing.T) {
	c := compileOK(t, guardedDoc(condDB("inputs.db")+guardedWorker))
	for _, tc := range []struct {
		db      bool
		queries int
	}{{false, 1}, {true, 2}} { // the worker is always queried
		tr := expandTransformer(t)
		var asked []string
		sc := model.QueryOutputsWith(guardedScope(tc.db), func(comp *oam.Component, n func(oam.NameRequest) (string, bool)) ([]oam.Output, error) {
			asked = append(asked, comp.Name)
			return tr.ComponentOutputsNamed("shop", comp, n)
		})
		parts, published, err := model.ExpandedParts(c, tr, sc)
		if err != nil {
			t.Fatalf("db=%v: %v", tc.db, err)
		}
		if len(asked) != tc.queries {
			t.Errorf("db=%v: the Transformer was asked about %v, want %d questions", tc.db, asked, tc.queries)
		}
		_, boundDB := parts["db"]
		_, publishedDB := published["db"]
		if boundDB != tc.db || publishedDB != tc.db {
			t.Errorf("db=%v: parts holds db: %v, published holds db: %v", tc.db, boundDB, publishedDB)
		}
	}
}

// A key the part does not declare is still refused when the part is emitted; the branch of a
// skipped part is never read.
func TestExpand_AWrongKeyOfAGuardedReadFailsOnlyWhenThePartEmits(t *testing.T) {
	doc := guardedDoc(condDB("inputs.db") + reader("", "${ inputs.db ? parts.db.nope.name : 'none' }"))
	c := compileOK(t, doc)
	if _, err := c.Expand(expandTransformer(t), guardedScope(false), nil, nil); err != nil {
		t.Fatalf("Expand with the part skipped: %v", err)
	}
	_, err := c.Expand(expandTransformer(t), guardedScope(true), nil, nil)
	if err == nil {
		t.Fatal("Expand accepted a read of a key the part does not declare")
	}
	for _, want := range []string{"spec.parts[1].properties.data.x", "parts.db.nope", "nope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A null else branch is the value of the property when the part is skipped. The test records what
// the build does with it.
func TestExpand_ASkippedGuardedReadGivesNull(t *testing.T) {
	c := compileOK(t, guardedDoc(condDB("inputs.db")+reader("", "${ inputs.db ? parts.db.name : null }")))
	comps, err := c.Expand(expandTransformer(t), guardedScope(false), nil, nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(comps) != 1 {
		t.Fatalf("Expand gave %d components, want the reader only", len(comps))
	}
	if got := prop(t, comps[0].Properties, "data", "x"); got != nil {
		t.Fatalf("data.x = %#v, want null", got)
	}
}

// With the input absent, `inputs.opt == true` is false, so the part is skipped and the test takes
// the else branch.
func TestExpand_ACheckedNullableWhenSkipsThePart(t *testing.T) {
	c := compileOK(t, guardedDoc(condDB("inputs.opt == true")+reader("", "${ inputs.opt == true ? parts.db.cluster.name : 'none' }")))
	sc := guardedScope(true)
	comps, err := c.Expand(expandTransformer(t), sc, nil, nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(comps) != 1 || prop(t, comps[0].Properties, "data", "x") != "none" {
		t.Fatalf("Expand gave %v, want the reader with none", comps)
	}
	sc.Inputs["opt"] = true
	comps, err = c.Expand(expandTransformer(t), sc, nil, nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(comps) != 2 || prop(t, comps[1].Properties, "data", "x") != "db" {
		t.Fatalf("Expand gave %v, want db and the reader with db", comps)
	}
}

func compileOK(t *testing.T, doc string) *model.Compiled {
	t.Helper()
	m, err := model.Load([]byte(doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c, err := model.Compile(m)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return c
}
