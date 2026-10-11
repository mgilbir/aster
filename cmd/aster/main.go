// Command aster converts Vega and Vega-Lite specs to SVG, PNG, and PDF.
//
// Usage:
//
//	aster svg -i input.vl.json -o output.svg
//	aster png -i input.vl.json -o output.png -scale 2
//	aster pdf -i input.vl.json -o output.pdf
//	cat spec.json | aster svg > output.svg      # stdin/stdout
//	aster compile -i input.vl.json              # Vega-Lite → Vega JSON
//
// Whether the input is Vega or Vega-Lite is read from its $schema, or else
// from its top-level properties (see detectSpec); -from overrides both.
// Warnings are printed to stderr. A failed data or image load, or an error
// raised while the chart is evaluated, fails the command unless -lenient is
// given, which renders past them as a browser does and prints each.
//
// Exit status: 0 on success, 1 on any error, 2 for invalid flags.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	allowPrivate bool
	version      string
	timeout      time.Duration
	from         string
	lenient      bool
}

func registerCommonOpts(fs *flag.FlagSet) *commonOpts {
	co := &commonOpts{fs: fs}
	fs.BoolVar(&co.allowHTTP, "allow-http", false, "allow HTTP(S) data loading from any public host")
	fs.Var(&co.allowDomains, "allow-domain", "restrict HTTP loading to this host (repeatable); implies -allow-http")
	fs.BoolVar(&co.allowPrivate, "allow-private-networks", false, "let HTTP loading reach loopback, link-local and private addresses (denied by default)")
	fs.StringVar(&co.version, "version", "", "Vega-Lite version, e.g. 5.8 or 6.4 (default: build default)")
	fs.DurationVar(&co.timeout, "timeout", 0, "max duration per render, e.g. 30s; 0 disables the timeout (default: 30s)")
	fs.StringVar(&co.from, "from", "auto", "input language: vega, vega-lite, or auto (from $schema, else from the top-level properties)")
	fs.BoolVar(&co.lenient, "lenient", false, "render past failed data and image loads and evaluation errors, as a browser does, printing each to stderr")
	return co
}

// lite reports whether spec is to be rendered as Vega-Lite, per -from.
func (co *commonOpts) lite(spec []byte) (bool, error) {
	switch co.from {
	case "vega-lite":
		return true, nil
	case "vega":
		return false, nil
	case "auto", "":
		k, err := detectSpec(spec)
		if err != nil {
			return false, fmt.Errorf("%w; pass -from vega or -from vega-lite", err)
		}
		return k == specVegaLite, nil
	default:
		return false, fmt.Errorf("invalid -from %q (must be vega, vega-lite or auto)", co.from)
	}
}

// errorHandler prints warnings to stderr and, with -lenient, the errors it
// lets go; without -lenient an error fails the render.
func (co *commonOpts) errorHandler() aster.ErrorHandlerOption {
	return aster.WithErrorHandler(func(err error) error {
		var w aster.Warning
		switch {
		case errors.As(err, &w):
			fmt.Fprintf(os.Stderr, "aster: warning: %v\n", err)
		case co.lenient:
			fmt.Fprintf(os.Stderr, "aster: ignored: %v\n", err)
		default:
			return withLoadHint(err)
		}
		return nil
	})
}

// withLoadHint adds what to do to an error for a load the default loader
// refused.
func withLoadHint(err error) error {
	var le *aster.LoadError
	if errors.As(err, &le) && strings.Contains(le.Err.Error(), "no loader configured") {
		return fmt.Errorf("%w (pass -allow-http or -allow-domain to load over HTTP, or -lenient to render without it)", err)
	}
	return err
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
		opts = append(opts, aster.WithLoader(&aster.HTTPLoader{AllowedDomains: co.allowDomains, AllowPrivateNetworks: co.allowPrivate}))
	case co.allowHTTP:
		opts = append(opts, aster.WithLoader(&aster.HTTPLoader{AllowPrivateNetworks: co.allowPrivate}))
	}
	return opts, nil
}

// signalFlag collects the repeatable -signal name=value flag of the rendering
// subcommands. A value that parses as JSON is that JSON value; any other is a
// string.
type signalFlag []aster.SignalOption

func (s *signalFlag) String() string { return "" }

func (s *signalFlag) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if !ok || name == "" {
		return fmt.Errorf("want name=value, got %q", v)
	}
	if json.Valid([]byte(value)) {
		*s = append(*s, aster.WithSignal(name, json.RawMessage(value)))
	} else {
		*s = append(*s, aster.WithSignal(name, value))
	}
	return nil
}

func registerSignals(fs *flag.FlagSet) *signalFlag {
	s := &signalFlag{}
	fs.Var(s, "signal", "set a top-level signal (a Vega-Lite parameter) before rendering, as name=value, value JSON or else a string (repeatable)")
	return s
}

func runSVG(args []string) (err error) {
	fs := flag.NewFlagSet("svg", flag.ExitOnError)
	input := fs.String("i", "", "input spec file (- or omit for stdin)")
	output := fs.String("o", "", "output SVG file (omit for stdout)")
	co := registerCommonOpts(fs)
	signals := registerSignals(fs)
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

	lite, err := co.lite(spec)
	if err != nil {
		return err
	}
	renderOpts := []aster.RenderOption{co.errorHandler()}
	for _, s := range *signals {
		renderOpts = append(renderOpts, s)
	}
	var svg string
	if lite {
		svg, err = c.VegaLiteToSVG(spec, renderOpts...)
	} else {
		svg, err = c.VegaToSVG(spec, renderOpts...)
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
	signals := registerSignals(fs)
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

	lite, err := co.lite(spec)
	if err != nil {
		return err
	}
	pngOpts := []aster.PNGOption{co.errorHandler()}
	if *scale != 1.0 {
		pngOpts = append(pngOpts, aster.WithScale(*scale))
	}
	if *recode {
		pngOpts = append(pngOpts, aster.WithRecodePNG())
	}
	for _, s := range *signals {
		pngOpts = append(pngOpts, s)
	}

	var data []byte
	if lite {
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
	textMode := fs.String("text", "embed", "PDF text mode: embed (subset fonts, selectable text), named (no font program; the assembling document must embed the same font), or outlines (glyphs as paths)")
	co := registerCommonOpts(fs)
	signals := registerSignals(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	pdfOpts := []aster.PDFOption{co.errorHandler()}
	switch *textMode {
	case "embed":
		// library default
	case "named":
		pdfOpts = append(pdfOpts, aster.WithPDFText(aster.PDFTextNamed))
	case "outlines":
		pdfOpts = append(pdfOpts, aster.WithPDFText(aster.PDFTextOutlines))
	default:
		return fmt.Errorf("invalid -text %q (must be embed, named, or outlines)", *textMode)
	}
	for _, s := range *signals {
		pdfOpts = append(pdfOpts, s)
	}

	spec, err := readInput(*input)
	if err != nil {
		return err
	}
	lite, err := co.lite(spec)
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
	if lite {
		data, err = c.VegaLiteToPDF(spec, pdfOpts...)
	} else {
		data, err = c.VegaToPDF(spec, pdfOpts...)
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
	if k, err := detectSpec(spec); err == nil && k == specVega {
		return errors.New("the input is a Vega specification; compile takes Vega-Lite")
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

type specKind int

const (
	specVega specKind = iota
	specVegaLite
)

// The top-level properties only one of the two languages has. Vega-Lite's
// are a view (mark, encoding, a composition) and what only Vega-Lite allows
// beside one; Vega's are its arrays of marks, signals, scales, guides and
// projections. Both share data, width, height, padding, autosize, config,
// background, title, description and usermeta, and a data that is an object
// is Vega-Lite's, an array Vega's.
var (
	vegaLiteOnly = []string{"mark", "encoding", "layer", "facet", "repeat", "concat", "hconcat", "vconcat", "spec", "params", "transform", "resolve", "selection", "datasets", "projection", "view"}
	vegaOnly     = []string{"marks", "signals", "scales", "axes", "legends", "projections", "encode"}
)

// detectSpec tells a Vega specification from a Vega-Lite one: by its $schema
// when it names either language's schema, else by its top-level properties.
// A specification with properties of both, or of neither, is an error rather
// than a guess, since rendering one language as the other draws an empty
// chart without failing.
func detectSpec(spec []byte) (specKind, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(spec, &top); err != nil {
		return 0, fmt.Errorf("the input is not a JSON object: %w", err)
	}
	var schema string
	if raw, ok := top["$schema"]; ok && json.Unmarshal(raw, &schema) == nil {
		switch {
		case strings.Contains(schema, "/vega-lite/"):
			return specVegaLite, nil
		case strings.Contains(schema, "/vega/"):
			return specVega, nil
		}
	}
	lite, vega := has(top, vegaLiteOnly), has(top, vegaOnly)
	if d := bytes.TrimSpace(top["data"]); len(d) > 0 {
		switch d[0] {
		case '{':
			lite = append(lite, "data (an object)")
		case '[':
			vega = append(vega, "data (an array)")
		}
	}
	switch {
	case len(lite) > 0 && len(vega) == 0:
		return specVegaLite, nil
	case len(vega) > 0 && len(lite) == 0:
		return specVega, nil
	case len(lite) > 0:
		return 0, fmt.Errorf("cannot tell whether the input is Vega or Vega-Lite: it has no $schema, and has Vega-Lite's %s and Vega's %s",
			strings.Join(lite, ", "), strings.Join(vega, ", "))
	}
	return 0, errors.New("cannot tell whether the input is Vega or Vega-Lite: it has no $schema and none of the properties that tell them apart")
}

// has lists the keys of top among names.
func has(top map[string]json.RawMessage, names []string) []string {
	var found []string
	for _, n := range names {
		if _, ok := top[n]; ok {
			found = append(found, n)
		}
	}
	return found
}
