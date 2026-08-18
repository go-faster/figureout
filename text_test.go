package figureout_test

import (
	"strings"
	"testing"

	"github.com/go-faster/errors"
	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

// level is a named integer whose spellings are words, modeled on zapcore.Level.
// Its underlying kind admits 1; the type does not.
type level int8

const (
	levelDebug level = iota - 1
	levelInfo
	levelWarn
)

// UnmarshalText implements [encoding.TextUnmarshaler].
func (l *level) UnmarshalText(text []byte) error {
	switch string(text) {
	case "debug":
		*l = levelDebug
	case "info":
		*l = levelInfo
	case "warn":
		*l = levelWarn
	default:
		return errors.Errorf("unrecognized level %q", text)
	}
	return nil
}

// byteSize is a named integer accepting both a bare count and a suffixed one,
// modeled on xbytes.Bytes.
type byteSize int64

// UnmarshalText implements [encoding.TextUnmarshaler].
func (b *byteSize) UnmarshalText(text []byte) error {
	s := string(text)
	scale := int64(1)
	switch {
	case strings.HasSuffix(s, "MiB"):
		s, scale = strings.TrimSuffix(s, "MiB"), 1<<20
	case strings.HasSuffix(s, "KiB"):
		s, scale = strings.TrimSuffix(s, "KiB"), 1<<10
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return errors.Errorf("invalid byte count %q", text)
		}
		n = n*10 + int64(r-'0')
	}
	if s == "" {
		return errors.Errorf("invalid byte count %q", text)
	}
	*b = byteSize(n * scale)
	return nil
}

type textConfig struct {
	Level    level
	MaxBytes byteSize
}

func describeText(c *textConfig, s *figureout.Schema[textConfig]) {
	figureout.Value(s, &c.Level, "ch_log_level")
	figureout.Value(s, &c.MaxBytes, "max_bytes")
}

// TestTextScalarSpellings covers the whole point of honoring a type's own
// parser: the spellings it accepts are the ones a document may use, in every
// source.
func TestTextScalarSpellings(t *testing.T) {
	d, err := figureout.Derive(describeText)
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("ch_log_level")
	require.True(t, ok)
	require.True(t, f.Type.Text, "the type parses itself")
	require.Equal(t, figureout.TypeInteger, f.Type.Kind, "the underlying kind still sets the semantic type")

	for _, tt := range []struct {
		name   string
		source figureout.Source
	}{
		{"YAML", yaml.Bytes([]byte("ch_log_level: debug\nmax_bytes: 256MiB\n"))},
		{"YAMLQuoted", yaml.Bytes([]byte("ch_log_level: \"debug\"\nmax_bytes: \"256MiB\"\n"))},
		{"JSON", json.Bytes([]byte(`{"ch_log_level": "debug", "max_bytes": "256MiB"}`))},
		{"Env", env.Values(map[string]string{"CH_LOG_LEVEL": "debug", "MAX_BYTES": "256MiB"})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := d.Resolve(tt.source)
			require.NoError(t, err)
			require.Equal(t, levelDebug, cfg.Level)
			require.Equal(t, byteSize(256<<20), cfg.MaxBytes)
		})
	}
}

// TestTextScalarBareNumber covers a type whose parser accepts a bare number:
// the value still goes through the type, which is why 256 and "256MiB" agree.
func TestTextScalarBareNumber(t *testing.T) {
	d, err := figureout.Derive(describeText)
	require.NoError(t, err)

	for _, tt := range []struct {
		name   string
		source figureout.Source
	}{
		{"YAML", yaml.Bytes([]byte("max_bytes: 256\n"))},
		{"JSON", json.Bytes([]byte(`{"max_bytes": 256}`))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := d.Resolve(tt.source)
			require.NoError(t, err)
			require.Equal(t, byteSize(256), cfg.MaxBytes)
		})
	}
}

// TestTextScalarRejectsUnderlyingSpelling is the bug this exists for: bound as
// the underlying int8, "ch_log_level: 1" resolves to warn and the document
// means something other than what it says. The type rejects it, so the
// descriptor has to.
func TestTextScalarRejectsUnderlyingSpelling(t *testing.T) {
	d, err := figureout.Derive(describeText)
	require.NoError(t, err)

	for _, tt := range []struct {
		name   string
		source figureout.Source
	}{
		{"YAML", yaml.Bytes([]byte("ch_log_level: 1\n"))},
		{"Env", env.Values(map[string]string{"CH_LOG_LEVEL": "1"})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := d.Resolve(tt.source)
			require.Error(t, err)
			require.Contains(t, err.Error(), `unrecognized level "1"`)
			require.Contains(t, err.Error(), "ch_log_level", "the path says where")
			require.Equal(t, level(0), cfg.Level, "nothing was bound")
		})
	}
}

// TestTextScalarRejectsGarbage covers a spelling neither the type nor the
// underlying kind accepts.
func TestTextScalarRejectsGarbage(t *testing.T) {
	d, err := figureout.Derive(describeText)
	require.NoError(t, err)

	_, _, err = d.Resolve(yaml.Bytes([]byte("max_bytes: 12PB\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), `invalid byte count "12PB"`)
}

// TestTextScalarInCollections covers a text type reached through a list and a
// map, where the element type is derived rather than registered.
func TestTextScalarInCollections(t *testing.T) {
	type Cfg struct {
		Levels []level
		Limits map[string]byteSize
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Levels, "levels")
		figureout.Value(s, &c.Limits, "limits")
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(yaml.Bytes([]byte("levels: [debug, warn]\nlimits:\n  logs: 1KiB\n")))
	require.NoError(t, err)
	require.Equal(t, []level{levelDebug, levelWarn}, cfg.Levels)
	require.Equal(t, map[string]byteSize{"logs": 1 << 10}, cfg.Limits)

	_, _, err = d.Resolve(yaml.Bytes([]byte("levels: [debug, 2]\n")))
	require.Error(t, err)
	require.Contains(t, err.Error(), `unrecognized level "2"`)
}

// TestTextScalarSchema covers the generated schema, which has to allow the
// string spelling the type accepts as well as the underlying kind.
func TestTextScalarSchema(t *testing.T) {
	d, err := figureout.Derive(describeText)
	require.NoError(t, err)

	out, _, err := jsonschema.Generate(d)
	require.NoError(t, err)
	require.Contains(t, string(out), `"integer",`)
	require.Contains(t, string(out), `"string"`)
}

// TestPlainScalarKeepsItsKind guards the boundary: a named type with no parser
// of its own is still derived from its underlying kind, tag check included.
func TestPlainScalarKeepsItsKind(t *testing.T) {
	type port uint16
	type Cfg struct {
		Port port
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Port, "port")
	})
	require.NoError(t, err)

	f, ok := d.Model().FieldByPath("port")
	require.True(t, ok)
	require.False(t, f.Type.Text)

	_, _, err = d.Resolve(yaml.Bytes([]byte(`port: "8080"`)))
	require.Error(t, err, "a quoted number is still a string")
}
