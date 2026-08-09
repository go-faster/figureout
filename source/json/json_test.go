package json_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/json"
)

type Server struct {
	Address string
	Port    int
}

type Config struct {
	Server  Server
	Timeout figureout.OptionalOf[time.Duration]
	Tags    []string
	Limits  map[string]int
}

var serverDescriptor = figureout.MustDerive(func(c *Server, s *figureout.Schema[Server]) {
	figureout.Explicit(s, &c.Address, "address").NonEmpty()
	figureout.Explicit(s, &c.Port, "port", json.Accepts(json.Integer(), json.String())).
		InRange(1, 65535)
})

var configDescriptor = figureout.MustDerive(func(c *Config, s *figureout.Schema[Config]) {
	figureout.Object(s, &c.Server, "server", serverDescriptor)
	figureout.Optional(s, &c.Timeout, "timeout")
	figureout.Value(s, &c.Tags, "tags").ApplyDefault([]string{})
	figureout.Value(s, &c.Limits, "limits").ApplyDefault(map[string]int{})
})

const document = `{
  "server": {
    "address": "127.0.0.1",
    "port": 8080
  },
  "timeout": "5s",
  "tags": ["a", "b"],
  "limits": {"cpu": 2, "mem": 8}
}`

func TestLoad(t *testing.T) {
	cfg, report, err := configDescriptor.Resolve(json.Bytes([]byte(document)))
	require.NoError(t, err)

	require.Equal(t, "127.0.0.1", cfg.Server.Address)
	require.Equal(t, 8080, cfg.Server.Port)
	require.Equal(t, []string{"a", "b"}, cfg.Tags)
	require.Equal(t, map[string]int{"cpu": 2, "mem": 8}, cfg.Limits)

	timeout, ok := cfg.Timeout.Value()
	require.True(t, ok)
	require.Equal(t, 5*time.Second, timeout)

	origin, ok := report.OriginOf("server.port")
	require.True(t, ok)
	require.Equal(t, json.Source, origin.Source)
	require.Equal(t, "server.port", origin.Name)
	require.Equal(t, 4, origin.Line)
	require.Equal(t, 5, origin.Col)
}

func TestFileProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(document), 0o600))

	_, report, err := configDescriptor.Resolve(json.File(path))
	require.NoError(t, err)

	origin, ok := report.OriginOf("server.address")
	require.True(t, ok)
	require.Equal(t, path, origin.File)
	require.Equal(t, 3, origin.Line)
	require.Contains(t, origin.String(), "config.json:3:")
}

func TestConstraintErrorPointsAtTheValue(t *testing.T) {
	_, _, err := configDescriptor.Resolve(json.Bytes([]byte(`{
  "server": {
    "address": "localhost",
    "port": 70000
  }
}`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 65535")
	require.Contains(t, err.Error(), "json server.port")
}

// TestAcceptsString pins the declaration-driven behavior: a numeric field
// takes a JSON string only because it said it would.
func TestAcceptsString(t *testing.T) {
	cfg, _, err := configDescriptor.Resolve(json.Bytes([]byte(`{
  "server": {"address": "localhost", "port": "8080"}
}`)))
	require.NoError(t, err)
	require.Equal(t, 8080, cfg.Server.Port)

	_, _, err = configDescriptor.Resolve(json.Bytes([]byte(`{
  "server": {"address": 8080, "port": 80}
}`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "want a string, got number")
}

func TestUndeclaredStringIsRejected(t *testing.T) {
	type Cfg struct {
		Port int
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port")
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(json.Bytes([]byte(`{"port": "80"}`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "declare json.Accepts(json.String())")
}

// TestNullErasesARequiredField shows the cost of null meaning erase: a
// required field with nothing to fall back on becomes missing.
func TestNullErasesARequiredField(t *testing.T) {
	_, _, err := configDescriptor.Resolve(json.Bytes([]byte(`{
  "server": {"address": null, "port": 80}
}`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value provided and no default")
}

func TestNamesAndAliases(t *testing.T) {
	type Cfg struct {
		Port   int
		Legacy string
		Hidden string
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port", json.Name("listenPort"))
		figureout.Value(s, &c.Legacy, "legacy", json.Alias("oldName")).ApplyDefault("")
		figureout.Value(s, &c.Hidden, "hidden", json.Skip()).ApplyDefault("untouched")
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(json.Bytes([]byte(
		`{"listenPort": 1, "oldName": "alias", "hidden": "ignored"}`,
	)))
	require.NoError(t, err)
	require.Equal(t, 1, cfg.Port)
	require.Equal(t, "alias", cfg.Legacy)
	require.Equal(t, "untouched", cfg.Hidden)
}

func TestDisallowUnknownFields(t *testing.T) {
	type Cfg struct {
		Port int
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port")
	})
	require.NoError(t, err)

	body := []byte(`{"port": 1, "extra": true}`)
	_, _, err = d.Resolve(json.Bytes(body))
	require.NoError(t, err, "unknown members are accepted by default")

	_, _, err = d.Resolve(json.Bytes(body, json.DisallowUnknownFields()))
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown configuration property "extra"`)
}

func TestOptionalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")

	_, _, err := configDescriptor.Resolve(json.File(path))
	require.Error(t, err)

	type Cfg struct {
		Port int
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port").ApplyDefault(80)
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(json.File(path, json.Optional()))
	require.NoError(t, err)
	require.Equal(t, 80, cfg.Port)
}

func TestMalformedDocument(t *testing.T) {
	for _, body := range []string{`{"port": }`, `[]`, `{"a": 1} {"b": 2}`} {
		_, _, err := configDescriptor.Resolve(json.Bytes([]byte(body)))
		require.Error(t, err, body)
	}
}
