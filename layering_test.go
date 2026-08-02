package figureout_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

// TestLayering resolves one descriptor from a file and the environment
// together: later sources win, and the report says which one supplied each
// value.
func TestLayering(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte(`server:
  address: 127.0.0.1
  port: 8080
level: warn
tags:
  - from-file
`), 0o600))

	cfg, report, err := configDescriptor.Resolve(
		yaml.File(file),
		env.Values(map[string]string{
			"APP_PORT":  "9090",
			"APP_LEVEL": "error",
		}, env.Prefix("APP_")),
	)
	require.NoError(t, err)

	require.Equal(t, "127.0.0.1", cfg.Server.Address, "only the file supplies it")
	require.Equal(t, 9090, cfg.Server.Port, "the environment wins over the file")
	require.Equal(t, LogError, cfg.Level)
	require.Equal(t, []string{"from-file"}, cfg.Tags)

	address, ok := report.OriginOf("server.address")
	require.True(t, ok)
	require.Equal(t, yaml.Source, address.Source)
	require.Equal(t, file, address.File)
	require.Equal(t, 2, address.Line)

	port, ok := report.OriginOf("server.port")
	require.True(t, ok)
	require.Equal(t, env.Source, port.Source)
	require.Equal(t, "APP_PORT", port.Name)
}

// TestSameDocumentAcrossFormats pins that JSON and YAML agree on the semantic
// result while remaining separate adapters.
func TestSameDocumentAcrossFormats(t *testing.T) {
	fromJSON, _, err := configDescriptor.Resolve(json.Bytes([]byte(`{
  "server": {"address": "localhost", "port": 80},
  "timeout": "1m",
  "level": "debug",
  "tags": ["a"]
}`)))
	require.NoError(t, err)

	fromYAML, _, err := configDescriptor.Resolve(yaml.Bytes([]byte(`server:
  address: localhost
  port: 80
timeout: 1m
level: debug
tags: [a]
`)))
	require.NoError(t, err)

	require.Equal(t, fromJSON, fromYAML)
}

// TestValidationRunsAfterMerge shows that constraints apply to the merged
// value, not to each layer in isolation.
func TestValidationRunsAfterMerge(t *testing.T) {
	_, _, err := configDescriptor.Resolve(
		json.Bytes([]byte(`{"server": {"address": "localhost", "port": 80}, "level": "info"}`)),
		env.Values(map[string]string{"PORT": "70000"}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 65535")
	require.Contains(t, err.Error(), "env PORT", "the failing layer is named")

	_, _, err = configDescriptor.Resolve(
		json.Bytes([]byte(`{"server": {"address": "localhost", "port": 70000}, "level": "info"}`)),
		env.Values(map[string]string{"PORT": "80"}),
	)
	require.NoError(t, err, "an overridden bad value never reaches validation")
}
