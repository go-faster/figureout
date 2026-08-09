package env_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
)

type Cfg struct {
	Port    int
	Legacy  string
	Skipped string
	Tags    []string
}

func descriptor(t *testing.T) *figureout.Descriptor[Cfg] {
	t.Helper()
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port", env.Name("LISTEN_PORT"))
		figureout.Value(s, &c.Legacy, "legacy", env.Alias("OLD_NAME")).
			ApplyDefault("")
		figureout.Value(s, &c.Skipped, "skipped", env.Skip()).
			ApplyDefault("untouched")
		figureout.Value(s, &c.Tags, "tags", env.Separator(";")).
			ApplyDefault([]string{})
	})
	require.NoError(t, err)
	return d
}

func TestNamesAndAliases(t *testing.T) {
	d := descriptor(t)

	cfg, report, err := d.Resolve(env.Values(map[string]string{
		"APP_LISTEN_PORT": "8080",
		"APP_LEGACY":      "primary",
		"APP_OLD_NAME":    "alias",
		"APP_SKIPPED":     "ignored",
		"APP_TAGS":        "x;y",
	}, env.Prefix("APP_")))
	require.NoError(t, err)

	require.Equal(t, 8080, cfg.Port)
	require.Equal(t, "primary", cfg.Legacy, "the primary name wins over an alias")
	require.Equal(t, "untouched", cfg.Skipped, "skipped fields are not read")
	require.Equal(t, []string{"x", "y"}, cfg.Tags)

	origin, ok := report.OriginOf("port")
	require.True(t, ok)
	require.Equal(t, "APP_LISTEN_PORT", origin.Name)
}

func TestAliasFallback(t *testing.T) {
	d := descriptor(t)

	cfg, _, err := d.Resolve(env.Values(map[string]string{
		"LISTEN_PORT": "80",
		"OLD_NAME":    "alias",
	}))
	require.NoError(t, err)
	require.Equal(t, "alias", cfg.Legacy)
}

func TestNameCollision(t *testing.T) {
	type Colliding struct {
		Port      int
		AdminPort int
	}

	d, err := figureout.Derive(func(c *Colliding, s *figureout.Schema[Colliding]) {
		figureout.Value(s, &c.Port, "port", env.Name("PORT"))
		figureout.Value(s, &c.AdminPort, "admin_port", env.Name("PORT"))
	})
	require.NoError(t, err, "the collision is source-specific, not semantic")

	_, _, err = d.Resolve(env.Values(map[string]string{"PORT": "1"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), figureout.CodeSourceNameCollision)
	require.Contains(t, err.Error(), "environment variable PORT is assigned to both port and admin_port")
}

func TestLayerPrecedence(t *testing.T) {
	d := descriptor(t)

	cfg, _, err := d.Resolve(
		env.Values(map[string]string{"LISTEN_PORT": "1"}),
		env.Values(map[string]string{"LISTEN_PORT": "2"}),
	)
	require.NoError(t, err)
	require.Equal(t, 2, cfg.Port, "later sources override earlier ones")
}

func TestParseErrorCarriesOrigin(t *testing.T) {
	d := descriptor(t)

	_, _, err := d.Resolve(env.Values(map[string]string{"LISTEN_PORT": "eighty"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid integer "eighty"`)
	require.Contains(t, err.Error(), "env LISTEN_PORT")
}

// TestNullLiteral covers the opt-in erase spelling: environment variables have
// no null, so a field must declare the text that means one.
func TestNullLiteral(t *testing.T) {
	type Cfg struct {
		Level   string
		Comment string
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Level, "level", env.NullLiteral("null")).ApplyDefault("info")
		figureout.Value(s, &c.Comment, "comment").ApplyDefault("")
	})
	require.NoError(t, err)

	cfg, report, err := d.Resolve(
		env.Values(map[string]string{"LEVEL": "debug", "COMMENT": "kept"}),
		env.Values(map[string]string{"LEVEL": "null", "COMMENT": "null"}),
	)
	require.NoError(t, err)

	require.Equal(t, "info", cfg.Level, "erased, so the default applies")
	require.Equal(t, "null", cfg.Comment, "undeclared, so it stays an ordinary string")

	erased, ok := report.ErasedBy("level")
	require.True(t, ok)
	require.Equal(t, "LEVEL", erased.Name)
}

type Nested struct {
	Port int
}

type Outer struct {
	Server Nested
	Port   int
}

// TestNestedNameIsRelative pins that a name given inside a nested descriptor
// keeps its parent's segments. Were it absolute, the nested field would claim
// the same variable as the top-level one.
func TestNestedNameIsRelative(t *testing.T) {
	inner := figureout.MustDerive(func(c *Nested, s *figureout.Schema[Nested]) {
		figureout.Value(s, &c.Port, "port", env.Name("LISTEN_PORT"))
	})
	d, err := figureout.Derive(func(c *Outer, s *figureout.Schema[Outer]) {
		figureout.Object(s, &c.Server, "server", inner)
		figureout.Value(s, &c.Port, "port")
	})
	require.NoError(t, err)

	cfg, report, err := d.Resolve(env.Values(map[string]string{
		"APP_SERVER_LISTEN_PORT": "9090",
		"APP_PORT":               "80",
	}, env.Prefix("APP_")))
	require.NoError(t, err)

	require.Equal(t, 9090, cfg.Server.Port)
	require.Equal(t, 80, cfg.Port)

	origin, ok := report.OriginOf("server.port")
	require.True(t, ok)
	require.Equal(t, "APP_SERVER_LISTEN_PORT", origin.Name)
}

// TestNestedNameCollision shows the check the relative naming makes possible:
// two fields can only collide when they really resolve to one variable.
func TestNestedNameCollision(t *testing.T) {
	inner := figureout.MustDerive(func(c *Nested, s *figureout.Schema[Nested]) {
		figureout.Value(s, &c.Port, "port", env.Name("PORT"))
	})
	d, err := figureout.Derive(func(c *Outer, s *figureout.Schema[Outer]) {
		figureout.Object(s, &c.Server, "server", inner)
		figureout.Value(s, &c.Port, "port")
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{
		"SERVER_PORT": "9090",
		"PORT":        "80",
	}))
	require.NoError(t, err, "SERVER_PORT and PORT are distinct")
	require.Equal(t, 9090, cfg.Server.Port)
	require.Equal(t, 80, cfg.Port)
}

func TestCustomNaming(t *testing.T) {
	inner := figureout.MustDerive(func(c *Nested, s *figureout.Schema[Nested]) {
		figureout.Value(s, &c.Port, "port")
	})
	d, err := figureout.Derive(func(c *Outer, s *figureout.Schema[Outer]) {
		figureout.Object(s, &c.Server, "server", inner)
		figureout.Value(s, &c.Port, "port")
	})
	require.NoError(t, err)

	// Double underscore between levels.
	naming := func(_ *figureout.FieldModel, segments []string) []string {
		return []string{strings.ToUpper(strings.Join(segments, "__"))}
	}

	cfg, report, err := d.Resolve(env.Values(map[string]string{
		"SERVER__PORT": "9090",
		"PORT":         "80",
	}, env.Names(naming)))
	require.NoError(t, err)
	require.Equal(t, 9090, cfg.Server.Port)
	require.Equal(t, 80, cfg.Port)

	origin, ok := report.OriginOf("server.port")
	require.True(t, ok)
	require.Equal(t, "SERVER__PORT", origin.Name)
}

// TestCustomNamingCollision shows that a naming function is still checked: one
// that flattens away a level makes two fields claim the same variable.
func TestCustomNamingCollision(t *testing.T) {
	inner := figureout.MustDerive(func(c *Nested, s *figureout.Schema[Nested]) {
		figureout.Value(s, &c.Port, "port")
	})
	d, err := figureout.Derive(func(c *Outer, s *figureout.Schema[Outer]) {
		figureout.Object(s, &c.Server, "server", inner)
		figureout.Value(s, &c.Port, "port")
	})
	require.NoError(t, err)

	flatten := func(_ *figureout.FieldModel, segments []string) []string {
		return []string{strings.ToUpper(segments[len(segments)-1])}
	}

	_, _, err = d.Resolve(env.Values(map[string]string{"PORT": "80"}, env.Names(flatten)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "environment variable PORT is assigned to both server.port and port")
}

// TestEmptyVariableIsAbsent covers the shape every compose file has:
// "APP_TOKEN: ${APP_TOKEN:-}" materializes the variable whether or not an
// operator supplied a value, and an empty one must not blank the file layer.
func TestEmptyVariableIsAbsent(t *testing.T) {
	type Cfg struct {
		Token string
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Token, "token")
	})
	require.NoError(t, err)

	cfg, report, err := d.Resolve(
		env.Values(map[string]string{"TOKEN": "from-file"}),
		env.Values(map[string]string{"TOKEN": ""}),
	)
	require.NoError(t, err)
	require.Equal(t, "from-file", cfg.Token, "an empty variable is the shell saying nothing")

	_, ok := report.ErasedBy("token")
	require.False(t, ok, "an empty variable is absent, not an erase")
}

func TestEmptyVariableWithAllowEmpty(t *testing.T) {
	type Cfg struct {
		Token string
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Token, "token")
	})
	require.NoError(t, err)

	cfg, report, err := d.Resolve(
		env.Values(map[string]string{"TOKEN": "from-file"}),
		env.Values(map[string]string{"TOKEN": ""}, env.AllowEmpty()),
	)
	require.NoError(t, err)
	require.Empty(t, cfg.Token)

	origin, ok := report.OriginOf("token")
	require.True(t, ok)
	require.Equal(t, env.Source, origin.Source)
}

func TestEmptyVariableFallsThroughToAnAlias(t *testing.T) {
	cfg, _, err := descriptor(t).Resolve(env.Values(map[string]string{
		"APP_LISTEN_PORT": "8080",
		"APP_LEGACY":      "",
		"APP_OLD_NAME":    "alias",
	}, env.Prefix("APP_")))
	require.NoError(t, err)
	require.Equal(t, "alias", cfg.Legacy,
		"an empty primary name is unset, so the alias is what is set")
}

func TestEmptyVariableLeavesARequiredFieldMissing(t *testing.T) {
	type Cfg struct {
		DSN string
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Explicit(s, &c.DSN, "dsn")
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(env.Values(map[string]string{"DSN": ""}))
	require.Error(t, err, "an empty variable does not satisfy a field that must be set")
	require.Contains(t, err.Error(), "no value provided and no default")
}
