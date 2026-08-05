package file_test

import (
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/file"
	"github.com/go-faster/figureout/source/yaml"
)

type database struct {
	DSN      string
	Password string
}

type config struct {
	Database database
	Token    string
	Timeout  time.Duration
	Hosts    []string
}

func descriptor(t *testing.T) *figureout.Descriptor[config] {
	t.Helper()
	d, err := figureout.Derive(func(c *config, s *figureout.Schema[config]) {
		figureout.ObjectFunc(s, &c.Database, "database", func(c *database, s *figureout.Schema[database]) {
			figureout.Value(s, &c.DSN, "dsn", figureout.Secret()).ApplyDefault("")
			figureout.Value(s, &c.Password, "password", figureout.Secret(), file.Name("db-password")).
				ApplyDefault("")
		})
		figureout.Value(s, &c.Token, "token", figureout.Secret()).ApplyDefault("")
		figureout.Value(s, &c.Timeout, "timeout").ApplyDefault(time.Second)
		figureout.Value(s, &c.Hosts, "hosts").ApplyDefault([]string{})
	})
	require.NoError(t, err)
	return d
}

func TestDirReadsOneValuePerFile(t *testing.T) {
	cfg, report, err := descriptor(t).Resolve(file.FS(fstest.MapFS{
		// A secret written with "echo" ends in a newline that is not part of it.
		"database.dsn": {Data: []byte("postgres://localhost\n")},
		// Name replaces one segment: the file is database.db-password, not
		// db-password, exactly as env.Name composes.
		"database.db-password": {Data: []byte("hunter2")},
		"timeout":              {Data: []byte("30s")},
		"hosts":                {Data: []byte("a,b,c")},
	}))
	require.NoError(t, err)
	require.Equal(t, "postgres://localhost", cfg.Database.DSN)
	require.Equal(t, "hunter2", cfg.Database.Password)
	require.Equal(t, 30*time.Second, cfg.Timeout)
	require.Equal(t, []string{"a", "b", "c"}, cfg.Hosts)
	require.Empty(t, cfg.Token, "a missing file leaves the field to earlier layers")

	origin, ok := report.OriginOf("database.dsn")
	require.True(t, ok)
	require.Equal(t, file.Source, origin.Source)
	require.Equal(t, "database.dsn", origin.Name)
	require.True(t, report.Secret("database.dsn"))
}

func TestDirOverridesAFileLayer(t *testing.T) {
	cfg, report, err := descriptor(t).Resolve(
		yaml.Bytes([]byte("token: from-yaml\ntimeout: 5s\n")),
		file.FS(fstest.MapFS{"token": {Data: []byte("from-mount")}}),
	)
	require.NoError(t, err)
	require.Equal(t, "from-mount", cfg.Token, "later sources win")
	require.Equal(t, 5*time.Second, cfg.Timeout, "an absent file leaves the earlier layer alone")

	origin, _ := report.OriginOf("token")
	require.Equal(t, file.Source, origin.Source)
}

func TestDirRedactsDecodingFailures(t *testing.T) {
	type c struct{ Port int }
	d, err := figureout.Derive(func(cfg *c, s *figureout.Schema[c]) {
		figureout.Value(s, &cfg.Port, "port", figureout.Secret())
	})
	require.NoError(t, err)

	_, _, resolveErr := d.Resolve(file.FS(fstest.MapFS{"port": {Data: []byte("hunter2")}}))
	require.Error(t, resolveErr)
	require.NotContains(t, resolveErr.Error(), "hunter2")
}

func TestDirMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")

	_, _, err := descriptor(t).Resolve(file.Dir(missing))
	require.Error(t, err)

	cfg, _, err := descriptor(t).Resolve(file.Dir(missing, file.Optional()))
	require.NoError(t, err)
	require.Empty(t, cfg.Token)
}

func TestDirReadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, writeFile(filepath.Join(dir, "token"), "sk-abc"))

	cfg, report, err := descriptor(t).Resolve(file.Dir(dir))
	require.NoError(t, err)
	require.Equal(t, "sk-abc", cfg.Token)

	origin, _ := report.OriginOf("token")
	require.Equal(t, filepath.Join(dir, "token"), origin.File)
}

func TestDirNaming(t *testing.T) {
	cfg, _, err := descriptor(t).Resolve(file.FS(
		fstest.MapFS{"DATABASE_DSN": {Data: []byte("postgres://localhost")}},
		file.Names(func(_ *figureout.FieldModel, segments []string) []string {
			return []string{upperSnake(segments)}
		}),
	))
	require.NoError(t, err)
	require.Equal(t, "postgres://localhost", cfg.Database.DSN)
}

func TestDirCollision(t *testing.T) {
	type c struct{ A, B string }
	d, err := figureout.Derive(func(cfg *c, s *figureout.Schema[c]) {
		figureout.Value(s, &cfg.A, "a", file.Name("same"))
		figureout.Value(s, &cfg.B, "b", file.Name("same"))
	})
	require.NoError(t, err)

	_, _, resolveErr := d.Resolve(file.FS(fstest.MapFS{}))
	require.Error(t, resolveErr)
	require.Contains(t, resolveErr.Error(), figureout.CodeSourceNameCollision)
}

func TestDirSkip(t *testing.T) {
	type c struct{ A string }
	d, err := figureout.Derive(func(cfg *c, s *figureout.Schema[c]) {
		figureout.Value(s, &cfg.A, "a", file.Skip()).ApplyDefault("default")
	})
	require.NoError(t, err)

	cfg, _, err := d.Resolve(file.FS(fstest.MapFS{"a": {Data: []byte("from-file")}}))
	require.NoError(t, err)
	require.Equal(t, "default", cfg.A)
}
