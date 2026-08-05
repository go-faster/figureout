package figureout_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

type secretConfig struct {
	Token   string
	Retries int
}

func secretDescriptor(t *testing.T) *figureout.Descriptor[secretConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *secretConfig, s *figureout.Schema[secretConfig]) {
		figureout.Value(s, &c.Token, "token", figureout.Secret()).
			Pattern(`^sk-[a-z0-9]+$`)
		figureout.Value(s, &c.Retries, "retries").ApplyDefault(3)
	})
	require.NoError(t, err)
	return d
}

func TestSecretRedactsConstraintFailures(t *testing.T) {
	_, report, err := secretDescriptor(t).Resolve(yaml.Bytes([]byte(`token: hunter2`)))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hunter2", "a constraint failure never prints the secret")
	require.Contains(t, err.Error(), "must match")
	require.NotContains(t, report.Diagnostics[0].Message, "hunter2")
}

func TestSecretRedactsEnumFailures(t *testing.T) {
	d, err := figureout.Derive(func(c *secretConfig, s *figureout.Schema[secretConfig]) {
		figureout.Value(s, &c.Token, "token", figureout.Secret()).Enum("sk-a", "sk-b")
		figureout.Value(s, &c.Retries, "retries").ApplyDefault(3)
	})
	require.NoError(t, err)

	_, _, resolveErr := d.Resolve(yaml.Bytes([]byte(`token: hunter2`)))
	require.Error(t, resolveErr)
	require.NotContains(t, resolveErr.Error(), "hunter2")
	require.Contains(t, resolveErr.Error(), figureout.Redacted)
}

func TestSecretRedactsSourceFailures(t *testing.T) {
	type c struct{ Port int }
	d, err := figureout.Derive(func(cfg *c, s *figureout.Schema[c]) {
		figureout.Value(s, &cfg.Port, "port", figureout.Secret())
	})
	require.NoError(t, err)

	_, _, resolveErr := d.Resolve(env.Values(map[string]string{"PORT": "hunter2"}))
	require.Error(t, resolveErr)
	require.NotContains(t, resolveErr.Error(), "hunter2",
		"a decoding failure quotes what it could not read")

	_, _, resolveErr = d.Resolve(yaml.Bytes([]byte(`port: hunter2`)))
	require.Error(t, resolveErr)
	require.NotContains(t, resolveErr.Error(), "hunter2")
}

func TestSecretMarksTheReport(t *testing.T) {
	cfg, report, err := secretDescriptor(t).Resolve(yaml.Bytes([]byte(`token: sk-abc123`)))
	require.NoError(t, err)
	require.Equal(t, "sk-abc123", cfg.Token)

	require.True(t, report.Secret("token"))
	require.False(t, report.Secret("retries"))
	require.Equal(t, []string{"token"}, slices.Sorted(report.Secrets()))
}

func TestSecretImpliesHidden(t *testing.T) {
	f, ok := secretDescriptor(t).Model().FieldByPath("token")
	require.True(t, ok)
	require.True(t, f.Meta.Secret)
	require.True(t, f.Meta.Hidden)
}

func TestSecretSchema(t *testing.T) {
	raw, _, err := jsonschema.Generate(secretDescriptor(t), jsonschema.Semantic())
	require.NoError(t, err)

	var doc struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Equal(t, true, doc.Properties["token"]["writeOnly"])
	require.NotContains(t, doc.Properties["retries"], "writeOnly")
}

func TestRedactLeavesPlainFieldsAlone(t *testing.T) {
	f, ok := secretDescriptor(t).Model().FieldByPath("retries")
	require.True(t, ok)
	require.Equal(t, `invalid integer "abc"`, figureout.Redact(f, `invalid integer "abc"`, "abc"))
}
