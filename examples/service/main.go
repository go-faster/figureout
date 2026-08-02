// Command service shows figureout in a realistic setup: a layered
// configuration, provenance in its output, and a JSON Schema generated from
// the same definition.
//
//	go run ./examples/service
//	APP_SERVER_PORT=9090 go run ./examples/service
//	go run ./examples/service -schema
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/go-faster/figureout"
	"github.com/go-faster/figureout/schema/jsonschema"
	"github.com/go-faster/figureout/source/json"
)

func main() {
	var (
		path       = flag.String("config", "examples/service/config.yaml", "configuration file")
		printJSON  = flag.Bool("schema", false, "print the JSON Schema and exit")
		printPaths = flag.Bool("paths", false, "print every configuration path and exit")
	)
	flag.Parse()

	switch {
	case *printJSON:
		if err := printSchema(); err != nil {
			fail(err)
		}
	case *printPaths:
		printModel()
	default:
		if err := run(*path); err != nil {
			fail(err)
		}
	}
}

func run(path string) error {
	cfg, report, err := Load(path)
	if err != nil {
		return err
	}

	fmt.Printf("listening on %s:%d\n", cfg.Server.Address, cfg.Server.Port)
	if timeout, ok := cfg.Server.Timeout.Value(); ok {
		fmt.Printf("request timeout %s\n", timeout)
	} else {
		fmt.Println("no request timeout")
	}

	switch {
	case cfg.Storage.S3 != nil:
		fmt.Printf("storage s3 bucket=%s region=%s\n", cfg.Storage.S3.Bucket, cfg.Storage.S3.Region)
	case cfg.Storage.Local != nil:
		fmt.Printf("storage local path=%s\n", cfg.Storage.Local.Path)
	}

	fmt.Printf("level=%s tags=%v limits=%v\n", cfg.Level, cfg.Tags, cfg.Limits)

	fmt.Println("\nprovenance:")
	for _, path := range configPaths() {
		origin, ok := report.OriginOf(path)
		if !ok {
			continue
		}
		fmt.Printf("  %-24s %s\n", path, origin)
	}
	return nil
}

// configPaths lists every path a source can set, in declaration order.
//
// A union's discriminator is not a Go field, so it is not in Fields(); ask the
// model for its path explicitly.
func configPaths() []string {
	var out []string
	for _, f := range ConfigDescriptor.Model().Fields() {
		if path, ok := figureout.DiscriminatorPath(f); ok {
			out = append(out, path)
			continue
		}
		if f.Type.Object == nil {
			out = append(out, f.Path)
		}
	}
	return out
}

func printSchema() error {
	schema, diags, err := jsonschema.Generate(ConfigDescriptor,
		jsonschema.ForSource(json.Source),
		jsonschema.Title("Service configuration"),
	)
	if err != nil {
		return err
	}
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, "warning:", d.Error())
	}
	fmt.Println(string(schema))
	return nil
}

// printModel walks the compiled descriptor, which is what documentation and
// shell completion would do.
func printModel() {
	for _, f := range ConfigDescriptor.Model().Fields() {
		line := fmt.Sprintf("%-20s %-10s %s", f.Path, f.Type.Kind, f.Presence)
		if values, ok := figureout.EnumValuesOf(f); ok {
			line += fmt.Sprintf(" %v", values)
		}
		if f.Default != nil && f.Default.Applied {
			line += fmt.Sprintf(" default=%v", f.Default.Value)
		}
		if f.Meta.Doc != "" {
			line += "  // " + f.Meta.Doc
		}
		fmt.Println(line)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
