package figureout_test

import (
	"iter"
	"slices"
	"time"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/source/env"
)

// LogLevel is an enumerated string type. AllValues is what a stringer
// derivative such as enumer would generate.
type LogLevel string

// Log levels.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

var logLevels = []LogLevel{LogDebug, LogInfo, LogWarn, LogError}

// AllValues implements [figureout.EnumValuer].
func (LogLevel) AllValues() iter.Seq[LogLevel] { return slices.Values(logLevels) }

// Mode uses the slice-returning provider form.
type Mode string

// Modes.
const (
	ModeFast Mode = "fast"
	ModeSafe Mode = "safe"
)

// Values implements [figureout.EnumSliceValuer].
func (Mode) Values() []Mode { return []Mode{ModeFast, ModeSafe} }

// Kind uses a package-level values function, as enumer generates.
type Kind int

// Kinds.
const (
	KindA Kind = iota + 1
	KindB
)

// KindValues mirrors the function enumer emits.
func KindValues() []Kind { return []Kind{KindA, KindB} }

type Server struct {
	Address string
	Port    int
}

type Config struct {
	Server  Server
	Timeout figureout.OptionalOf[time.Duration]
	Level   LogLevel
	Tags    []string

	logger any
}

var serverDescriptor = figureout.MustDerive(
	func(c *Server, s *figureout.Schema[Server]) {
		figureout.Explicit(s, &c.Address, "address", env.Name("ADDRESS")).
			NonEmpty()
		figureout.Explicit(s, &c.Port, "port", env.Name("PORT")).
			InRange(1, 65535)
	},
)

var configDescriptor = figureout.MustDerive(
	func(c *Config, s *figureout.Schema[Config]) {
		figureout.Object(s, &c.Server, "server", serverDescriptor)

		figureout.Optional(s, &c.Timeout, "timeout",
			figureout.Doc("Request timeout."),
		).AtLeast(time.Second)

		figureout.Enum(s, &c.Level, "level").
			ApplyDefault(LogInfo)

		figureout.Value(s, &c.Tags, "tags").
			ApplyDefault([]string{})

		figureout.Ignore(s, &c.logger, figureout.Reason("runtime dependency"))
	},
	figureout.Completeness(figureout.CompletenessExported),
)
