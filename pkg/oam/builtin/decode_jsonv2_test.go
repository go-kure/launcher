//go:build goexperiment.jsonv2

package builtin_test

import (
	"encoding/json/jsontext"
	"reflect"
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// Under GOEXPERIMENT=jsonv2, encoding/json is backed by json/v2, which also calls a
// type's own MarshalJSONTo and UnmarshalJSONFrom.
type (
	// Its own UnmarshalJSONFrom takes the whole object and sets neither rival.
	reachDecodeFrom struct {
		reachInner
		Promoted string `json:"promoted"`
	}
	// As reachCustom, through MarshalJSONTo.
	reachEncodeTo struct {
		reachInner
		Promoted string `json:"promoted"`
	}
)

func (*reachDecodeFrom) UnmarshalJSONFrom(d *jsontext.Decoder) error {
	_, err := d.ReadValue()
	return err
}

func (e reachEncodeTo) MarshalJSONTo(enc *jsontext.Encoder) error {
	for _, tok := range []jsontext.Token{jsontext.BeginArray, jsontext.String(e.reachInner.Promoted), jsontext.String(e.Promoted), jsontext.EndArray} {
		if err := enc.WriteToken(tok); err != nil {
			return err
		}
	}
	return nil
}

// TestUnreachableJSONFields_JSONv2Methods: a root with a json/v2 method is as
// unprovable as one with its v1 counterpart.
func TestUnreachableJSONFields_JSONv2Methods(t *testing.T) {
	want := []string{"Promoted", "reachInner.Promoted"}
	for _, typ := range []reflect.Type{reflect.TypeFor[reachDecodeFrom](), reflect.TypeFor[reachEncodeTo]()} {
		if got := builtin.UnreachableJSONFields(typ); !slices.Equal(got, want) {
			t.Errorf("UnreachableJSONFields(%v) = %v, want %v", typ, got, want)
		}
	}

	// The gap is real: the key decodes, yet no rival is set, or only the outer one.
	if s, _, err := builtin.DecodeStrictJSON[reachDecodeFrom](map[string]any{"promoted": "x"}); err != nil || s.Promoted != "" || s.reachInner.Promoted != "" {
		t.Errorf("reachDecodeFrom: %+v, %v; want neither rival set", s, err)
	}
	if s, _, err := builtin.DecodeStrictJSON[reachEncodeTo](map[string]any{"promoted": "x"}); err != nil || s.Promoted != "x" || s.reachInner.Promoted != "" {
		t.Errorf("reachEncodeTo: %+v, %v; want promoted on the outer field", s, err)
	}
}
