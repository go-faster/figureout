package figureout_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/yaml"
)

type movedConfig struct {
	HTTPAddr string
	Port     int
}

func movedDescriptor(t *testing.T) *figureout.Descriptor[movedConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *movedConfig, s *figureout.Schema[movedConfig]) {
		figureout.Group(s, "api", func(s *figureout.Schema[movedConfig]) {
			figureout.Value(s, &c.HTTPAddr, "http_addr",
				figureout.MovedFrom("http_addr", "legacy.addr"),
			).ApplyDefault(":8080")
		})
		figureout.Value(s, &c.Port, "port",
			figureout.Deprecated("use api.http_addr"),
		).ApplyDefault(0)
	})
	require.NoError(t, err)
	return d
}

func warnings(d figureout.Diagnostics) []string {
	var out []string
	for _, diag := range d {
		if diag.Severity == figureout.SeverityWarning {
			out = append(out, diag.FieldPath+": "+diag.Message)
		}
	}
	return out
}

func TestMovedFromAcceptsOldSpelling(t *testing.T) {
	cfg, report, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(`http_addr: ":9090"`)))
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Equal(t, []string{"http_addr: deprecated, use api.http_addr"}, warnings(report.Diagnostics))
}

func TestMovedFromAcceptsOldNesting(t *testing.T) {
	cfg, report, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(`
legacy:
  addr: ":9090"
`)))
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Equal(t, []string{"legacy.addr: deprecated, use api.http_addr"}, warnings(report.Diagnostics))
}

func TestMovedFromCurrentSpellingIsQuiet(t *testing.T) {
	cfg, report, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(`
api:
  http_addr: ":9090"
`)))
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Empty(t, report.Diagnostics)
}

func TestMovedFromBothSpellingsIsAnError(t *testing.T) {
	_, _, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(`
http_addr: ":9090"
api:
  http_addr: ":8080"
`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), figureout.CodeMovedConflict)
	require.Contains(t, err.Error(), "remove the deprecated spelling")
	require.Contains(t, err.Error(), "api.http_addr",
		"the conflict names both spellings and both origins")
}

func TestMovedFromAcrossLayers(t *testing.T) {
	// Explicitly setting the new key to its default value alongside the old one
	// is still a conflict: provenance answers "was this set?", not a comparison
	// against the default.
	_, _, err := movedDescriptor(t).Resolve(
		yaml.Bytes([]byte(`http_addr: ":9090"`)),
		env.Values(map[string]string{"API_HTTP_ADDR": ":8080"}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), figureout.CodeMovedConflict)
}

func TestMovedFromEnv(t *testing.T) {
	cfg, report, err := movedDescriptor(t).Resolve(env.Values(map[string]string{
		"HTTP_ADDR": ":9090",
	}))
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Len(t, warnings(report.Diagnostics), 1)
}

func TestDeprecatedFieldWarnsWhenSet(t *testing.T) {
	_, report, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(`port: 8080`)))
	require.NoError(t, err)
	require.Equal(t, []string{"port: use api.http_addr"}, warnings(report.Diagnostics))
}

func TestDeprecatedFieldQuietWhenUnset(t *testing.T) {
	_, report, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(`{}`)))
	require.NoError(t, err)
	require.Empty(t, report.Diagnostics, "a default is not a use of the deprecated key")
}

func TestMovedFromModel(t *testing.T) {
	m := movedDescriptor(t).Model()

	old, ok := m.FieldByPath("http_addr")
	require.True(t, ok)
	require.True(t, old.Moved())
	require.Equal(t, "api.http_addr", old.MovedTo)

	current, ok := m.FieldByPath("api.http_addr")
	require.True(t, ok)
	require.False(t, current.Moved())
	require.Equal(t, []string{"http_addr", "legacy.addr"}, current.MovedFrom)
}

func TestMovedFromSchema(t *testing.T) {
	raw, _, err := jsonschema.Generate(movedDescriptor(t), jsonschema.Semantic())
	require.NoError(t, err)

	var doc struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	require.Equal(t, true, doc.Properties["http_addr"]["deprecated"],
		"the former path is a deprecated property, not a second field")
	require.Equal(t, true, doc.Properties["legacy"]["deprecated"])
	require.NotContains(t, doc.Required, "http_addr")
	require.NotContains(t, doc.Required, "legacy")
}

func TestMovedFromCollides(t *testing.T) {
	_, err := figureout.Derive(func(c *movedConfig, s *figureout.Schema[movedConfig]) {
		figureout.Value(s, &c.HTTPAddr, "http_addr", figureout.MovedFrom("port"))
		figureout.Value(s, &c.Port, "port")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "is already a configuration property")
}

func TestMovedFromThroughNonGroup(t *testing.T) {
	type nested struct{ Addr string }
	type cfg struct {
		Server   nested
		HTTPAddr string
	}
	_, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
		figureout.ObjectFunc(s, &c.Server, "server", func(c *nested, s *figureout.Schema[nested]) {
			figureout.Value(s, &c.Addr, "addr")
		})
		figureout.Value(s, &c.HTTPAddr, "http_addr", figureout.MovedFrom("server.legacy_addr"))
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "which is not a group")
}

func TestMovedFromEmptyPath(t *testing.T) {
	_, err := figureout.Derive(func(c *movedConfig, s *figureout.Schema[movedConfig]) {
		figureout.Value(s, &c.HTTPAddr, "http_addr", figureout.MovedFrom(""))
		figureout.Value(s, &c.Port, "port")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty former path")
}
