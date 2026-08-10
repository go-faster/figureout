package integration

import (
	"bytes"
	"strings"
	"testing"
	"time"

	validator "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/json"
)

// compileSchema generates the schema for the JSON source and compiles it.
//
// Compiling validates the document against the 2020-12 metaschema, so a schema
// we emit that is not itself valid JSON Schema fails here.
func compileSchema(t *testing.T) *validator.Schema {
	t.Helper()

	data, diags, err := jsonschema.Generate(configDescriptor,
		jsonschema.ForSource(json.Source),
		jsonschema.Title("Config"),
	)
	require.NoError(t, err)
	for _, d := range diags {
		require.NotEqual(t, figureout.SeverityError, d.Severity, d.Message)
	}

	doc, err := validator.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)

	c := validator.NewCompiler()
	require.NoError(t, c.AddResource("config.schema.json", doc))
	sch, err := c.Compile("config.schema.json")
	require.NoError(t, err, "generated schema must itself be valid JSON Schema")
	return sch
}

func validate(t *testing.T, sch *validator.Schema, doc string) error {
	t.Helper()
	inst, err := validator.UnmarshalJSON(strings.NewReader(doc))
	require.NoError(t, err)
	return sch.Validate(inst)
}

// TestDocumentDecodesAndValidates is the end-to-end claim: one document, both
// the binder and a real JSON Schema validator accept it, and the resolved
// values are what the document said.
func TestDocumentDecodesAndValidates(t *testing.T) {
	sch := compileSchema(t)
	require.NoError(t, validate(t, sch, document),
		"the generated schema must accept the document it describes")

	cfg, report, err := configDescriptor.Resolve(
		json.Bytes([]byte(document), json.DisallowUnknownFields()),
	)
	require.NoError(t, err)

	require.Equal(t, "api.example.com", cfg.Server.Address)
	require.Equal(t, 8080, cfg.Server.Port)
	require.Equal(t, "/etc/tls/cert.pem", cfg.Server.TLS.Cert)
	require.Equal(t, "/etc/tls/key.pem", cfg.Server.TLS.Key)

	require.NotNil(t, cfg.Backend.S3)
	require.Nil(t, cfg.Backend.Local)
	require.Equal(t, "configs", cfg.Backend.S3.Bucket)
	require.Equal(t, "eu-central-1", cfg.Backend.S3.Region)

	require.Equal(t, LogWarn, cfg.Level)
	timeout, ok := cfg.Timeout.Value()
	require.True(t, ok)
	require.Equal(t, 30*time.Second, timeout)
	require.Equal(t, []string{"api", "prod"}, cfg.Tags)
	require.Equal(t, map[string]int{"cpu": 2, "mem": 8}, cfg.Limits)
	require.InDelta(t, 0.25, cfg.Ratio, 0)
	require.True(t, cfg.Debug)
	require.Equal(t, "s3cret", cfg.Token)

	origin, ok := report.OriginOf("server.port")
	require.True(t, ok)
	require.Equal(t, json.Source, origin.Source)
}

// TestDefaultsValidate covers the other direction: a minimal document leaves
// the defaulted members out, and both sides still accept it.
func TestDefaultsValidate(t *testing.T) {
	const minimal = `{
  "server": {
    "address": "localhost",
    "port": 80,
    "tls": {"cert": "c", "key": "k"}
  },
  "backend": {"type": "local", "path": "/var/lib/app"}
}`

	sch := compileSchema(t)
	require.NoError(t, validate(t, sch, minimal))

	cfg, _, err := configDescriptor.Resolve(
		json.Bytes([]byte(minimal), json.DisallowUnknownFields()),
	)
	require.NoError(t, err)

	require.NotNil(t, cfg.Backend.Local)
	require.Equal(t, "/var/lib/app", cfg.Backend.Local.Path)
	require.Equal(t, LogInfo, cfg.Level)
	require.Equal(t, []string{"default"}, cfg.Tags)
	require.Equal(t, map[string]int{}, cfg.Limits)
	require.InDelta(t, 0.5, cfg.Ratio, 0)
	require.False(t, cfg.Debug)
	_, ok := cfg.Timeout.Value()
	require.False(t, ok, "an absent optional stays absent")
}

// TestSchemaAndBinderAgree is the point of generating a schema at all: an
// editor and the process that reads the file must reject the same documents.
func TestSchemaAndBinderAgree(t *testing.T) {
	sch := compileSchema(t)

	const valid = `"server": {"address": "localhost", "port": 80, "tls": {"cert": "c", "key": "k"}},
  "backend": {"type": "local", "path": "/var/lib/app"}`

	tests := []struct {
		name string
		body string
	}{
		{"PortOutOfRange", `"server": {"address": "localhost", "port": 70000, "tls": {"cert": "c", "key": "k"}},
  "backend": {"type": "local", "path": "/var/lib/app"}`},
		{"EmptyAddress", `"server": {"address": "", "port": 80, "tls": {"cert": "c", "key": "k"}},
  "backend": {"type": "local", "path": "/var/lib/app"}`},
		{"UnknownMember", valid + `, "nope": true`},
		{"UnknownLevel", valid + `, "level": "trace"`},
		{"RatioOutOfRange", valid + `, "ratio": 1.5`},
		{"TooManyTags", valid + `, "tags": ["a","b","c","d","e","f","g","h","i"]`},
		{"MissingBackendMember", `"server": {"address": "localhost", "port": 80, "tls": {"cert": "c", "key": "k"}},
  "backend": {"type": "s3"}`},
		{"MissingNestedObject", `"server": {"address": "localhost", "port": 80},
  "backend": {"type": "local", "path": "/var/lib/app"}`},
	}

	// Without this the table proves nothing: a body that is not well-formed
	// JSON would fail every case for the wrong reason.
	t.Run("ValidControl", func(t *testing.T) {
		require.NoError(t, validate(t, sch, "{\n  "+valid+"\n}"))
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := "{\n  " + tt.body + "\n}"

			require.Error(t, validate(t, sch, doc), "the schema must reject it")

			_, _, err := configDescriptor.Resolve(
				json.Bytes([]byte(doc), json.DisallowUnknownFields()),
			)
			require.Error(t, err, "the binder must reject it too")
		})
	}
}

// TestDurationFormatIsNotISO8601 documents a known gap rather than asserting
// the behavior we want.
//
// A time.Duration is emitted as "format": "duration", which JSON Schema defines
// as an RFC 3339 duration ("PT30S"). The sources accept Go's spelling ("30s"),
// so a validator configured to assert formats rejects every duration in an
// otherwise valid document. Formats are annotations by default, which is why
// this is latent rather than breaking.
func TestDurationFormatIsNotISO8601(t *testing.T) {
	data, _, err := jsonschema.Generate(configDescriptor, jsonschema.ForSource(json.Source))
	require.NoError(t, err)
	doc, err := validator.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)

	c := validator.NewCompiler()
	c.AssertFormat()
	require.NoError(t, c.AddResource("config.schema.json", doc))
	sch, err := c.Compile("config.schema.json")
	require.NoError(t, err)

	inst, err := validator.UnmarshalJSON(strings.NewReader(document))
	require.NoError(t, err)
	err = sch.Validate(inst)
	require.Error(t, err, "if this passes the format gap is closed; drop this test")
	require.Contains(t, err.Error(), "is not valid duration")
}

// TestSchemaKeyValidates is the regression this file was written for: the
// member that attaches the schema must survive both the validator and a
// binder configured to reject anything it does not know.
func TestSchemaKeyValidates(t *testing.T) {
	sch := compileSchema(t)

	require.NoError(t, validate(t, sch, document))
	require.Contains(t, document, `"$schema"`)

	_, _, err := configDescriptor.Resolve(
		json.Bytes([]byte(document), json.DisallowUnknownFields()),
	)
	require.NoError(t, err)
}
