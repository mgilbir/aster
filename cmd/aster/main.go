// Command aster converts Vega and Vega-Lite specs to SVG.
//
// Usage:
//
//	aster svg -i input.vl.json -o output.svg
//	aster svg -i input.vl.json              # stdout
//	cat spec.json | aster svg > output.svg  # stdin
//	aster compile -i input.vl.json          # Vega-Lite → Vega JSON
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mgilbir/aster"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "aster: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: aster <command> [flags]\n\nCommands:\n  svg      Render spec to SVG\n  compile  Compile Vega-Lite to Vega JSON")
	}

	command := os.Args[1]
	switch command {
	case "svg":
		return runSVG(os.Args[2:])
	case "compile":
		return runCompile(os.Args[2:])
	default:
		return fmt.Errorf("unknown command %q (expected svg or compile)", command)
	}
}

func runSVG(args []string) (err error) {
	fs := flag.NewFlagSet("svg", flag.ExitOnError)
	input := fs.String("i", "", "input spec file (- or omit for stdin)")
	output := fs.String("o", "", "output SVG file (omit for stdout)")
	allowHTTP := fs.Bool("allow-http", false, "allow HTTP(S) data loading")
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := readInput(*input)
	if err != nil {
		return err
	}

	var opts []aster.Option
	if *allowHTTP {
		opts = append(opts, aster.WithLoader(aster.NewHTTPLoader(nil)))
	}

	c, err := aster.New(opts...)
	if err != nil {
		return err
	}
	defer func() {
		if e := c.Close(); e != nil && err == nil {
			err = e
		}
	}()

	var svg string
	if isVegaLite(spec) {
		svg, err = c.VegaLiteToSVG(spec)
	} else {
		svg, err = c.VegaToSVG(spec)
	}
	if err != nil {
		return err
	}

	return writeOutput(*output, []byte(svg))
}

func runCompile(args []string) (err error) {
	fs := flag.NewFlagSet("compile", flag.ExitOnError)
	input := fs.String("i", "", "input Vega-Lite spec file (- or omit for stdin)")
	output := fs.String("o", "", "output Vega JSON file (omit for stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := readInput(*input)
	if err != nil {
		return err
	}

	c, err := aster.New(aster.WithTextMeasurement(false))
	if err != nil {
		return err
	}
	defer func() {
		if e := c.Close(); e != nil && err == nil {
			err = e
		}
	}()

	vgSpec, err := c.VegaLiteToVega(spec)
	if err != nil {
		return err
	}

	// Pretty-print the output JSON.
	var pretty json.RawMessage = vgSpec
	formatted, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		formatted = vgSpec
	}

	return writeOutput(*output, append(formatted, '\n'))
}

func readInput(path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func writeOutput(path string, data []byte) error {
	if path == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// isVegaLite checks the $schema field to auto-detect Vega vs Vega-Lite.
func isVegaLite(spec []byte) bool {
	var s struct {
		Schema string `json:"$schema"`
	}
	if json.Unmarshal(spec, &s) == nil {
		return strings.Contains(s.Schema, "vega-lite")
	}
	// If no $schema, assume Vega-Lite (more common).
	return true
}
