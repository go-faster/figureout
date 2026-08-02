package figureout_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

type MergeConfig struct {
	Tags    []string
	Args    []string
	Limits  map[string]int
	Timeout figureout.OptionalOf[time.Duration]
	Level   string
}

var mergeDescriptor = figureout.MustDerive(func(c *MergeConfig, s *figureout.Schema[MergeConfig]) {
	figureout.Value(s, &c.Tags, "tags").MergeAppend().ApplyDefault([]string{})
	figureout.Value(s, &c.Args, "args").ApplyDefault([]string{}) // replace, the default
	figureout.Value(s, &c.Limits, "limits").MergeByKey().ApplyDefault(map[string]int{})
	figureout.Optional(s, &c.Timeout, "timeout")
	figureout.Value(s, &c.Level, "level").ApplyDefault("info")
})

func TestMergeAppend(t *testing.T) {
	cfg, _, err := mergeDescriptor.Resolve(
		yaml.Bytes([]byte("tags: [a, b]\nargs: [x]\n")),
		yaml.Bytes([]byte("tags: [c]\nargs: [y]\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, cfg.Tags, "append accumulates in layer order")
	require.Equal(t, []string{"y"}, cfg.Args, "replace is still the default")
}

func TestMergeByKey(t *testing.T) {
	cfg, _, err := mergeDescriptor.Resolve(
		yaml.Bytes([]byte("limits:\n  cpu: 1\n  mem: 8\n")),
		yaml.Bytes([]byte("limits:\n  mem: 16\n")),
	)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"cpu": 1, "mem": 16}, cfg.Limits,
		"a later layer changes only the keys it names")
}

// TestNullErases pins the meaning null carries in a layered configuration: it
// drops what earlier layers set rather than becoming a value.
func TestNullErases(t *testing.T) {
	cfg, report, err := mergeDescriptor.Resolve(
		yaml.Bytes([]byte("level: debug\ntimeout: 30s\ntags: [a]\n")),
		yaml.Bytes([]byte("level: null\ntimeout: ~\ntags: null\n")),
	)
	require.NoError(t, err)

	require.Equal(t, "info", cfg.Level, "erased, so the default applies")
	require.False(t, cfg.Timeout.IsSet(), "erased, and no default, so missing")
	require.Empty(t, cfg.Tags, "an erase drops the whole accumulation")

	erased, ok := report.ErasedBy("level")
	require.True(t, ok)
	require.Equal(t, yaml.Source, erased.Source)
	require.Equal(t, 1, erased.Line)

	origin, ok := report.OriginOf("level")
	require.True(t, ok)
	require.Equal(t, figureout.SourceID("default"), origin.Source,
		"the surviving value comes from the default, not the erased layer")
}

func TestNullThenValue(t *testing.T) {
	cfg, _, err := mergeDescriptor.Resolve(
		yaml.Bytes([]byte("tags: [a]\n")),
		yaml.Bytes([]byte("tags: null\n")),
		yaml.Bytes([]byte("tags: [b]\n")),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, cfg.Tags, "an erase resets the accumulation, it does not disable it")
}

func TestMergeAcrossFormats(t *testing.T) {
	cfg, report, err := mergeDescriptor.Resolve(
		json.Bytes([]byte(`{"tags": ["from-json"], "limits": {"cpu": 1}}`)),
		yaml.Bytes([]byte("tags: [from-yaml]\nlimits:\n  mem: 8\n")),
		env.Values(map[string]string{"LEVEL": "warn"}),
	)
	require.NoError(t, err)

	require.Equal(t, []string{"from-json", "from-yaml"}, cfg.Tags)
	require.Equal(t, map[string]int{"cpu": 1, "mem": 8}, cfg.Limits)
	require.Equal(t, "warn", cfg.Level)

	origin, ok := report.OriginOf("tags")
	require.True(t, ok)
	require.Equal(t, yaml.Source, origin.Source, "the last contributing layer is named")
}

func TestMergePolicyMustFitTheType(t *testing.T) {
	type Cfg struct {
		Name string
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Name, "name").MergeAppend()
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `merge policy "append" does not apply to a string field`)

	_, err = figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Value(s, &c.Name, "name").MergeByKey()
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `merge policy "by-key" does not apply to a string field`)
}
