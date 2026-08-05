package figureout_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	jsonsource "github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

// secretValue is the "a scalar, or an object" idiom: a token written inline, or
// an object saying where to read it from.
type secretValue struct {
	Value string
	File  string
}

type shorthandConfig struct {
	AuthToken secretValue
	Retries   int
}

var secretValueDescriptor = figureout.MustDerive(
	func(c *secretValue, s *figureout.Schema[secretValue]) {
		figureout.Value(s, &c.Value, "value", figureout.Secret()).ApplyDefault("")
		figureout.Value(s, &c.File, "file").ApplyDefault("")
	},
)

func shorthandDescriptor(t *testing.T) *figureout.Descriptor[shorthandConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *shorthandConfig, s *figureout.Schema[shorthandConfig]) {
		figureout.ScalarOr(s, &c.AuthToken, "auth_token", secretValueDescriptor,
			func(v string) secretValue { return secretValue{Value: v} })
		figureout.Value(s, &c.Retries, "retries").ApplyDefault(3)
	})
	require.NoError(t, err)
	return d
}

func TestScalarOrScalarSpelling(t *testing.T) {
	cfg, report, err := shorthandDescriptor(t).Resolve(yaml.Bytes([]byte(`auth_token: sk-live-abc`)))
	require.NoError(t, err)
	require.Equal(t, secretValue{Value: "sk-live-abc"}, cfg.AuthToken)

	origin, ok := report.OriginOf("auth_token")
	require.True(t, ok)
	require.Equal(t, 1, origin.Line)
}

func TestScalarOrObjectSpelling(t *testing.T) {
	cfg, _, err := shorthandDescriptor(t).Resolve(yaml.Bytes([]byte(`
auth_token:
  file: /run/secrets/token
`)))
	require.NoError(t, err)
	require.Equal(t, secretValue{File: "/run/secrets/token"}, cfg.AuthToken)
}

func TestScalarOrJSON(t *testing.T) {
	cfg, _, err := shorthandDescriptor(t).Resolve(jsonsource.Bytes([]byte(`{"auth_token": "sk-live-abc"}`)))
	require.NoError(t, err)
	require.Equal(t, secretValue{Value: "sk-live-abc"}, cfg.AuthToken)

	cfg, _, err = shorthandDescriptor(t).Resolve(jsonsource.Bytes([]byte(`{"auth_token": {"file": "/x"}}`)))
	require.NoError(t, err)
	require.Equal(t, secretValue{File: "/x"}, cfg.AuthToken)
}

func TestScalarOrEnv(t *testing.T) {
	// The environment has no object syntax, so the scalar spelling binds at the
	// object's own name; its members still bind under it.
	cfg, _, err := shorthandDescriptor(t).Resolve(env.Values(map[string]string{
		"AUTH_TOKEN": "sk-live-abc",
	}))
	require.NoError(t, err)
	require.Equal(t, secretValue{Value: "sk-live-abc"}, cfg.AuthToken)

	cfg, _, err = shorthandDescriptor(t).Resolve(env.Values(map[string]string{
		"AUTH_TOKEN_FILE": "/run/secrets/token",
	}))
	require.NoError(t, err)
	require.Equal(t, secretValue{File: "/run/secrets/token"}, cfg.AuthToken)
}

func TestScalarOrLaterSpellingReplaces(t *testing.T) {
	// A widened scalar stands for the whole object, so the two spellings never
	// half-merge: the later layer wins outright.
	cfg, _, err := shorthandDescriptor(t).Resolve(
		yaml.Bytes([]byte("auth_token:\n  file: /run/secrets/token\n")),
		env.Values(map[string]string{"AUTH_TOKEN": "sk-live-abc"}),
	)
	require.NoError(t, err)
	require.Equal(t, secretValue{Value: "sk-live-abc"}, cfg.AuthToken)

	cfg, _, err = shorthandDescriptor(t).Resolve(
		yaml.Bytes([]byte("auth_token: sk-live-abc\n")),
		env.Values(map[string]string{"AUTH_TOKEN_FILE": "/run/secrets/token"}),
	)
	require.NoError(t, err)
	require.Equal(t, secretValue{File: "/run/secrets/token"}, cfg.AuthToken)
}

func TestScalarOrModel(t *testing.T) {
	f, ok := shorthandDescriptor(t).Model().FieldByPath("auth_token")
	require.True(t, ok)

	scalar, ok := f.Shorthand()
	require.True(t, ok)
	require.Equal(t, figureout.TypeString, scalar.Kind)
	require.Len(t, f.Type.Object.Fields, 2, "the object half is still the descriptor")
}

func TestScalarOrSchema(t *testing.T) {
	raw, _, err := jsonschema.Generate(shorthandDescriptor(t), jsonschema.Semantic())
	require.NoError(t, err)

	var doc struct {
		Properties map[string]struct {
			OneOf []map[string]any `json:"oneOf"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	alternatives := doc.Properties["auth_token"].OneOf
	require.Len(t, alternatives, 2, "the shapes come from the descriptor, so they cannot drift")
	require.Equal(t, "string", alternatives[0]["type"])
	require.Equal(t, "object", alternatives[1]["type"])
	require.Contains(t, alternatives[1]["properties"], "file")
}

func TestScalarOrRejectsAnObjectAlternative(t *testing.T) {
	_, err := figureout.Derive(func(c *shorthandConfig, s *figureout.Schema[shorthandConfig]) {
		figureout.ScalarOr(s, &c.AuthToken, "auth_token", secretValueDescriptor,
			func(v secretValue) secretValue { return v })
		figureout.Value(s, &c.Retries, "retries")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be a scalar, not a object")
}

func TestScalarOrRejectsNilWidening(t *testing.T) {
	_, err := figureout.Derive(func(c *shorthandConfig, s *figureout.Schema[shorthandConfig]) {
		figureout.ScalarOr[shorthandConfig, secretValue, string](
			s, &c.AuthToken, "auth_token", secretValueDescriptor, nil)
		figureout.Value(s, &c.Retries, "retries")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil widening function")
}

func TestScalarOrWrongScalarKind(t *testing.T) {
	_, _, err := shorthandDescriptor(t).Resolve(yaml.Bytes([]byte(`auth_token: [a, b]`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "want a string")
}
