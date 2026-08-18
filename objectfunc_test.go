package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
)

type inlineDB struct {
	DSN  string
	Pool struct {
		Size int
	}
}

type inlineConfig struct {
	Server Server
	DB     inlineDB
}

func TestObjectFunc(t *testing.T) {
	d, err := figureout.Derive(func(c *inlineConfig, s *figureout.Schema[inlineConfig]) {
		figureout.ObjectFunc(s, &c.Server, "server", func(c *Server, s *figureout.Schema[Server]) {
			figureout.Value(s, &c.Address, "address").ApplyDefault("localhost")
			figureout.Explicit(s, &c.Port, "port", env.Name("LISTEN_PORT")).InRange(1, 65535)
		})
		figureout.ObjectFunc(s, &c.DB, "db", func(c *inlineDB, s *figureout.Schema[inlineDB]) {
			figureout.Value(s, &c.DSN, "dsn")
			// A struct declared inline in Go, registered inline here.
			figureout.ObjectFunc(s, &c.Pool, "pool", func(c *struct{ Size int }, s *figureout.Schema[struct{ Size int }]) {
				figureout.Value(s, &c.Size, "size").ApplyDefault(4)
			})
		})
	})
	require.NoError(t, err)

	m := d.Model()
	f, ok := m.FieldByPath("db.pool.size")
	require.True(t, ok)
	require.Equal(t, figureout.TypeInteger, f.Type.Kind)
	require.Equal(t, "inlineConfig.DB.Pool.Size", f.GoName, "Go paths stay rooted at the parent")

	cfg, report, err := d.Resolve(env.Values(map[string]string{
		"SERVER_LISTEN_PORT": "9090",
		"DB_DSN":             "postgres://localhost",
	}))
	require.NoError(t, err)
	require.Equal(t, "localhost", cfg.Server.Address)
	require.Equal(t, 9090, cfg.Server.Port)
	require.Equal(t, "postgres://localhost", cfg.DB.DSN)
	require.Equal(t, 4, cfg.DB.Pool.Size)

	origin, ok := report.OriginOf("server.port")
	require.True(t, ok)
	require.Equal(t, "SERVER_LISTEN_PORT", origin.Name,
		"env.Name replaces one segment, as it does for a separate descriptor")
}

func TestObjectFuncCompleteness(t *testing.T) {
	_, err := figureout.Derive(func(c *inlineConfig, s *figureout.Schema[inlineConfig]) {
		figureout.ObjectFunc(s, &c.Server, "server", func(c *Server, s *figureout.Schema[Server]) {
			figureout.Value(s, &c.Address, "address")
		})
		figureout.IgnoreRecursive(s, &c.DB)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "inlineConfig.Server.Port")
	require.Contains(t, err.Error(), figureout.CodeMissingDefinition)
}

func TestObjectFuncDuplicateName(t *testing.T) {
	_, err := figureout.Derive(func(c *inlineConfig, s *figureout.Schema[inlineConfig]) {
		figureout.ObjectFunc(s, &c.Server, "server", func(c *Server, s *figureout.Schema[Server]) {
			figureout.Value(s, &c.Address, "addr")
			figureout.Value(s, &c.Port, "addr")
		})
		figureout.IgnoreRecursive(s, &c.DB)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), figureout.CodeDuplicateName)
}

func TestObjectFuncForeignPointer(t *testing.T) {
	_, err := figureout.Derive(func(c *inlineConfig, s *figureout.Schema[inlineConfig]) {
		figureout.ObjectFunc(s, &c.Server, "server", func(_ *Server, s *figureout.Schema[Server]) {
			// Outside the nested object: the child binder rejects it.
			figureout.Value(s, &c.DB.DSN, "dsn")
		})
		figureout.IgnoreRecursive(s, &c.DB)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), figureout.CodeForeignPointer)
}

func TestObjectFuncNilDescribe(t *testing.T) {
	_, err := figureout.Derive(func(c *inlineConfig, s *figureout.Schema[inlineConfig]) {
		// Spelled out because a nil describe names no type to infer C from.
		figureout.ObjectFunc[inlineConfig, Server, Server](s, &c.Server, "server", nil)
		figureout.IgnoreRecursive(s, &c.DB)
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil describe function")
}
