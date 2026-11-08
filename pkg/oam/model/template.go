package model

import (
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
)

const holeOpen = "${"

// scanHole returns the CEL source inside the one `${ }` hole that makes up all of s.
// The fields values[].expr, when and forEach.in hold exactly one hole and no other
// text, so a result keeps the CEL type of the expression.
//
// The scan finds the closing brace the way CEL reads the text: a `}` inside a string
// literal or a `//` comment does not close the hole, and a `{}` pair of a map literal
// nests.
func scanHole(s string) (string, error) {
	const want = "want exactly one ${ } hole and no other text"
	if !strings.HasPrefix(s, holeOpen) {
		return "", errors.New(want)
	}
	end, err := holeEnd(s, len(holeOpen))
	if err != nil {
		return "", err
	}
	if end != len(s)-1 {
		return "", errors.New(want)
	}
	inner := s[len(holeOpen):end]
	if strings.TrimSpace(inner) == "" {
		return "", errors.New("the ${ } hole is empty")
	}
	return inner, nil
}

// holeEnd returns the index of the `}` that closes a hole whose body starts at i.
func holeEnd(s string, i int) (int, error) {
	depth := 1
	for i < len(s) {
		switch s[i] {
		case '\'', '"':
			raw := i > 0 && (s[i-1] == 'r' || s[i-1] == 'R')
			j, ok := skipString(s, i, raw)
			if !ok {
				return 0, errors.New("a string literal in the ${ } hole is not closed")
			}
			i = j
			continue
		case '`':
			// An escaped identifier (CEL.g4, ESC_IDENTIFIER) holds letters, digits and
			// the characters _ . - / and space. So it may hold `//`, which is not a
			// comment here, and it never holds a quote, a brace or a new line. It ends at
			// the next backtick. A new line or the end of the text before that is not a
			// valid identifier, so the hole is not closed.
			end := strings.IndexAny(s[i+1:], "`\n")
			if end < 0 || s[i+1+end] != '`' {
				return 0, errors.New("an escaped identifier in the ${ } hole is not closed")
			}
			i += end + 2
			continue
		case '/':
			// A comment runs from `//` to the end of the line, as in the CEL lexer
			// (COMMENT : '//' (~'\n')*). A brace or a quote inside it is not syntax.
			// With no newline the comment runs to the end of s, so the hole is not closed.
			if strings.HasPrefix(s[i:], "//") {
				nl := strings.IndexByte(s[i:], '\n')
				if nl < 0 {
					return 0, errors.New("the ${ hole is not closed")
				}
				i += nl
				continue
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
		i++
	}
	return 0, errors.New("the ${ hole is not closed")
}

// skipString returns the index after the string literal that opens at s[i]. A literal
// is single-quoted, double-quoted, or triple-quoted. A backslash escapes the next byte
// unless the literal is raw.
func skipString(s string, i int, raw bool) (int, bool) {
	q := s[i]
	triple := strings.HasPrefix(s[i:], string([]byte{q, q, q}))
	j := i + 1
	if triple {
		j = i + 3
	}
	for j < len(s) {
		switch {
		case s[j] == '\\' && !raw:
			j += 2
		case triple && strings.HasPrefix(s[j:], string([]byte{q, q, q})):
			return j + 3, true
		case !triple && s[j] == q:
			return j + 1, true
		default:
			j++
		}
	}
	return 0, false
}
