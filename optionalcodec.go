package figureout

import (
	"encoding/json"

	"github.com/go-faster/yaml"
)

// The carrier round-trips through JSON and YAML as the value it holds, so a
// struct converted from a "*T" to an [OptionalOf] keeps marshaling the way its
// consumers already expect: missing is null, present is the value itself, and
// the carrier is invisible in the document.
//
// It is what makes conversion a real answer for a configuration adopted onto
// figureout. Such a struct is usually marshaled as well as read, and a carrier
// that serialized as {"value":…,"set":true} would force the adopter to keep the
// pointer for the encoder's sake alone.
//
// Decoding null yields a missing value rather than a present zero, which is the
// same reading resolution gives it.

// MarshalJSON implements [json.Marshaler].
func (o OptionalOf[T]) MarshalJSON() ([]byte, error) {
	if !o.set {
		return []byte("null"), nil
	}
	return json.Marshal(o.value)
}

// UnmarshalJSON implements [json.Unmarshaler].
func (o *OptionalOf[T]) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		o.Clear()
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Set(v)
	return nil
}

// MarshalYAML implements [yaml.Marshaler].
func (o OptionalOf[T]) MarshalYAML() (any, error) {
	if !o.set {
		return nil, nil
	}
	return o.value, nil
}

// UnmarshalYAML implements [yaml.Unmarshaler].
func (o *OptionalOf[T]) UnmarshalYAML(n *yaml.Node) error {
	if n.Tag == "!!null" {
		o.Clear()
		return nil
	}
	var v T
	if err := n.Decode(&v); err != nil {
		return err
	}
	o.Set(v)
	return nil
}
