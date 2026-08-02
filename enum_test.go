package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
)

// TestEnumProviders covers the three accepted value providers: an AllValues
// iterator method, a Values slice method, and a package-level function of the
// kind stringer derivatives generate.
func TestEnumProviders(t *testing.T) {
	type Cfg struct {
		Level    LogLevel
		Mode     Mode
		Kind     Kind
		Explicit LogLevel
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Enum(s, &c.Level, "level")
		figureout.EnumSlice(s, &c.Mode, "mode")
		figureout.EnumFunc(s, &c.Kind, "kind", KindValues)
		figureout.EnumValues(s, &c.Explicit, "explicit", []LogLevel{LogDebug, LogError})
	})
	require.NoError(t, err)

	for path, want := range map[string][]any{
		"level":    {LogDebug, LogInfo, LogWarn, LogError},
		"mode":     {ModeFast, ModeSafe},
		"kind":     {KindA, KindB},
		"explicit": {LogDebug, LogError},
	} {
		f, ok := d.Model().FieldByPath(path)
		require.True(t, ok, path)
		values, ok := figureout.EnumValuesOf(f)
		require.True(t, ok, path)
		require.Equal(t, want, values, path)
	}

	vars := map[string]string{
		"LEVEL":    "warn",
		"MODE":     "safe",
		"KIND":     "2",
		"EXPLICIT": "debug",
	}
	cfg, _, err := d.Resolve(env.Values(vars))
	require.NoError(t, err)
	require.Equal(t, LogWarn, cfg.Level)
	require.Equal(t, ModeSafe, cfg.Mode)
	require.Equal(t, KindB, cfg.Kind)

	// A value outside the set is rejected even though it parses.
	vars["EXPLICIT"] = "warn"
	_, _, err = d.Resolve(env.Values(vars))
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be one of [debug, error]")
}

// TestEnumOnCarrier shows the option form, used for carriers the enum helpers
// do not spell.
func TestEnumOnCarrier(t *testing.T) {
	type Cfg struct {
		Level figureout.Optional[LogLevel]
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Field(s, &c.Level, "level", figureout.EnumOf[LogLevel]())
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{"LEVEL": "info"}))
	require.NoError(t, err)
	level, ok := cfg.Level.Value()
	require.True(t, ok)
	require.Equal(t, LogInfo, level)

	_, _, err = d.Resolve(env.Values(map[string]string{"LEVEL": "nope"}))
	require.Error(t, err)
}

func TestEnumWithoutValues(t *testing.T) {
	type Cfg struct {
		Level LogLevel
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.EnumValues(s, &c.Level, "level", nil)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "enum has no values")
}
