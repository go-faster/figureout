package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/docs"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

type embedConfig struct {
	DSN       string
	Collector map[string]any
}

func describeEmbed(c *embedConfig, s *figureout.Schema[embedConfig]) {
	figureout.Value(s, &c.DSN, "dsn")
	figureout.Opaque(s, &c.Collector, "otelcol",
		figureout.Reason("handed to the OpenTelemetry Collector verbatim"))
}

// TestOpaqueCarriesTheSubtree covers the whole point: what the document held is
// what the field gets, structure and scalar spellings included.
func TestOpaqueCarriesTheSubtree(t *testing.T) {
	d, err := figureout.Derive(describeEmbed)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte(`
dsn: clickhouse://localhost
otelcol:
  receivers:
    otlp:
      protocols:
        grpc:
          endpoint: 0.0.0.0:4317
          max_recv_msg_size_mib: 512
  service:
    pipelines: [traces, metrics]
    telemetry: null
`)))
	require.NoError(t, err)
	require.Equal(t, "clickhouse://localhost", cfg.DSN)
	require.Equal(t, map[string]any{
		"receivers": map[string]any{
			"otlp": map[string]any{
				"protocols": map[string]any{
					"grpc": map[string]any{
						"endpoint":              "0.0.0.0:4317",
						"max_recv_msg_size_mib": 512,
					},
				},
			},
		},
		"service": map[string]any{
			"pipelines": []any{"traces", "metrics"},
			"telemetry": nil,
		},
	}, cfg.Collector, "scalars keep the type the format resolved them to")
}

// TestOpaqueKeepsTheScalarSpelling guards the difference a passthrough would
// otherwise lose: a quoted number is a string, and an unquoted one is not.
func TestOpaqueKeepsTheScalarSpelling(t *testing.T) {
	d, err := figureout.Derive(describeEmbed)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("otelcol:\n  a: 512\n  b: \"512\"\n  c: true\n  d: 1.5\n")))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": 512, "b": "512", "c": true, "d": 1.5}, cfg.Collector)

	cfg, _, err = d.Resolve(json.Bytes([]byte(`{"otelcol": {"a": 512, "b": "512", "c": true, "d": 1.5}}`)))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": int64(512), "b": "512", "c": true, "d": 1.5}, cfg.Collector)
}

// TestOpaqueKeepsTheMapType covers the structure half of the same promise. YAML
// gives an "any" a map[string]any only while every key of a mapping is a
// string, and a passthrough is handed to a program that will read it as YAML.
//
// Found by a differential fuzzer in an adopting repository, on "0000:" — which
// YAML resolves as the integer zero, not as a name.
func TestOpaqueKeepsTheMapType(t *testing.T) {
	d, err := figureout.Derive(describeEmbed)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("otelcol:\n  nested:\n    0000: value\n")))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"nested": map[any]any{0: "value"},
	}, cfg.Collector, "an integer key is not a name")

	cfg, _, err = d.Resolve(yaml.Bytes([]byte("otelcol:\n  nested:\n    \"0000\": value\n")))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"nested": map[string]any{"0000": "value"},
	}, cfg.Collector, "a quoted one is")

	// A JSON object is string-keyed by construction, so it never chooses.
	cfg, _, err = d.Resolve(json.Bytes([]byte(`{"otelcol": {"nested": {"0000": "value"}}}`)))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"nested": map[string]any{"0000": "value"},
	}, cfg.Collector)
}

// TestOpaqueIsExemptFromUnknownFields is the load-bearing half: strictness stops
// at the passthrough and nowhere else.
func TestOpaqueIsExemptFromUnknownFields(t *testing.T) {
	d, err := figureout.Derive(describeEmbed)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes(
		[]byte("otelcol:\n  whatever_the_collector_grew: {this_week: true}\n"),
		yaml.DisallowUnknownFields(),
	))
	require.NoError(t, err, "the keys under a passthrough belong to another program")
	require.Len(t, cfg.Collector, 1)

	_, _, err = d.Resolve(yaml.Bytes(
		[]byte("otelcol: {}\nnot_a_field: 1\n"),
		yaml.DisallowUnknownFields(),
	))
	require.Error(t, err, "the hole is the passthrough, not the document")
	require.Contains(t, err.Error(), `unknown configuration property "not_a_field"`)
}

// TestOpaqueAbsentAndErased covers the two ways a passthrough carries nothing.
func TestOpaqueAbsentAndErased(t *testing.T) {
	d, err := figureout.Derive(describeEmbed)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("dsn: x\n")))
	require.NoError(t, err)
	require.Nil(t, cfg.Collector)

	cfg, _, err = d.Resolve(
		yaml.Bytes([]byte("otelcol:\n  receivers: {}\n")),
		yaml.Bytes([]byte("otelcol: null\n")),
	)
	require.NoError(t, err)
	require.Nil(t, cfg.Collector, "null erases the block rather than emptying it")
}

// TestOpaqueRejectsAValueItCannotHold covers the boundary the Go type still
// draws: a passthrough describes nothing, but it does not accept anything.
func TestOpaqueRejectsAValueItCannotHold(t *testing.T) {
	d, err := figureout.Derive(describeEmbed)
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("otelcol: a string\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "otelcol")
	require.Contains(t, err.Error(), "map[string]interface {}")
}

// TestOpaqueIntoAny covers a field with no shape of its own at all.
func TestOpaqueIntoAny(t *testing.T) {
	type Cfg struct {
		Block any
	}
	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Opaque(s, &c.Block, "block", figureout.Reason("another program's"))
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("block: [1, two]\n")))
	require.NoError(t, err)
	require.Equal(t, []any{1, "two"}, cfg.Block)

	cfg, _, err = d.Resolve(yaml.Bytes([]byte("{}\n")))
	require.NoError(t, err)
	require.Nil(t, cfg.Block)
}

// TestOpaqueNeedsAReason is what keeps a passthrough from being reached for
// absent-mindedly: it opts a whole subtree out of the checking that is the
// reason to adopt a descriptor, so the declaration has to say why.
func TestOpaqueNeedsAReason(t *testing.T) {
	_, err := figureout.Derive(func(c *embedConfig, s *figureout.Schema[embedConfig]) {
		figureout.Value(s, &c.DSN, "dsn")
		figureout.Opaque(s, &c.Collector, "otelcol")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "needs a Reason")
	require.Contains(t, err.Error(), "unknown-field checking")
}

// TestReasonOnADescribedField covers the other side: a reason says why a field
// is not described, so it means nothing on one that is.
func TestReasonOnADescribedField(t *testing.T) {
	_, err := figureout.Derive(func(c *embedConfig, s *figureout.Schema[embedConfig]) {
		figureout.Value(s, &c.DSN, "dsn", figureout.Reason("because"))
		figureout.Opaque(s, &c.Collector, "otelcol", figureout.Reason("verbatim"))
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "use Doc")
}

// TestOpaqueSkippedByFlatSources covers a source with no nesting, which has no
// way to spell a subtree and does not invent one.
func TestOpaqueSkippedByFlatSources(t *testing.T) {
	d, err := figureout.Derive(describeEmbed)
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{"DSN": "x", "OTELCOL": "nonsense"}))
	require.NoError(t, err)
	require.Equal(t, "x", cfg.DSN)
	require.Nil(t, cfg.Collector)
}

// TestOpaqueRequired covers the opt-in: a block the program cannot start
// without is still a block it cannot describe.
func TestOpaqueRequired(t *testing.T) {
	d, err := figureout.Derive(func(c *embedConfig, s *figureout.Schema[embedConfig]) {
		figureout.Value(s, &c.DSN, "dsn")
		figureout.Opaque(s, &c.Collector, "otelcol", figureout.Reason("verbatim")).Required()
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("dsn: x\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value provided and no default")
}

// TestOpaqueInSchemas covers both targets: a passthrough is described as the
// permissive thing it is, not omitted and not claimed to be checked.
func TestOpaqueInSchemas(t *testing.T) {
	d, err := figureout.Derive(describeEmbed)
	require.NoError(t, err)

	out, diags, err := jsonschema.Generate(d)
	require.NoError(t, err)
	require.Empty(t, diags)
	require.Contains(t, string(out), `"otelcol"`)
	require.Contains(t, string(out), "handed to the OpenTelemetry Collector verbatim")
	require.NotContains(t, string(out), `"receivers"`)

	page, _, err := docs.Generate(d, docs.ForSource(yaml.Bytes(nil)))
	require.NoError(t, err)
	require.Contains(t, string(page), "passthrough")
	require.Contains(t, string(page), "handed to the OpenTelemetry Collector verbatim")
}
