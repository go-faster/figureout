// Package figureout derives configuration decoding, validation, defaults and
// schema generation from a single typed declaration.
//
// An application declares its configuration once with [Derive], binding Go
// fields by pointer:
//
//	var ConfigDescriptor = figureout.MustDerive(
//		func(c *Config, s *figureout.Schema[Config]) {
//			figureout.Explicit(s, &c.Host, "host").NonEmpty()
//			figureout.Explicit(s, &c.Port, "port").InRange(1, 65535)
//			figureout.Value(s, &c.Banner, "banner")
//			figureout.Optional(s, &c.Timeout, "timeout").AtLeast(time.Second)
//		},
//	)
//
// The registration function says what absence means: [Explicit] demands a
// value, [Value] resolves to the zero one, and [Optional] keeps the difference
// visible to the consumer.
//
// The resulting [Descriptor] is immutable and format-neutral: sources project
// it into wire representations, schema targets emit it as JSON Schema or CUE.
package figureout
