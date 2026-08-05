package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
	jsonsource "github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

// carrierDecoder reads the legacy {value, env, file} carrier a configuration
// keeps during a deprecation window, resolving it to the plain string the field
// actually holds.
type carrierDecoder struct {
	env    map[string]string
	called *int
}

func (d carrierDecoder) DecodeValue(raw any) (any, error) {
	if d.called != nil {
		*d.called++
	}
	switch v := raw.(type) {
	case string:
		return v, nil
	case map[string]any:
		if name, ok := v["env"].(string); ok {
			return d.env[name], nil
		}
		if value, ok := v["value"].(string); ok {
			return value, nil
		}
		return nil, errNoCarrierMember
	default:
		return nil, errNoCarrierMember
	}
}

type carrierError string

func (e carrierError) Error() string { return string(e) }

const errNoCarrierMember carrierError = "carrier needs one of value or env"

type decoderConfig struct{ Token string }

var carrierShapes = []figureout.Shape{
	{Kind: figureout.ShapeString},
	{Kind: figureout.ShapeObject, Fields: map[string]figureout.Shape{
		"value": {Kind: figureout.ShapeString},
		"env":   {Kind: figureout.ShapeString},
	}},
}

func decoderDescriptor(t *testing.T, id figureout.SourceID, dec figureout.Decoder) *figureout.Descriptor[decoderConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *decoderConfig, s *figureout.Schema[decoderConfig]) {
		figureout.Value(s, &c.Token, "token", figureout.Secret(),
			figureout.WithDecoder(id, dec, carrierShapes...)).ApplyDefault("")
	})
	require.NoError(t, err)
	return d
}

func TestDecoderScalarSpelling(t *testing.T) {
	called := 0
	d := decoderDescriptor(t, yaml.Source, carrierDecoder{called: &called})

	cfg, _, err := d.Resolve(yaml.Bytes([]byte(`token: literal`)))
	require.NoError(t, err)
	require.Equal(t, "literal", cfg.Token)
	require.Equal(t, 1, called, "the field's own decoder runs")
}

func TestDecoderObjectSpelling(t *testing.T) {
	// The declared object shape reaches the decoder, which the semantic type
	// (a string) cannot describe on its own.
	d := decoderDescriptor(t, yaml.Source, carrierDecoder{env: map[string]string{"SOME": "from-env"}})

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("token:\n  env: SOME\n")))
	require.NoError(t, err)
	require.Equal(t, "from-env", cfg.Token)
}

func TestDecoderJSON(t *testing.T) {
	d := decoderDescriptor(t, jsonsource.Source, carrierDecoder{env: map[string]string{"SOME": "from-env"}})

	cfg, _, err := d.Resolve(jsonsource.Bytes([]byte(`{"token": {"env": "SOME"}}`)))
	require.NoError(t, err)
	require.Equal(t, "from-env", cfg.Token)

	cfg, _, err = d.Resolve(jsonsource.Bytes([]byte(`{"token": "literal"}`)))
	require.NoError(t, err)
	require.Equal(t, "literal", cfg.Token)
}

func TestDecoderRejectsUndeclaredShape(t *testing.T) {
	// An array was never declared, so it fails rather than reaching the decoder.
	called := 0
	d := decoderDescriptor(t, yaml.Source, carrierDecoder{called: &called})

	_, _, err := d.Resolve(yaml.Bytes([]byte(`token: [a, b]`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "want string or object, got array")
	require.Zero(t, called, "declaring shapes still decides what is accepted")
}

func TestDecoderErrorIsRedacted(t *testing.T) {
	d := decoderDescriptor(t, yaml.Source, carrierDecoder{})

	_, _, err := d.Resolve(yaml.Bytes([]byte("token:\n  file: /run/secrets/hunter2\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "carrier needs one of")
	require.NotContains(t, err.Error(), "hunter2")
}

func TestDecoderNullStillErases(t *testing.T) {
	called := 0
	d := decoderDescriptor(t, yaml.Source, carrierDecoder{called: &called})

	cfg, report, err := d.Resolve(
		yaml.Bytes([]byte(`token: literal`)),
		yaml.Bytes([]byte(`token: null`)),
	)
	require.NoError(t, err)
	require.Empty(t, cfg.Token, "null is a merge directive, not a value for a decoder")
	require.Equal(t, 1, called, "only the first layer reached the decoder")

	_, erased := report.ErasedBy("token")
	require.True(t, erased)
}

func TestDecoderEnvReceivesRawText(t *testing.T) {
	d := decoderDescriptor(t, env.Source, carrierDecoder{env: map[string]string{"SOME": "from-env"}})

	cfg, _, err := d.Resolve(env.Values(map[string]string{"TOKEN": "literal"}))
	require.NoError(t, err)
	require.Equal(t, "literal", cfg.Token)
}

func TestDecoderIsPerSource(t *testing.T) {
	// A decoder installed for YAML leaves the other sources alone.
	called := 0
	d := decoderDescriptor(t, yaml.Source, carrierDecoder{called: &called})

	cfg, _, err := d.Resolve(env.Values(map[string]string{"TOKEN": "plain"}))
	require.NoError(t, err)
	require.Equal(t, "plain", cfg.Token)
	require.Zero(t, called)
}

type opaqueThing struct{ A, B string }

type opaqueDecoder struct{}

func (opaqueDecoder) DecodeValue(raw any) (any, error) {
	m, _ := raw.(map[string]any)
	a, _ := m["a"].(string)
	b, _ := m["b"].(string)
	return opaqueThing{A: a, B: b}, nil
}

type opaqueConfig struct{ Thing opaqueThing }

func TestDecoderOwnsItsShape(t *testing.T) {
	// A struct whose shape belongs to its decoder needs no Object description:
	// requiring one would reject a field that resolves perfectly well.
	d, err := figureout.Derive(func(c *opaqueConfig, s *figureout.Schema[opaqueConfig]) {
		figureout.Value(s, &c.Thing, "thing",
			figureout.WithDecoder(yaml.Source, opaqueDecoder{},
				figureout.Shape{Kind: figureout.ShapeObject}))
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("thing:\n  a: x\n  b: y\n")))
	require.NoError(t, err)
	require.Equal(t, opaqueThing{A: "x", B: "y"}, cfg.Thing)
}
