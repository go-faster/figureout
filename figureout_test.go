package figureout_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
)

func TestResolve(t *testing.T) {
	cfg, report, err := configDescriptor.Resolve(
		env.Values(map[string]string{
			"APP_ADDRESS": "127.0.0.1",
			"APP_PORT":    "8080",
			"APP_TIMEOUT": "5s",
			"APP_LEVEL":   "warn",
			"APP_TAGS":    "a,b, c",
		}, env.Prefix("APP_")),
	)
	require.NoError(t, err)

	require.Equal(t, "127.0.0.1", cfg.Server.Address)
	require.Equal(t, 8080, cfg.Server.Port)
	require.Equal(t, LogWarn, cfg.Level)
	require.Equal(t, []string{"a", "b", "c"}, cfg.Tags)

	timeout, ok := cfg.Timeout.Value()
	require.True(t, ok)
	require.Equal(t, 5*time.Second, timeout)

	origin, ok := report.OriginOf("server.port")
	require.True(t, ok)
	require.Equal(t, env.Source, origin.Source)
	require.Equal(t, "APP_PORT", origin.Name)
}

func TestResolveDefaults(t *testing.T) {
	cfg, report, err := configDescriptor.Resolve(
		env.Values(map[string]string{
			"ADDRESS": "localhost",
			"PORT":    "80",
		}),
	)
	require.NoError(t, err)

	require.Equal(t, LogInfo, cfg.Level, "applied default")
	require.Empty(t, cfg.Tags)
	require.False(t, cfg.Timeout.IsSet(), "optional without a default stays missing")

	origin, ok := report.OriginOf("level")
	require.True(t, ok)
	require.Equal(t, figureout.SourceID("default"), origin.Source)
}

func TestResolveMissingRequired(t *testing.T) {
	_, _, err := configDescriptor.Resolve(env.Values(map[string]string{
		"ADDRESS": "localhost",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "server.port")
	require.Contains(t, err.Error(), figureout.CodeMissingDefinition)
}

func TestResolveConstraintViolation(t *testing.T) {
	_, _, err := configDescriptor.Resolve(env.Values(map[string]string{
		"ADDRESS": "localhost",
		"PORT":    "70000",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 65535")
	require.Contains(t, err.Error(), "PORT", "diagnostics carry provenance")
}

func TestResolveEnumViolation(t *testing.T) {
	_, _, err := configDescriptor.Resolve(env.Values(map[string]string{
		"ADDRESS": "localhost",
		"PORT":    "80",
		"LEVEL":   "verbose",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be one of [debug, info, warn, error]")
}

func TestModel(t *testing.T) {
	m := configDescriptor.Model()

	f, ok := m.FieldByPath("server.port")
	require.True(t, ok)
	require.Equal(t, figureout.TypeInteger, f.Type.Kind)
	require.Equal(t, figureout.PresenceRequired, f.Presence)
	require.Equal(t, "Server.Port", f.GoName)

	timeout, ok := m.FieldByPath("timeout")
	require.True(t, ok)
	require.Equal(t, figureout.TypeDuration, timeout.Type.Kind)
	require.Equal(t, figureout.PresenceOptional, timeout.Presence)
	require.Equal(t, "Request timeout.", timeout.Meta.Doc)

	level, ok := m.FieldByPath("level")
	require.True(t, ok)
	values, ok := figureout.EnumValuesOf(level)
	require.True(t, ok)
	require.Equal(t, []any{LogDebug, LogInfo, LogWarn, LogError}, values)
}

func TestDeriveErrors(t *testing.T) {
	type Nested struct {
		Kept    string
		Dropped string
	}
	type Cfg struct {
		A int
		B string
		N Nested
	}

	tests := []struct {
		name     string
		describe func(*Cfg, *figureout.Schema[Cfg])
		wantCode string
		wantMsg  string
	}{
		{
			name: "missing definition",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.Int(s, &c.A, "a")
			},
			wantCode: figureout.CodeMissingDefinition,
			wantMsg:  "Cfg.B is neither registered nor explicitly ignored",
		},
		{
			name: "nested incompleteness",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.Int(s, &c.A, "a")
				figureout.String(s, &c.B, "b")
				figureout.String(s, &c.N.Kept, "kept")
			},
			wantCode: figureout.CodeMissingDefinition,
			wantMsg:  "Cfg.N.Dropped is neither registered nor explicitly ignored",
		},
		{
			name: "duplicate registration",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.Int(s, &c.A, "a")
				figureout.Int(s, &c.A, "also_a")
				figureout.String(s, &c.B, "b")
				figureout.IgnoreRecursive(s, &c.N)
			},
			wantCode: figureout.CodeDuplicateField,
			wantMsg:  "registered more than once",
		},
		{
			name: "duplicate name",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.Int(s, &c.A, "value")
				figureout.String(s, &c.B, "value")
				figureout.IgnoreRecursive(s, &c.N)
			},
			wantCode: figureout.CodeDuplicateName,
			wantMsg:  `property "value" is bound to both`,
		},
		{
			name: "foreign pointer",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				var other int
				figureout.Int(s, &other, "a")
			},
			wantCode: figureout.CodeForeignPointer,
			wantMsg:  "does not refer to a field inside figureout_test.Cfg",
		},
		{
			name: "pointer to copy",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				a := c.A
				figureout.Int(s, &a, "a")
			},
			wantCode: figureout.CodeForeignPointer,
		},
		{
			name: "helper type mismatch is caught by binding",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				// A is an int, so no string field lives at its offset.
				figureout.String(s, (*string)(nil), "a")
			},
			wantCode: figureout.CodeForeignPointer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := figureout.Derive(tt.describe)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantCode)
			if tt.wantMsg != "" {
				require.Contains(t, err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestIgnore(t *testing.T) {
	type Runtime struct {
		Cache map[string]string
		Conn  int
	}
	type Cfg struct {
		Name    string
		Runtime Runtime
		Marker  struct{}

		hidden int //nolint:unused // exercises default exclusion of unexported fields
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.String(s, &c.Name, "name")
		figureout.IgnoreRecursive(s, &c.Runtime, figureout.Reason("runtime-only state"))
		figureout.IgnorePath[Cfg](s, "Marker")
	})
	require.NoError(t, err)
	require.Len(t, d.Model().Fields(), 1, "unexported fields are excluded by default")
}

func TestIgnoreIsNotRecursiveByDefault(t *testing.T) {
	type Runtime struct {
		Cache int
	}
	type Cfg struct {
		Runtime Runtime
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Ignore(s, &c.Runtime)
	})
	require.NoError(t, err, "ignoring the parent as a leaf covers it")

	_, err = figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Ignore(s, &c.Runtime.Cache)
	})
	require.NoError(t, err, "parent is covered by its registered descendants")
}

func TestCompletenessStrict(t *testing.T) {
	type Cfg struct {
		Name   string
		hidden int //nolint:unused // exercises default exclusion of unexported fields
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.String(s, &c.Name, "name")
	}, figureout.Completeness(figureout.CompletenessStrict))
	require.Error(t, err)
	require.Contains(t, err.Error(), "Cfg.hidden")

	_, err = figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.String(s, &c.Name, "name")
		figureout.Ignore(s, &c.hidden)
	}, figureout.Completeness(figureout.CompletenessStrict))
	require.NoError(t, err)
}

func TestCompletenessDisabled(t *testing.T) {
	type Cfg struct {
		Name string
		Port int
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.String(s, &c.Name, "name")
	}, figureout.Completeness(figureout.CompletenessDisabled))
	require.NoError(t, err)
}

func TestCompletenessTagged(t *testing.T) {
	type Cfg struct {
		Name  string `config:"name"`
		Extra string
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.String(s, &c.Name, "name")
	}, figureout.Completeness(figureout.CompletenessTagged))
	require.NoError(t, err, "untagged fields do not participate")
}

func TestUnexportedFieldCannotBeRegistered(t *testing.T) {
	type Cfg struct {
		secret string
	}

	_, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.String(s, &c.secret, "secret")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexported")
}

func TestTypeRegistry(t *testing.T) {
	type Port uint16
	type Cfg struct {
		Listen Port
		Admin  Port
	}

	types := figureout.NewTypeRegistry()
	require.NoError(t, figureout.RegisterType[Port](types,
		figureout.IntegerType(),
		figureout.InRange(1, 65535),
	))

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Int(s, &c.Listen, "listen")
		figureout.Int(s, &c.Admin, "admin")
	}, figureout.WithTypeRegistry(types))
	require.NoError(t, err)

	_, _, err = d.Resolve(env.Values(map[string]string{
		"LISTEN": "0",
		"ADMIN":  "9000",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 1")
}

func TestOptionalCarrier(t *testing.T) {
	type Cfg struct {
		Name  figureout.Optional[string]
		Count figureout.Nullable[int]
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.String(s, &c.Name, "name")
		figureout.Int(s, &c.Count, "count")
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(env.Values(map[string]string{"NAME": ""}))
	require.NoError(t, err)

	name, ok := cfg.Name.Value()
	require.True(t, ok, "an explicitly provided zero value is present")
	require.Empty(t, name)
	require.True(t, cfg.Count.IsMissing())
}

func TestCheckIsRuntimeOnly(t *testing.T) {
	type Cfg struct {
		Workers int
	}

	d, err := figureout.Derive(func(c *Cfg, s *figureout.Schema[Cfg]) {
		figureout.Int(s, &c.Workers, "workers", figureout.Check("must-be-even", func(v int) error {
			if v%2 != 0 {
				return errEven
			}
			return nil
		}))
	})
	require.NoError(t, err)

	_, _, err = d.Resolve(env.Values(map[string]string{"WORKERS": "3"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "must-be-even")

	_, _, err = d.Resolve(env.Values(map[string]string{"WORKERS": "4"}))
	require.NoError(t, err)
}

type constError string

func (e constError) Error() string { return string(e) }

const errEven constError = "must be even"

func TestDescriptorValue(t *testing.T) {
	cfg, _, err := configDescriptor.Resolve(env.Values(map[string]string{
		"ADDRESS": "localhost",
		"PORT":    "80",
		"TIMEOUT": "3s",
	}))
	require.NoError(t, err)

	port, ok := configDescriptor.Value(&cfg, "server.port")
	require.True(t, ok)
	require.Equal(t, 80, port)

	timeout, ok := configDescriptor.Value(&cfg, "timeout")
	require.True(t, ok)
	require.Equal(t, 3*time.Second, timeout, "carriers are unwrapped")

	_, ok = configDescriptor.Value(&cfg, "nope")
	require.False(t, ok)
}

func TestDescriptorValueUnion(t *testing.T) {
	cfg, _, err := storeDescriptor.Resolve(env.Values(map[string]string{
		"BACKEND_TYPE":   "s3",
		"BACKEND_BUCKET": "configs",
	}))
	require.NoError(t, err)

	bucket, ok := storeDescriptor.Value(&cfg, "backend.bucket")
	require.True(t, ok)
	require.Equal(t, "configs", bucket)

	_, ok = storeDescriptor.Value(&cfg, "backend.path")
	require.False(t, ok, "paths inside an unselected variant are absent")
}
