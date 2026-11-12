package model

import (
	"cel.dev/cel-go/cel"

	"github.com/go-kure/launcher/pkg/oam"
)

// celType maps an input schema to the CEL type of the variable that holds it.
//
// An object is map(string, dyn), whatever its declared properties. A list inside an
// object is then dyn, and a comprehension over it is refused. That is a limit of
// celEnv 1 and not a goal. A union (types) is dyn.
func celType(s oam.PropertySchema) *cel.Type {
	if len(s.Types) > 0 {
		return cel.DynType
	}
	switch s.Type {
	case oam.PropertyTypeString:
		return cel.StringType
	case oam.PropertyTypeInteger:
		return cel.IntType
	case oam.PropertyTypeBoolean:
		return cel.BoolType
	case oam.PropertyTypeNumber:
		return cel.DoubleType
	case oam.PropertyTypeArray:
		elem := cel.DynType
		if s.Items != nil {
			elem = celType(*s.Items)
		}
		return cel.ListType(elem)
	case oam.PropertyTypeObject:
		return cel.MapType(cel.StringType, cel.DynType)
	}
	return cel.DynType
}

// inputType is the type of the variable `inputs.<name>`. An input that is not required
// and has no default can be absent, so it is nullable: absent means null, and a model
// tests it with `== null`. The nullable type keeps the kind of the type it wraps, so a
// nullable list still ranges in a comprehension.
func inputType(s oam.PropertySchema) *cel.Type {
	t := celType(s)
	if !s.Required && s.Default == nil && t.Kind() != cel.DynType.Kind() {
		return cel.NullableType(t)
	}
	return t
}
