package integration

import (
	"iter"
	"slices"
	"time"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
)

type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
)

func (LogLevel) AllValues() iter.Seq[LogLevel] {
	return slices.Values([]LogLevel{LogDebug, LogInfo, LogWarn})
}

type TLS struct {
	Cert string
	Key  string
}

type Server struct {
	Address string
	Port    int
	TLS     TLS
}

type S3Backend struct {
	Bucket string
	Region string
}

type LocalBackend struct {
	Path string
}

type Backend struct {
	S3    *S3Backend
	Local *LocalBackend
}

type Config struct {
	Server  Server
	Backend Backend
	Level   LogLevel
	Timeout figureout.OptionalOf[time.Duration]
	Tags    []string
	Limits  map[string]int
	Ratio   float64
	Debug   bool
	Token   string
}

var tlsDescriptor = figureout.MustDerive(func(c *TLS, s *figureout.Schema[TLS]) {
	figureout.Explicit(s, &c.Cert, "cert").NonEmpty()
	figureout.Explicit(s, &c.Key, "key").NonEmpty()
})

var serverDescriptor = figureout.MustDerive(func(c *Server, s *figureout.Schema[Server]) {
	figureout.Explicit(s, &c.Address, "address", env.Name("ADDRESS")).
		NonEmpty().Pattern(`^[a-z0-9.\-]+$`)
	figureout.Explicit(s, &c.Port, "port", env.Name("PORT")).
		InRange(1, 65535)
	figureout.Object(s, &c.TLS, "tls", tlsDescriptor)
})

var configDescriptor = figureout.MustDerive(func(c *Config, s *figureout.Schema[Config]) {
	figureout.Object(s, &c.Server, "server", serverDescriptor).
		Doc("HTTP server settings.")

	figureout.OneOf(s, &c.Backend, "backend",
		figureout.Discriminator("type"),
		figureout.Variant("s3", &c.Backend.S3,
			figureout.MustDerive(func(b *S3Backend, s *figureout.Schema[S3Backend]) {
				figureout.Explicit(s, &b.Bucket, "bucket").NonEmpty()
				figureout.Value(s, &b.Region, "region").ApplyDefault("us-east-1")
			})),
		figureout.Variant("local", &c.Backend.Local,
			figureout.MustDerive(func(b *LocalBackend, s *figureout.Schema[LocalBackend]) {
				figureout.Explicit(s, &b.Path, "path").NonEmpty()
			})),
	)

	figureout.Enum(s, &c.Level, "level").ApplyDefault(LogInfo)
	figureout.Optional(s, &c.Timeout, "timeout").AtLeast(time.Second)
	figureout.Value(s, &c.Tags, "tags").MinItems(1).MaxItems(8).ApplyDefault([]string{"default"})
	figureout.Value(s, &c.Limits, "limits").ApplyDefault(map[string]int{})
	figureout.Value(s, &c.Ratio, "ratio").GreaterThan(0.0).LessThan(1.0).ApplyDefault(0.5)
	figureout.Value(s, &c.Debug, "debug").ApplyDefault(false)
	figureout.Value(s, &c.Token, "token", figureout.Secret()).ApplyDefault("")
})

// document exercises every shape the descriptor declares, and carries the
// schema reference an editor writes.
const document = `{
  "$schema": "./config.schema.json",
  "server": {
    "address": "api.example.com",
    "port": 8080,
    "tls": {
      "cert": "/etc/tls/cert.pem",
      "key": "/etc/tls/key.pem"
    }
  },
  "backend": {
    "type": "s3",
    "bucket": "configs",
    "region": "eu-central-1"
  },
  "level": "warn",
  "timeout": "30s",
  "tags": ["api", "prod"],
  "limits": {"cpu": 2, "mem": 8},
  "ratio": 0.25,
  "debug": true,
  "token": "s3cret"
}`
