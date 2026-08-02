package main

import (
	"iter"
	"slices"
	"time"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
	"github.com/go-faster/figureout/source/json"
	"github.com/go-faster/figureout/source/yaml"
)

// LogLevel is an enumerated type. AllValues is what a stringer derivative such
// as enumer generates, and it is what makes the enum self-describing.
type LogLevel string

// Log levels.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// AllValues implements [figureout.EnumValuer].
func (LogLevel) AllValues() iter.Seq[LogLevel] {
	return slices.Values([]LogLevel{LogDebug, LogInfo, LogWarn, LogError})
}

// Server is the HTTP listener configuration.
type Server struct {
	Address string
	Port    int
	Timeout figureout.OptionalOf[time.Duration]
}

// Storage selects where the service keeps its data. Exactly one variant is
// populated, selected by the "type" discriminator.
type Storage struct {
	S3    *S3Storage
	Local *LocalStorage
}

// S3Storage stores data in an S3 bucket.
type S3Storage struct {
	Bucket string
	Region string
}

// LocalStorage stores data on the local filesystem.
type LocalStorage struct {
	Path string
}

// Config is the service configuration.
type Config struct {
	Server  Server
	Storage Storage
	Level   LogLevel
	Tags    []string
	Limits  map[string]int

	// Runtime state, deliberately not configuration.
	started time.Time
}

// ServerDescriptor describes [Server].
var ServerDescriptor = figureout.MustDerive(
	func(c *Server, s *figureout.Schema[Server]) {
		figureout.Value(s, &c.Address, "address").
			Doc("Listen address.").NonEmpty().ApplyDefault("127.0.0.1")

		// env.Name replaces this field's own segment only, so nesting Server
		// under "server" reads APP_SERVER_LISTEN_PORT.
		figureout.Value(s, &c.Port, "port",
			env.Name("LISTEN_PORT"),
			json.Accepts(json.Integer(), json.String()),
		).Doc("Listen port.").InRange(1, 65535).ApplyDefault(8080)

		figureout.Optional(s, &c.Timeout, "timeout",
			env.NullLiteral("null"),
		).Doc("Request timeout; unset means no timeout.").AtLeast(time.Millisecond)
	},
)

// S3Descriptor describes [S3Storage].
var S3Descriptor = figureout.MustDerive(
	func(c *S3Storage, s *figureout.Schema[S3Storage]) {
		figureout.Value(s, &c.Bucket, "bucket").NonEmpty()
		figureout.Value(s, &c.Region, "region").ApplyDefault("us-east-1")
	},
)

// LocalDescriptor describes [LocalStorage].
var LocalDescriptor = figureout.MustDerive(
	func(c *LocalStorage, s *figureout.Schema[LocalStorage]) {
		figureout.Value(s, &c.Path, "path").NonEmpty()
	},
)

// ConfigDescriptor describes [Config]. It is the single definition every
// consumer derives from: decoding, validation, defaults and the JSON Schema.
var ConfigDescriptor = figureout.MustDerive(
	func(c *Config, s *figureout.Schema[Config]) {
		figureout.Object(s, &c.Server, "server", ServerDescriptor).
			Doc("HTTP server settings.")

		figureout.OneOf(s, &c.Storage, "storage",
			figureout.Discriminator("type"),
			figureout.Variant("s3", &c.Storage.S3, S3Descriptor),
			figureout.Variant("local", &c.Storage.Local, LocalDescriptor),
		).Doc("Where the service keeps its data.")

		figureout.Enum(s, &c.Level, "level").
			Doc("Log verbosity.").ApplyDefault(LogInfo)

		figureout.Value(s, &c.Tags, "tags").
			Doc("Tags applied to every metric.").MergeAppend().ApplyDefault([]string{})

		figureout.Value(s, &c.Limits, "limits").
			Doc("Resource limits, merged per entry.").MergeByKey().ApplyDefault(map[string]int{})

		figureout.Ignore(s, &c.started, figureout.Reason("runtime state"))
	},
)

// Load resolves the configuration from a file, then the environment.
//
// Later sources win, so APP_PORT overrides what the file says.
func Load(path string) (Config, *figureout.Report, error) {
	return ConfigDescriptor.Resolve(
		yaml.File(path, yaml.Optional()),
		env.Current(env.Prefix("APP_")),
	)
}
