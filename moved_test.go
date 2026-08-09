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

// movedSecret is the shape a moved credential has in a real deployment: a
// scalar, or the object spelling that says where to read it from.
type movedSecret struct{ Value, File string }

var movedSecretDescriptor = figureout.MustDerive(
	func(c *movedSecret, s *figureout.Schema[movedSecret]) {
		figureout.Value(s, &c.Value, "value", figureout.Secret()).ApplyDefault("")
		figureout.Value(s, &c.File, "file").ApplyDefault("")
	},
)

type movedSecretConfig struct {
	Database struct{ DSN movedSecret }
}

func movedSecretDescriptorFor(t *testing.T) *figureout.Descriptor[movedSecretConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *movedSecretConfig, s *figureout.Schema[movedSecretConfig]) {
		figureout.Group(s, "database", func(s *figureout.Schema[movedSecretConfig]) {
			figureout.ScalarOr(s, &c.Database.DSN, "dsn", movedSecretDescriptor,
				func(v string) movedSecret { return movedSecret{Value: v} },
				figureout.MovedFrom("database_dsn"))
		})
	})
	require.NoError(t, err)
	return d
}

func TestMovedFromKeepsTheTargetsOwnPaths(t *testing.T) {
	// A shadow borrows its target's models by pointer; walking them would
	// re-path the target's members under the former name.
	m := movedSecretDescriptorFor(t).Model()

	f, ok := m.FieldByPath("database.dsn.value")
	require.True(t, ok, "the target's members keep their own paths")
	require.Equal(t, "database.dsn.value", f.Path)

	_, ok = m.FieldByPath("database_dsn.value")
	require.True(t, ok, "and the former path is findable too")
}

func TestMovedFromRedirectsTheObjectSpelling(t *testing.T) {
	// The object spelling used to vanish without a value or a diagnostic, which
	// for a moved credential meant dropping it on the floor.
	cfg, report, err := movedSecretDescriptorFor(t).Resolve(
		yaml.Bytes([]byte("database_dsn:\n  value: postgres://old\n")))
	require.NoError(t, err)
	require.Equal(t, movedSecret{Value: "postgres://old"}, cfg.Database.DSN)
	require.Equal(t, []string{"database_dsn: deprecated, use database.dsn"},
		warnings(report.Diagnostics))

	origin, ok := report.OriginOf("database.dsn.value")
	require.True(t, ok, "the redirected value keeps its provenance")
	require.Equal(t, 2, origin.Line)
}

func TestMovedFromRedirectsTheScalarSpelling(t *testing.T) {
	cfg, report, err := movedSecretDescriptorFor(t).Resolve(
		yaml.Bytes([]byte(`database_dsn: postgres://old`)))
	require.NoError(t, err)
	require.Equal(t, movedSecret{Value: "postgres://old"}, cfg.Database.DSN)
	require.Len(t, warnings(report.Diagnostics), 1)
}

func TestMovedFromCurrentObjectSpelling(t *testing.T) {
	cfg, report, err := movedSecretDescriptorFor(t).Resolve(
		yaml.Bytes([]byte("database:\n  dsn:\n    value: x\n")))
	require.NoError(t, err)
	require.Equal(t, movedSecret{Value: "x"}, cfg.Database.DSN)
	require.Empty(t, report.Diagnostics)
}

func TestMovedFromConflictAcrossSpellings(t *testing.T) {
	// Both spellings set is an error however either one is written.
	for _, tc := range []struct{ name, doc string }{
		{"both objects", "database_dsn:\n  value: old\ndatabase:\n  dsn:\n    value: new\n"},
		{"old object, new scalar", "database_dsn:\n  value: old\ndatabase:\n  dsn: new\n"},
		{"old scalar, new object", "database_dsn: old\ndatabase:\n  dsn:\n    value: new\n"},
		{"both scalars", "database_dsn: old\ndatabase:\n  dsn: new\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := movedSecretDescriptorFor(t).Resolve(yaml.Bytes([]byte(tc.doc)))
			require.Error(t, err)
			require.Contains(t, err.Error(), figureout.CodeMovedConflict)
		})
	}
}

func TestMovedFromRedirectsANestedObject(t *testing.T) {
	type endpoint struct{ Host, Port string }
	type cfg struct{ API endpoint }

	d, err := figureout.Derive(func(c *cfg, s *figureout.Schema[cfg]) {
		figureout.ObjectFunc(s, &c.API, "api", func(c *endpoint, s *figureout.Schema[endpoint]) {
			figureout.Value(s, &c.Host, "host").ApplyDefault("")
			figureout.Value(s, &c.Port, "port").ApplyDefault("")
		}, figureout.MovedFrom("server"))
	})
	require.NoError(t, err)

	resolved, report, err := d.Resolve(yaml.Bytes([]byte("server:\n  host: h\n  port: p\n")))
	require.NoError(t, err)
	require.Equal(t, endpoint{Host: "h", Port: "p"}, resolved.API,
		"every member of a moved object comes across")
	require.Len(t, warnings(report.Diagnostics), 1)
}

// diagnosticOf returns the single diagnostic carrying code.
func diagnosticOf(t *testing.T, ds figureout.Diagnostics, code string) figureout.Diagnostic {
	t.Helper()
	var found []figureout.Diagnostic
	for _, d := range ds {
		if d.Code == code {
			found = append(found, d)
		}
	}
	require.Len(t, found, 1, "exactly one %s diagnostic", code)
	return found[0]
}

// TestDeprecationCarriesTheTargetPath covers the machine-readable half of a
// deprecation: an application that phrases its own warnings reads the target
// rather than cutting a prefix off the message.
func TestDeprecationCarriesTheTargetPath(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  string
		from string
	}{
		{"flat", `http_addr: ":9090"`, "http_addr"},
		{"nested", "legacy:\n  addr: \":9090\"\n", "legacy.addr"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, report, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(tt.doc)))
			require.NoError(t, err)

			d := diagnosticOf(t, report.Diagnostics, figureout.CodeDeprecated)
			require.Equal(t, tt.from, d.FieldPath)
			require.Equal(t, "api.http_addr", d.MovedTo)
		})
	}
}

func TestMovedConflictCarriesTheTargetPath(t *testing.T) {
	_, report, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(`
http_addr: ":9090"
api:
  http_addr: ":8080"
`)))
	require.Error(t, err)

	d := diagnosticOf(t, report.Diagnostics, figureout.CodeMovedConflict)
	require.Equal(t, "http_addr", d.FieldPath)
	require.Equal(t, "api.http_addr", d.MovedTo,
		"both spellings are data, so an application can say set one of them in its own words")
}

func TestDeprecationWithoutAMoveHasNoTarget(t *testing.T) {
	_, report, err := movedDescriptor(t).Resolve(yaml.Bytes([]byte(`port: 8080`)))
	require.NoError(t, err)

	d := diagnosticOf(t, report.Diagnostics, figureout.CodeDeprecated)
	require.Equal(t, "port", d.FieldPath)
	require.Empty(t, d.MovedTo, "a deprecation with no replacement has nothing to point at")
}
