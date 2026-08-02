package figureout_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
)

type S3Backend struct {
	Bucket string
}

type LocalBackend struct {
	Path string
}

type Backend struct {
	S3    *S3Backend
	Local *LocalBackend
}

type StoreConfig struct {
	Backend Backend
}

var (
	s3Descriptor = figureout.MustDerive(func(c *S3Backend, s *figureout.Schema[S3Backend]) {
		figureout.String(s, &c.Bucket, "bucket").NonEmpty()
	})
	localDescriptor = figureout.MustDerive(func(c *LocalBackend, s *figureout.Schema[LocalBackend]) {
		figureout.String(s, &c.Path, "path").NonEmpty()
	})
	storeDescriptor = figureout.MustDerive(func(c *StoreConfig, s *figureout.Schema[StoreConfig]) {
		figureout.OneOf(s, &c.Backend, "backend",
			figureout.Discriminator("type"),
			figureout.Variant("s3", &c.Backend.S3, s3Descriptor),
			figureout.Variant("local", &c.Backend.Local, localDescriptor),
		)
	})
)

func TestOneOf(t *testing.T) {
	cfg, report, err := storeDescriptor.Resolve(env.Values(map[string]string{
		"BACKEND_TYPE":   "s3",
		"BACKEND_BUCKET": "configs",
	}))
	require.NoError(t, err)
	require.NotNil(t, cfg.Backend.S3)
	require.Nil(t, cfg.Backend.Local, "unselected variants stay nil")
	require.Equal(t, "configs", cfg.Backend.S3.Bucket)

	origin, ok := report.OriginOf("backend.type")
	require.True(t, ok)
	require.Equal(t, "BACKEND_TYPE", origin.Name)

	cfg, _, err = storeDescriptor.Resolve(env.Values(map[string]string{
		"BACKEND_TYPE": "local",
		"BACKEND_PATH": "/etc/app",
	}))
	require.NoError(t, err)
	require.Nil(t, cfg.Backend.S3)
	require.Equal(t, "/etc/app", cfg.Backend.Local.Path)
}

func TestOneOfUnknownVariant(t *testing.T) {
	_, _, err := storeDescriptor.Resolve(env.Values(map[string]string{
		"BACKEND_TYPE": "gcs",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown variant "gcs", want one of [s3, local]`)
}

func TestOneOfMissingDiscriminator(t *testing.T) {
	_, _, err := storeDescriptor.Resolve(env.Values(map[string]string{
		"BACKEND_BUCKET": "configs",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), `requires discriminator "type"`)
}

func TestOneOfDeclarationErrors(t *testing.T) {
	type Cfg struct {
		Backend Backend
	}
	other := Backend{}

	tests := []struct {
		name     string
		describe func(*Cfg, *figureout.Schema[Cfg])
		wantMsg  string
	}{
		{
			name: "no discriminator",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.OneOf(s, &c.Backend, "backend",
					figureout.Variant("s3", &c.Backend.S3, s3Descriptor),
				)
			},
			wantMsg: "has no discriminator",
		},
		{
			name: "no variants",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.OneOf(s, &c.Backend, "backend", figureout.Discriminator("type"))
			},
			wantMsg: "has no variants",
		},
		{
			name: "duplicate tag",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.OneOf(s, &c.Backend, "backend",
					figureout.Discriminator("type"),
					figureout.Variant("s3", &c.Backend.S3, s3Descriptor),
					figureout.Variant("s3", &c.Backend.Local, localDescriptor),
				)
			},
			wantMsg: `duplicate variant tag "s3"`,
		},
		{
			name: "foreign variant",
			describe: func(c *Cfg, s *figureout.Schema[Cfg]) {
				figureout.OneOf(s, &c.Backend, "backend",
					figureout.Discriminator("type"),
					figureout.Variant("s3", &other.S3, s3Descriptor),
				)
			},
			wantMsg: "does not refer to a field inside",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := figureout.Derive(tt.describe)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantMsg)
		})
	}
}
