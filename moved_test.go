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

func TestMovedFromEnvBindsTheTarget(t *testing.T) {
	// A former *file* key does not imply a former variable, so env reads the
	// field under its current name and says nothing about the old one.
	cfg, report, err := movedDescriptor(t).Resolve(env.Values(map[string]string{
		"API_HTTP_ADDR": ":9090",
	}))
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Empty(t, warnings(report.Diagnostics))

	cfg, _, err = movedDescriptor(t).Resolve(env.Values(map[string]string{
		"HTTP_ADDR": ":9090",
	}))
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTPAddr, "the shadow has no variable of its own")
}

// flatConfig is the rename MovedFrom exists for: a flat key becomes a section.
type flatConfig struct {
	Database struct{ DSN string }
}

func flatDescriptor(t *testing.T) *figureout.Descriptor[flatConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *flatConfig, s *figureout.Schema[flatConfig]) {
		figureout.Group(s, "database", func(s *figureout.Schema[flatConfig]) {
			figureout.Value(s, &c.Database.DSN, "dsn",
				figureout.MovedFrom("database_dsn")).ApplyDefault("")
		})
	})
	require.NoError(t, err)
	return d
}

func TestMovedFromEnvNoSelfCollision(t *testing.T) {
	// "database_dsn" and "database.dsn" derive the same variable by
	// construction, so a shadow that claimed one would collide with its own
	// target and refuse the descriptor outright.
	cfg, report, err := flatDescriptor(t).Resolve(env.Values(map[string]string{
		"DATABASE_DSN": "postgres://localhost",
	}))
	require.NoError(t, err)
	require.Equal(t, "postgres://localhost", cfg.Database.DSN)
	require.Empty(t, report.Diagnostics)
}

func TestMovedFromEnvAliasNamesAnOldVariable(t *testing.T) {
	// When a variable really did exist under an old name, that is what Alias is
	// for: it is an env-side fact, independent of the file-side rename.
	d, err := figureout.Derive(func(c *flatConfig, s *figureout.Schema[flatConfig]) {
		figureout.Group(s, "database", func(s *figureout.Schema[flatConfig]) {
			figureout.Value(s, &c.Database.DSN, "dsn",
				figureout.MovedFrom("database_dsn"),
				env.Alias("LEGACY_DSN"),
			).ApplyDefault("")
		})
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{"LEGACY_DSN": "postgres://old"}))
	require.NoError(t, err)
	require.Equal(t, "postgres://old", cfg.Database.DSN)
}

func TestMovedFromFileStillReadsTheOldKey(t *testing.T) {
	// The file-side deprecation is untouched: only the derived variable name
	// went away.
	cfg, report, err := flatDescriptor(t).Resolve(yaml.Bytes([]byte(`database_dsn: postgres://old`)))
	require.NoError(t, err)
	require.Equal(t, "postgres://old", cfg.Database.DSN)
	require.Equal(t, []string{"database_dsn: deprecated, use database.dsn"}, warnings(report.Diagnostics))
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

func TestMovedFromInNestedDescriptorExplainsScope(t *testing.T) {
	type api struct{ HTTPAddr string }
	type cfg struct{ API api }

	// The intent is "the old spelling was at the document root", but a former
	// path is relative to the descriptor declaring the field.
	_, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
		figureout.ObjectFunc(s, &c.API, "api", func(c *api, s *figureout.Schema[api]) {
			figureout.Value(s, &c.HTTPAddr, "http_addr",
				figureout.MovedFrom("http_addr")).ApplyDefault(":8080")
		})
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolves to the field itself")
	require.Contains(t, err.Error(), "cfg.API", "the message names the scope it resolved in")
	require.Contains(t, err.Error(), "Group", "and the idiom that does work")
}

func TestMovedFromInGroupReachesTheRoot(t *testing.T) {
	type api struct{ HTTPAddr string }
	type cfg struct{ API api }

	// Group keeps the field declared by the root schema, so a root-relative
	// former path is in scope.
	d, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
		figureout.Group(s, "api", func(s *figureout.Schema[cfg]) {
			figureout.Value(s, &c.API.HTTPAddr, "http_addr",
				figureout.MovedFrom("http_addr")).ApplyDefault(":8080")
		})
	})
	require.NoError(t, err)

	resolved, report, err := d.Resolve(yaml.Bytes([]byte(`http_addr: ":9090"`)))
	require.NoError(t, err)
	require.Equal(t, ":9090", resolved.API.HTTPAddr)
	require.Equal(t, []string{"http_addr: deprecated, use api.http_addr"}, warnings(report.Diagnostics))
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
