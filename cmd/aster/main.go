// Command aster converts Vega and Vega-Lite specs to SVG, PNG, and PDF.
//
// Usage:
//
//	aster svg -i input.vl.json -o output.svg
//	aster png -i input.vl.json -o output.png -scale 2
//	aster pdf -i input.vl.json -o output.pdf
//	cat spec.json | aster svg > output.svg      # stdin/stdout
//	aster compile -i input.vl.json              # Vega-Lite → Vega JSON
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mgilbir/aster"
)

func main() {
	if err := run(); err != nil {
		// Library errors are already namespaced ("aster:" / "aster/runtime:");
		// only add the program prefix to errors that aren't (flag parsing,
		// file I/O) so we don't print "aster: aster: ...".
		msg := err.Error()
		if strings.HasPrefix(msg, "aster") {
			fmt.Fprintln(os.Stderr, msg)
		} else {
			fmt.Fprintf(os.Stderr, "aster: %s\n", msg)
		}
		os.Exit(1)
	}
}

const usage = `usage: aster <command> [flags]

Commands:
  svg      Render spec to SVG
  png      Render spec to PNG
  pdf      Render spec to vector PDF
  compile  Compile Vega-Lite to Vega JSON

Run "aster <command> -h" for command-specific flags.`

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("%s", usage)
	}

	command := os.Args[1]
	switch command {
	case "svg":
		return runSVG(os.Args[2:])
	case "png":
		return runPNG(os.Args[2:])
	case "pdf":
		return runPDF(os.Args[2:])
	case "compile":
		return runCompile(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Println(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usage)
	}
}

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// commonOpts holds flags shared by the rendering subcommands and turns them
// into aster options.
type commonOpts struct {
	fs           *flag.FlagSet
	allowHTTP    bool
	allowDomains stringList
	version      string
	timeout      time.Duration
}

func registerCommonOpts(fs *flag.FlagSet) *commonOpts {
	co := &commonOpts{fs: fs}
	fs.BoolVar(&co.allowHTTP, "allow-http", false, "allow HTTP(S) data loading from any host")
	fs.Var(&co.allowDomains, "allow-domain", "restrict HTTP loading to this host (repeatable); implies -allow-http")
	fs.StringVar(&co.version, "version", "", "Vega-Lite version, e.g. 5.8 or 6.4 (default: build default)")
	fs.DurationVar(&co.timeout, "timeout", 0, "max duration per render, e.g. 30s; 0 disables the timeout (default: 30s)")
	return co
}

func (co *commonOpts) options() ([]aster.Option, error) {
	var opts []aster.Option
	if co.version != "" {
		opts = append(opts, aster.WithVegaLiteVersion(co.version))
	}
	// Only an explicitly-set -timeout overrides the library default, so that
	// -timeout 0 means "no timeout" (slow geo specs can exceed 30s) rather
	// than silently meaning "default".
	timeoutSet := false
	co.fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" {
			timeoutSet = true
		}
	})
	if co.timeout < 0 {
		return nil, fmt.Errorf("invalid -timeout %v (must be >= 0; 0 disables the timeout)", co.timeout)
	}
	if timeoutSet {
		opts = append(opts, aster.WithTimeout(co.timeout))
	}
	switch {
	case len(co.allowDomains) > 0:
		opts = append(opts, aster.WithLoader(&aster.HTTPLoader{AllowedDomains: co.allowDomains}))
	case co.allowHTTP:
		opts = append(opts, aster.WithLoader(aster.NewHTTPLoader(nil)))
	}
	return opts, nil
}

func runSVG(args []string) (err error) {
	fs := flag.NewFlagSet("svg", flag.ExitOnError)
	input := fs.String("i", "", "input spec file (- or omit for stdin)")
	output := fs.String("o", "", "output SVG file (omit for stdout)")
	co := registerCommonOpts(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := readInput(*input)
	if err != nil {
		return err
	}

	opts, err := co.options()
	if err != nil {
		return err
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

func runPNG(args []string) (err error) {
	fs := flag.NewFlagSet("png", flag.ExitOnError)
	input := fs.String("i", "", "input spec file (- or omit for stdin)")
	output := fs.String("o", "", "output PNG file (omit for stdout)")
	scale := fs.Float64("scale", 1.0, "scale factor; 2 produces 2x dimensions")
	recode := fs.Bool("recode", false, "losslessly re-encode the PNG into a smaller equivalent format")
	co := registerCommonOpts(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := readInput(*input)
	if err != nil {
		return err
	}

	opts, err := co.options()
	if err != nil {
		return err
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

	var pngOpts []aster.PNGOption
	if *scale != 1.0 {
		pngOpts = append(pngOpts, aster.WithScale(*scale))
	}
	if *recode {
		pngOpts = append(pngOpts, aster.WithRecodePNG())
	}

	var data []byte
	if isVegaLite(spec) {
		data, err = c.VegaLiteToPNG(spec, pngOpts...)
	} else {
		data, err = c.VegaToPNG(spec, pngOpts...)
	}
	if err != nil {
		return err
	}

	return writeOutput(*output, data)
}

func runPDF(args []string) (err error) {
	fs := flag.NewFlagSet("pdf", flag.ExitOnError)
	input := fs.String("i", "", "input spec file (- or omit for stdin)")
	output := fs.String("o", "", "output PDF file (omit for stdout)")
	co := registerCommonOpts(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := readInput(*input)
	if err != nil {
		return err
	}

	opts, err := co.options()
	if err != nil {
		return err
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

	var data []byte
	if isVegaLite(spec) {
		data, err = c.VegaLiteToPDF(spec)
	} else {
		data, err = c.VegaToPDF(spec)
	}
	if err != nil {
		return err
	}

	return writeOutput(*output, data)
}

func runCompile(args []string) (err error) {
	fs := flag.NewFlagSet("compile", flag.ExitOnError)
	input := fs.String("i", "", "input Vega-Lite spec file (- or omit for stdin)")
	output := fs.String("o", "", "output Vega JSON file (omit for stdout)")
	version := fs.String("version", "", "Vega-Lite version, e.g. 5.8 or 6.4 (default: build default)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := readInput(*input)
	if err != nil {
		return err
	}

	opts := []aster.Option{aster.WithTextMeasurement(false)}
	if *version != "" {
		opts = append(opts, aster.WithVegaLiteVersion(*version))
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
