package requiredfields_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam/internal/requiredfields"
)

func TestRefuse(t *testing.T) {
	required := map[string]string{
		"selector.expressions[].operator": "the operator",
		"selector.expressions[].key":      "the key",
		"tls.secret":                      "the Secret",
		"tls.secret.name":                 "the Secret's name",
	}
	expression := func(fields ...string) map[string]any {
		out := map[string]any{}
		for _, field := range fields {
			out[field] = "x"
		}
		return out
	}
	for name, tc := range map[string]struct {
		authored map[string]any
		want     string
	}{
		"nothing authored":      {map[string]any{}, ""},
		"parent left out":       {map[string]any{"selector": map[string]any{}}, ""},
		"empty list":            {map[string]any{"selector": map[string]any{"expressions": []any{}}}, ""},
		"every field authored":  {map[string]any{"selector": map[string]any{"expressions": []any{expression("key", "operator")}}, "tls": map[string]any{"secret": map[string]any{"name": "s"}}}, ""},
		"authored empty value":  {map[string]any{"tls": map[string]any{"secret": map[string]any{"name": ""}}}, ""},
		"parent is no object":   {map[string]any{"tls": "x", "selector": []any{"x"}}, ""},
		"entry is no object":    {map[string]any{"selector": map[string]any{"expressions": []any{"x"}}}, ""},
		"field left out":        {map[string]any{"tls": map[string]any{}}, "tls.secret: required (the Secret)"},
		"nested field left out": {map[string]any{"tls": map[string]any{"secret": map[string]any{}}}, "tls.secret.name: required (the Secret's name)"},
		"second entry":          {map[string]any{"selector": map[string]any{"expressions": []any{expression("key", "operator"), expression("key")}}}, "selector.expressions[1].operator: required (the operator)"},
		// Paths are read in sorted order: `key` before `operator`, `selector`
		// before `tls`.
		"first in path order": {map[string]any{"tls": map[string]any{}, "selector": map[string]any{"expressions": []any{expression()}}}, "selector.expressions[0].key: required (the key)"},
		// A key matches as the decode matches it, and is reported as authored.
		"key in another case":    {map[string]any{"TLS": map[string]any{"Secret": map[string]any{"Name": "s"}}}, ""},
		"parent in another case": {map[string]any{"TLS": map[string]any{"Secret": map[string]any{}}}, "TLS.Secret.name: required (the Secret's name)"},
		// One field in two spellings: the decode keeps one or merges both, so
		// each is held to the list, whichever of them is the json name.
		"two spellings, the json name incomplete":  {map[string]any{"tls": map[string]any{"SECRET": map[string]any{"name": "s"}, "secret": map[string]any{}}}, "tls.secret.name: required (the Secret's name)"},
		"two spellings, the other incomplete":      {map[string]any{"tls": map[string]any{"SECRET": map[string]any{}, "secret": map[string]any{"name": "s"}}}, "tls.SECRET.name: required (the Secret's name)"},
		"two spellings, neither the json name":     {map[string]any{"tls": map[string]any{"SECRET": map[string]any{"name": "s"}, "Secret": map[string]any{}}}, "tls.Secret.name: required (the Secret's name)"},
		"two spellings of a list":                  {map[string]any{"selector": map[string]any{"EXPRESSIONS": []any{expression("key", "operator")}, "expressions": []any{expression("key")}}}, "selector.expressions[0].operator: required (the operator)"},
		"two spellings of a list, first in order":  {map[string]any{"selector": map[string]any{"EXPRESSIONS": []any{expression("operator")}, "expressions": []any{expression("key")}}}, "selector.EXPRESSIONS[0].key: required (the key)"},
		"two spellings, both complete":             {map[string]any{"tls": map[string]any{"SECRET": map[string]any{"name": "s"}, "secret": map[string]any{"NAME": "s"}}}, ""},
		"two spellings of the last field, one set": {map[string]any{"tls": map[string]any{"secret": map[string]any{"NAME": "s", "name": "t"}}}, ""},
		// A last field null in one spelling: the decode may take the null one
		// last, so it is the field left out, in either order.
		"two spellings of the last field, the later null":   {map[string]any{"tls": map[string]any{"secret": map[string]any{"NAME": "s", "name": nil}}}, "tls.secret.name: required (the Secret's name)"},
		"two spellings of the last field, the earlier null": {map[string]any{"tls": map[string]any{"secret": map[string]any{"NAME": nil, "name": "s"}}}, "tls.secret.NAME: required (the Secret's name)"},
		// A parent null in one spelling holds nothing to refuse; the other
		// spelling is still held to the list.
		"two spellings of a parent, one null, the other complete":   {map[string]any{"selector": map[string]any{"EXPRESSIONS": []any{expression("key", "operator")}, "expressions": nil}}, ""},
		"two spellings of a parent, one null, the other incomplete": {map[string]any{"selector": map[string]any{"EXPRESSIONS": []any{expression("key")}, "expressions": nil}}, "selector.EXPRESSIONS[0].operator: required (the operator)"},
	} {
		t.Run(name, func(t *testing.T) {
			err := requiredfields.Refuse(tc.authored, required)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want none", err)
			case tc.want != "" && (err == nil || err.Error() != tc.want):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestRefuse_MapValues: a {} step descends every value of a map, in the
// order of its keys and with no case folding, and names the value by its key.
func TestRefuse_MapValues(t *testing.T) {
	required := map[string]string{
		"storage{}.kind":    "the kind",
		"headers{}[].key":   "the header",
		"headers{}[].value": "the value",
	}
	header := func(fields ...string) map[string]any {
		out := map[string]any{}
		for _, field := range fields {
			out[field] = "x"
		}
		return out
	}
	for name, tc := range map[string]struct {
		authored map[string]any
		want     string
	}{
		"field present":  {map[string]any{"storage": map[string]any{"data": map[string]any{"kind": "x"}}}, ""},
		"field absent":   {map[string]any{"storage": map[string]any{"data": map[string]any{}}}, "storage[data].kind: required (the kind)"},
		"empty map":      {map[string]any{"storage": map[string]any{}}, ""},
		"map left out":   {map[string]any{}, ""},
		"non-map value":  {map[string]any{"storage": []any{map[string]any{}}}, ""},
		"value no map":   {map[string]any{"storage": map[string]any{"data": "x"}}, ""},
		"first key only": {map[string]any{"storage": map[string]any{"b": map[string]any{}, "a": map[string]any{}}}, "storage[a].kind: required (the kind)"},
		// A map key is an entry, never a spelling of a field: one spelled
		// like the field is descended as any other, and not case-folded.
		"key spelled like a field": {map[string]any{"storage": map[string]any{"kind": map[string]any{"name": "a"}}}, "storage[kind].kind: required (the kind)"},
		"keys differ in case":      {map[string]any{"storage": map[string]any{"Data": map[string]any{"kind": "x"}, "data": map[string]any{}}}, "storage[data].kind: required (the kind)"},
		"dotted key quoted":        {map[string]any{"storage": map[string]any{"a.b": map[string]any{}}}, `storage["a.b"].kind: required (the kind)`},
		// {}[]: every element of every list the map holds.
		"lists complete":         {map[string]any{"headers": map[string]any{"a": []any{header("key", "value")}, "b": []any{header("key", "value"), header("key", "value")}}}, ""},
		"later key, later entry": {map[string]any{"headers": map[string]any{"a": []any{header("key", "value")}, "b": []any{header("key", "value"), header("value")}}}, "headers[b][1].key: required (the header)"},
		"first key in order":     {map[string]any{"headers": map[string]any{"b": []any{header("value")}, "a": []any{header("value")}}}, "headers[a][0].key: required (the header)"},
		// Paths first, then keys: `key` is read under every key before `value`.
		"first path in order": {map[string]any{"headers": map[string]any{"b": []any{header("value")}, "a": []any{header("key")}}}, "headers[b][0].key: required (the header)"},
		"empty list":          {map[string]any{"headers": map[string]any{"a": []any{}}}, ""},
		"non-list value":      {map[string]any{"headers": map[string]any{"a": header()}}, ""},
		"entry is no object":  {map[string]any{"headers": map[string]any{"a": []any{"x"}}}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			err := requiredfields.Refuse(tc.authored, required)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want none", err)
			case tc.want != "" && (err == nil || err.Error() != tc.want):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestCiliumRule_SamePathsAtEveryPosition: the list is the same rule wherever
// it stands, with the position as a prefix and none for the rule itself.
func TestCiliumRule_SamePathsAtEveryPosition(t *testing.T) {
	bare := requiredfields.CiliumRule("")
	if len(bare) == 0 {
		t.Fatal("the rule's required list is empty")
	}
	for path := range bare {
		if strings.HasPrefix(path, ".") {
			t.Errorf("path %q of the rule itself starts with a dot", path)
		}
	}
	for _, at := range []string{"spec", "specs[]"} {
		want := map[string]string{}
		for path, says := range bare {
			want[at+"."+path] = says
		}
		if got := requiredfields.CiliumRule(at); !maps.Equal(got, want) {
			t.Errorf("CiliumRule(%q) = %v\nwant the rule's own list under %s: %v", at, slices.Sorted(maps.Keys(got)), at, slices.Sorted(maps.Keys(want)))
		}
	}
}

// TestCiliumTraitRule_IsTheRuleUnderThePublishedFields: every entry of the
// rule's list under a field the trait publishes, with what it says, and no
// other.
func TestCiliumTraitRule_IsTheRuleUnderThePublishedFields(t *testing.T) {
	published := requiredfields.CiliumTraitFields()
	trait := requiredfields.CiliumTraitRule()
	for path, says := range requiredfields.CiliumRule("") {
		field, _, _ := strings.Cut(path, ".")
		got, listed := trait[path]
		switch under := slices.Contains(published, strings.TrimSuffix(field, "[]")); {
		case under && (!listed || got != says):
			t.Errorf("%s lies under a published field and the trait's list holds %q, want %q", path, got, says)
		case !under && listed:
			t.Errorf("%s lies under no published field and is in the trait's list", path)
		}
	}
	for path := range trait {
		if _, ok := requiredfields.CiliumRule("")[path]; !ok {
			t.Errorf("%s is in the trait's list and not in the rule's", path)
		}
	}
	for _, field := range published {
		if !slices.ContainsFunc(slices.Collect(maps.Keys(trait)), func(path string) bool {
			return strings.HasPrefix(path, field+".") || strings.HasPrefix(path, field+"[].")
		}) {
			t.Errorf("the trait's list holds nothing under %s", field)
		}
	}
}
