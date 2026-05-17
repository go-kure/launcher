package builtin

import (
	"bytes"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
)

// DecodeStrict decodes src into T using yaml.v3 KnownFields mode.
// Unknown keys in src produce an error — they indicate a rendering map that
// does not match the handler's declared schema.
func DecodeStrict[T any](src map[string]any) (*T, error) {
	data, err := yaml.Marshal(src)
	if err != nil {
		return nil, errors.Wrap(err, "internal: marshal rendering")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var out T
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
