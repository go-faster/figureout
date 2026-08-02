// Package figureout derives configuration decoding, validation, defaults and
// schema generation from a single typed declaration.
//
// An application declares its configuration once with [Derive], binding Go
// fields by pointer:
//
//	var ConfigDescriptor = figureout.MustDerive(
//		func(c *Config, s *figureout.Schema[Config]) {
//			figureout.String(s, &c.Host, "host").NonEmpty()
//			figureout.Int(s, &c.Port, "port").InRange(1, 65535)
//		},
//	)
//
// The resulting [Descriptor] is immutable and format-neutral: sources project
// it into wire representations, schema targets emit it as JSON Schema or CUE.
package figureout
