package traits

import (
	"fmt"
	"strconv"

	"github.com/go-kure/launcher/pkg/oam"
)

// authoredWord reads the optional word at m[key], one of valid.
//
// present is false where the key is left out or holds a null (a typed nil
// included): the caller's default stands. refused is empty for a word of
// valid. For any other value it says how the refusal names what was written:
// the quoted word, or that the value is not a string, with its type. Before
// go-kure/launcher#1598 a value that is not a string was read as left out.
func authoredWord(m map[string]any, key string, valid map[string]bool) (word string, present bool, refused string) {
	raw, ok := m[key]
	if !ok || oam.IsNullValue(raw) {
		return "", false, ""
	}
	s, isString := raw.(string)
	switch {
	case !isString:
		return "", true, fmt.Sprintf("a value that is not a string (%T)", raw)
	case !valid[s]:
		return "", true, strconv.Quote(s)
	}
	return s, true, ""
}
