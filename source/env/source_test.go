package env_test

import (
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
		figureout.Int(s, &c.Port, "port", env.Name("LISTEN_PORT"))
		figureout.String(s, &c.Legacy, "legacy", env.Alias("OLD_NAME")).
			ApplyDefault("")
		figureout.String(s, &c.Skipped, "skipped", env.Skip()).
			ApplyDefault("untouched")
		figureout.List(s, &c.Tags, "tags", env.Separator(";")).
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
		figureout.Int(s, &c.Port, "port", env.Name("PORT"))
		figureout.Int(s, &c.AdminPort, "admin_port", env.Name("PORT"))
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
