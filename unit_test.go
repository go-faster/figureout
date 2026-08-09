package figureout_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	jsonsource "github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

type unitConfig struct {
	Timeout      time.Duration
	Lease        time.Duration
	PollInterval time.Duration
	Maintenance  time.Duration
}

func unitDescriptor(t *testing.T) *figureout.Descriptor[unitConfig] {
	t.Helper()
	d, err := figureout.Derive(func(c *unitConfig, s *figureout.Schema[unitConfig]) {
		figureout.Explicit(s, &c.Timeout, "timeout_seconds", figureout.Unit(time.Second)).
			Doc("How long a request may take.").
			AtLeast(time.Second).
			AtMost(10 * time.Minute)
		figureout.Value(s, &c.Lease, "lease_seconds", figureout.Unit(time.Second)).
			ApplyDefault(5 * time.Minute)
		figureout.Value(s, &c.PollInterval, "poll_interval_ms", figureout.Unit(time.Millisecond)).
			ApplyDefault(250 * time.Millisecond)
		figureout.Value(s, &c.Maintenance, "maintenance").
			ApplyDefault(time.Hour)
	})
	require.NoError(t, err)
	return d
}

func TestUnitYAML(t *testing.T) {
	cfg, _, err := unitDescriptor(t).Resolve(yaml.Bytes([]byte(`
timeout_seconds: 180
lease_seconds: 300
poll_interval_ms: 1500
maintenance: 2h
`)))
	require.NoError(t, err)
	require.Equal(t, 180*time.Second, cfg.Timeout)
	require.Equal(t, 5*time.Minute, cfg.Lease)
	require.Equal(t, 1500*time.Millisecond, cfg.PollInterval)
	require.Equal(t, 2*time.Hour, cfg.Maintenance)
}

func TestUnitYAMLDurationSpelling(t *testing.T) {
	// The duration spelling stays accepted, so a rename is an alias change.
	cfg, _, err := unitDescriptor(t).Resolve(yaml.Bytes([]byte(`
timeout_seconds: 3m
`)))
	require.NoError(t, err)
	require.Equal(t, 3*time.Minute, cfg.Timeout)
}

func TestUnitJSON(t *testing.T) {
	cfg, _, err := unitDescriptor(t).Resolve(jsonsource.Bytes([]byte(
		`{"timeout_seconds": 180, "poll_interval_ms": 1500, "maintenance": "2h"}`,
	)))
	require.NoError(t, err)
	require.Equal(t, 180*time.Second, cfg.Timeout)
	require.Equal(t, 1500*time.Millisecond, cfg.PollInterval)
	require.Equal(t, 2*time.Hour, cfg.Maintenance)
}

func TestUnitJSONDurationSpelling(t *testing.T) {
	cfg, _, err := unitDescriptor(t).Resolve(jsonsource.Bytes([]byte(
		`{"timeout_seconds": "3m"}`,
	)))
	require.NoError(t, err)
	require.Equal(t, 3*time.Minute, cfg.Timeout)
}

func TestUnitEnv(t *testing.T) {
	cfg, _, err := unitDescriptor(t).Resolve(env.Values(map[string]string{
		"TIMEOUT_SECONDS": "180",
	}))
	require.NoError(t, err)
	require.Equal(t, 180*time.Second, cfg.Timeout)
}

func TestUnitConstraint(t *testing.T) {
	// Constraints stay typed as time.Duration, so the bound is 1s, not 1ns.
	_, _, err := unitDescriptor(t).Resolve(yaml.Bytes([]byte(`timeout_seconds: 0`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 1s")
}

func TestUnitInvalid(t *testing.T) {
	_, _, err := unitDescriptor(t).Resolve(yaml.Bytes([]byte(`timeout_seconds: half`)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "want a number of seconds")
}

func TestUnitRejectsNonDuration(t *testing.T) {
	_, err := figureout.Derive(func(c *unitConfig, s *figureout.Schema[unitConfig]) {
		figureout.Value(s, &c.Timeout, "timeout_seconds", figureout.Unit(time.Second))
		figureout.Value(s, &c.Lease, "lease_seconds")
		figureout.Value(s, &c.PollInterval, "poll")
		figureout.Value(s, &c.Maintenance, "maintenance", figureout.Unit(0))
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unit must be positive")
}

func TestUnitRejectsNonDurationKind(t *testing.T) {
	type c struct{ N int }
	_, err := figureout.Derive(func(cfg *c, s *figureout.Schema[c]) {
		figureout.Value(s, &cfg.N, "n", figureout.Unit(time.Second))
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "a unit applies to a duration field, not to a integer one")
}

func TestUnitSchema(t *testing.T) {
	raw, diags, err := jsonschema.Generate(unitDescriptor(t), jsonschema.Semantic())
	require.NoError(t, err)
	require.Empty(t, diags, "a unit-scaled duration carries representable bounds")

	var doc struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	timeout := doc.Properties["timeout_seconds"]
	require.Equal(t, "integer", timeout["type"])
	require.NotContains(t, timeout, "format")
	require.Equal(t, "How long a request may take. In seconds.", timeout["description"])
	require.EqualValues(t, 1, timeout["minimum"])
	require.EqualValues(t, 600, timeout["maximum"])

	require.EqualValues(t, 300, doc.Properties["lease_seconds"]["default"])
	require.Equal(t, "In milliseconds.", doc.Properties["poll_interval_ms"]["description"])
	require.EqualValues(t, 250, doc.Properties["poll_interval_ms"]["default"])

	// A duration without a unit is still a string, and its default is spelled
	// the way a source would accept it.
	maintenance := doc.Properties["maintenance"]
	require.Equal(t, "string", maintenance["type"])
	require.Equal(t, "duration", maintenance["format"])
	require.Equal(t, "1h0m0s", maintenance["default"])
}
