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
// Exit status: 0 on success, 1 on any error, 2 for an invalid command line:
// an unknown command or flag, or a flag value that is not valid.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mgilbir/aster"
)

func main() { os.Exit(cliMain(os.Args[1:], os.Stderr)) }

// cliMain runs the command line args and returns the exit status, printing
// any error to stderr.
func cliMain(args []string, stderr io.Writer) int {
	err := run(args)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var ue *usageError
	if errors.As(err, &ue) && ue.printed {
		return 2 // the flag package printed it, and the usage
	}
	// Library errors are already namespaced ("aster:" / "aster/runtime:");
	// only add the program prefix to errors that aren't (flag values, file
	// I/O) so we don't print "aster: aster: ...".
	msg := err.Error()
	if strings.HasPrefix(msg, "aster") {
		_, _ = fmt.Fprintln(stderr, msg)
	} else {
		_, _ = fmt.Fprintf(stderr, "aster: %s\n", msg)
	}
	if ue != nil {
		return 2
	}
	return 1
}

// usageError is an invalid command line, which exits with status 2.
type usageError struct {
	err     error
	printed bool // by the flag package, with the usage
}

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// invalid is an invalid flag value of the command fs parsed.
func invalid(fs *flag.FlagSet, format string, args ...any) error {
	return &usageError{err: fmt.Errorf(format+" (see aster %s -h)", append(args, fs.Name())...)}
}

// parse parses args with fs, which continues on error: the flag package has
// printed the error and the usage, and the exit status is 2.
func parse(fs *flag.FlagSet, args []string) error {
	fs.Init(fs.Name(), flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return &usageError{err: err, printed: true}
	}
	return nil
}

const usage = `usage: aster <command> [flags]

Commands:
  svg      Render spec to SVG
  png      Render spec to PNG
  pdf      Render spec to vector PDF
  compile  Compile Vega-Lite to Vega JSON

Run "aster <command> -h" for command-specific flags.`

func run(args []string) error {
	if len(args) < 1 {
		return &usageError{err: errors.New(usage)}
	}

	command := args[0]
	switch command {
	case "svg":
		return runSVG(args[1:])
	case "png":
		return runPNG(args[1:])
	case "pdf":
		return runPDF(args[1:])
	case "compile":
		return runCompile(args[1:])
	case "-h", "--help", "help":
		fmt.Println(usage)
		return nil
	default:
		return &usageError{err: fmt.Errorf("unknown command %q\n\n%s", command, usage)}
	}
}

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// configOpts are the flags that configure the converter, shared by every
// subcommand.
type configOpts struct {
	fs          *flag.FlagSet
	version     string
	timeout     time.Duration
	memoryLimit string
	timezone    string
	theme       string // the theme file's JSON
}

func registerConfigOpts(fs *flag.FlagSet) *configOpts {
	co := &configOpts{fs: fs}
	fs.StringVar(&co.version, "version", "", "Vega-Lite version, e.g. 5.8 or 6.4 (default: build default)")
	fs.DurationVar(&co.timeout, "timeout", 0, "max duration per render, e.g. 30s; 0 disables the timeout (default: 30s)")
	fs.StringVar(&co.memoryLimit, "memory-limit", "", "the memory budgets of a render, from one size: bytes, or with a unit, e.g. 256MiB or 1GiB (default: the fixed defaults; see docs/limits.md)")
	fs.StringVar(&co.timezone, "timezone", "", "IANA time zone for local-time operations, e.g. America/New_York (default: UTC)")
	fs.Func("theme", "a Vega theme config: a JSON file whose object is applied to every render", func(path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		co.theme = string(b)
		return nil
	})
	return co
}

// options turns the flags into aster options.
func (co *configOpts) options() ([]aster.Option, error) {
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
		return nil, invalid(co.fs, "invalid -timeout %v (must be >= 0; 0 disables the timeout)", co.timeout)
	}
	if timeoutSet {
		opts = append(opts, aster.WithTimeout(co.timeout))
	}
	if co.memoryLimit != "" {
		n, err := parseSize(co.memoryLimit)
		if err != nil {
			return nil, invalid(co.fs, "invalid -memory-limit %q: %v", co.memoryLimit, err)
		}
		opts = append(opts, aster.WithMemoryLimit(n))
	}
	if co.timezone != "" {
		opts = append(opts, aster.WithTimezone(co.timezone))
	}
	if co.theme != "" {
		opts = append(opts, aster.WithTheme(co.theme))
	}
	return opts, nil
}

// parseSize reads a byte size: a whole number, then optionally a unit,
// binary (KiB, MiB, GiB) or decimal (KB, MB, GB).
func parseSize(s string) (uint64, error) {
	units := []struct {
		suffix string
		mult   uint64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"B", 1}}
	num, mult := strings.TrimSpace(s), uint64(1)
	for _, u := range units {
		if strings.HasSuffix(num, u.suffix) {
			num, mult = strings.TrimSpace(strings.TrimSuffix(num, u.suffix)), u.mult
			break
		}
	}
	n, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		return 0, errors.New("want a number of bytes, optionally with a unit such as MiB")
	}
	if n > (1<<64-1)/mult {
		return 0, errors.New("too large")
	}
	return n * mult, nil
}

// newConverter is aster.New with the options the flags give. Every error New
// returns is an option's, so a flag's: an invalid command line.
func newConverter(fs *flag.FlagSet, opts []aster.Option) (*aster.Converter, error) {
	c, err := aster.New(opts...)
	if err != nil {
		return nil, &usageError{err: fmt.Errorf("%w (see aster %s -h)", err, fs.Name())}
	}
	return c, nil
}

// commonOpts holds flags shared by the rendering subcommands and turns them
// into aster options.
type commonOpts struct {
	*configOpts
	allowHTTP    bool
	allowDomains stringList
	allowPrivate bool
	allowFiles   stringList
	baseURL      string
	fonts        []aster.Option
	from         string
	lenient      bool
}

func registerCommonOpts(fs *flag.FlagSet) *commonOpts {
	co := &commonOpts{configOpts: registerConfigOpts(fs)}
	fs.BoolVar(&co.allowHTTP, "allow-http", false, "allow HTTP(S) data loading from any public host")
	fs.Var(&co.allowDomains, "allow-domain", "restrict HTTP loading to this host (repeatable); implies -allow-http")
	fs.BoolVar(&co.allowPrivate, "allow-private-networks", false, "let HTTP loading reach loopback, link-local and private addresses (denied by default)")
	fs.Var(&co.allowFiles, "allow-file", "load data and images named by a relative path from the files under this directory (repeatable: the first that has the file serves it)")
	fs.StringVar(&co.baseURL, "base-url", "", "resolve relative data and image URLs against this http(s) URL, and load them over HTTP; implies -allow-http")
	fs.Func("font", "register a font file under a family name, as FAMILY=FILE (repeatable)", func(v string) error {
		family, path, ok := strings.Cut(v, "=")
		if !ok || family == "" || path == "" {
			return fmt.Errorf("want FAMILY=FILE, got %q", v)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		co.fonts = append(co.fonts, aster.WithFont(family, b))
		return nil
	})
	fs.StringVar(&co.from, "from", "auto", "input language: vega, vega-lite, or auto (from $schema, else from the top-level properties)")
	fs.BoolVar(&co.lenient, "lenient", false, "render past failed data and image loads and evaluation errors, as a browser does, printing each to stderr")
	return co
}

// check validates the flag values that need no input, before any is read.
func (co *commonOpts) check() error {
	switch co.from {
	case "auto", "", "vega", "vega-lite":
	default:
		return invalid(co.fs, "invalid -from %q (must be vega, vega-lite or auto)", co.from)
	}
	if co.baseURL != "" {
		u, err := url.Parse(co.baseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return invalid(co.fs, "invalid -base-url %q (must be an absolute http or https URL)", co.baseURL)
		}
	}
	return nil
}

// lite reports whether spec is to be rendered as Vega-Lite, per -from.
func (co *commonOpts) lite(spec []byte) (bool, error) {
	switch co.from {
	case "vega-lite":
		return true, nil
	case "vega":
		return false, nil
	default:
		k, err := detectSpec(spec)
		if err != nil {
			return false, fmt.Errorf("%w; pass -from vega or -from vega-lite", err)
		}
		return k == specVegaLite, nil
	}
}

// httpAllowed reports whether a flag lets data load over HTTP.
func (co *commonOpts) httpAllowed() bool {
	return co.allowHTTP || len(co.allowDomains) > 0 || co.baseURL != ""
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
			return co.withLoadHint(err)
		}
		return nil
	})
}

// withLoadHint adds what to do to the error of a load no flag allowed.
func (co *commonOpts) withLoadHint(err error) error {
	var le *aster.LoadError
	if !errors.As(err, &le) || le.URL == "" {
		return err
	}
	u, perr := url.Parse(le.URL)
	switch {
	case perr == nil && u.Scheme == "" && len(co.allowFiles) == 0 && co.baseURL == "":
		return fmt.Errorf("%w (a relative URL: pass -allow-file DIR to read it from the files under DIR, or -base-url URL to load it over HTTP; or -lenient to render without it)", err)
	case perr == nil && (u.Scheme == "http" || u.Scheme == "https") && !co.httpAllowed():
		return fmt.Errorf("%w (pass -allow-http or -allow-domain to load over HTTP, or -lenient to render without it)", err)
	}
	return err
}

func (co *commonOpts) options() ([]aster.Option, error) {
	opts, err := co.configOpts.options()
	if err != nil {
		return nil, err
	}
	opts = append(opts, co.fonts...)
	// Files first, then HTTP: a relative URL is a file under one of the
	// directories when there is one, else it is resolved against -base-url.
	var loaders []aster.Loader
	for _, dir := range co.allowFiles {
		fl, err := aster.NewFileLoader(dir)
		if err != nil {
			return nil, invalid(co.fs, "invalid -allow-file %q: %v", dir, err)
		}
		loaders = append(loaders, fl)
	}
	if co.httpAllowed() {
		loaders = append(loaders, &aster.HTTPLoader{AllowedDomains: co.allowDomains, BaseURL: co.baseURL, AllowPrivateNetworks: co.allowPrivate})
	}
	switch len(loaders) {
	case 0:
	case 1:
		opts = append(opts, aster.WithLoader(loaders[0]))
	default:
		opts = append(opts, aster.WithLoader(aster.NewFallbackLoader(loaders...)))
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

// render is the part of svg, png and pdf they share: parse and check the
// flags, read the input, make the converter, and tell the language. It
// returns the converter, which the caller closes, the spec and whether it is
// Vega-Lite.
func render(fs *flag.FlagSet, co *commonOpts, input *string, args []string, check func() error) (c *aster.Converter, spec []byte, lite bool, err error) {
	if err := parse(fs, args); err != nil {
		return nil, nil, false, err
	}
	if err := co.check(); err != nil {
		return nil, nil, false, err
	}
	if check != nil {
		if err := check(); err != nil {
			return nil, nil, false, err
		}
	}
	opts, err := co.options()
	if err != nil {
		return nil, nil, false, err
	}
	c, err = newConverter(fs, opts)
	if err != nil {
		return nil, nil, false, err
	}
	spec, err = readInput(*input)
	if err == nil {
		lite, err = co.lite(spec)
	}
	if err != nil {
		_ = c.Close()
		return nil, nil, false, err
	}
	return c, spec, lite, nil
}

// closeInto closes c, keeping the first error.
func closeInto(c *aster.Converter, err *error) {
	if e := c.Close(); e != nil && *err == nil {
		*err = e
	}
}

func runSVG(args []string) (err error) {
	fs := flag.NewFlagSet("svg", flag.ContinueOnError)
	input := fs.String("i", "", "input spec file (- or omit for stdin)")
	output := fs.String("o", "", "output SVG file (omit for stdout)")
	co := registerCommonOpts(fs)
	signals := registerSignals(fs)
	c, spec, lite, err := render(fs, co, input, args, nil)
	if err != nil {
		return err
	}
	defer closeInto(c, &err)

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
	fs := flag.NewFlagSet("png", flag.ContinueOnError)
	input := fs.String("i", "", "input spec file (- or omit for stdin)")
	output := fs.String("o", "", "output PNG file (omit for stdout)")
	scale := fs.Float64("scale", 1.0, "scale factor; 2 produces 2x dimensions")
	recode := fs.Bool("recode", false, "losslessly re-encode the PNG into a smaller equivalent format")
	co := registerCommonOpts(fs)
	signals := registerSignals(fs)
	c, spec, lite, err := render(fs, co, input, args, func() error {
		if !(*scale > 0) || math.IsInf(*scale, 1) {
			return invalid(fs, "invalid -scale %v (must be a positive, finite number)", *scale)
		}
		return nil
	})
	if err != nil {
		return err
	}
	defer closeInto(c, &err)

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
	fs := flag.NewFlagSet("pdf", flag.ContinueOnError)
	input := fs.String("i", "", "input spec file (- or omit for stdin)")
	output := fs.String("o", "", "output PDF file (omit for stdout)")
	textMode := fs.String("text", "embed", "PDF text mode: embed (subset fonts, selectable text), named (no font program; the assembling document must embed the same font), or outlines (glyphs as paths)")
	co := registerCommonOpts(fs)
	signals := registerSignals(fs)
	pdfOpts := []aster.PDFOption{co.errorHandler()}
	c, spec, lite, err := render(fs, co, input, args, func() error {
		switch *textMode {
		case "embed":
			// library default
		case "named":
			pdfOpts = append(pdfOpts, aster.WithPDFText(aster.PDFTextNamed))
		case "outlines":
			pdfOpts = append(pdfOpts, aster.WithPDFText(aster.PDFTextOutlines))
		default:
			return invalid(fs, "invalid -text %q (must be embed, named, or outlines)", *textMode)
		}
		return nil
	})
	if err != nil {
		return err
	}
	defer closeInto(c, &err)

	for _, s := range *signals {
		pdfOpts = append(pdfOpts, s)
	}
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
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	input := fs.String("i", "", "input Vega-Lite spec file (- or omit for stdin)")
	output := fs.String("o", "", "output Vega JSON file (omit for stdout)")
	co := registerConfigOpts(fs)
	if err := parse(fs, args); err != nil {
		return err
	}
	opts, err := co.options()
	if err != nil {
		return err
	}
	c, err := newConverter(fs, append(opts, aster.WithTextMeasurement(false)))
	if err != nil {
		return err
	}
	defer closeInto(c, &err)

	spec, err := readInput(*input)
	if err != nil {
		return err
	}
	if k, err := detectSpec(spec); err == nil && k == specVega {
		return errors.New("the input is a Vega specification; compile takes Vega-Lite")
	}
	// The compiler's warnings are printed, as a render's are.
	vgSpec, err := c.VegaLiteToVega(spec, aster.WithErrorHandler(func(err error) error {
		var w aster.Warning
		if errors.As(err, &w) {
			_, _ = fmt.Fprintf(os.Stderr, "aster: warning: %v\n", err)
			return nil
		}
		return err
	}))
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
