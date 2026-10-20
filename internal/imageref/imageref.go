// Package imageref resolves the image an SVG <image> element refers to, for
// the PNG and PDF writers: the payload of a data: URI, the bytes a loader
// fetches for any other href, and the image they decode to (PNG, JPEG or the
// first frame of a GIF), each within limits. It touches neither the network
// nor the filesystem itself: fetching is the caller's loader.
package imageref

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/url"
	"strings"
	"sync"

	"github.com/mgilbir/aster/internal/budget"
)

// Limits bound one image.
type Limits struct {
	MaxBytes  int // its encoded size
	MaxPixels int // its decoded width times height
	// Err is wrapped by every error a limit causes; it must wrap
	// budget.ErrLimit.
	Err error
}

func (l Limits) over(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{l.Err}, args...)...)
}

// Loader fetches the bytes href refers to. The context carries a
// budget.Budget whose MaxLoadBytes is the most the loader should read.
type Loader func(ctx context.Context, href string) ([]byte, error)

// IsData reports whether href is a data: URI. A URL's scheme is
// case-insensitive (RFC 3986, section 3.1), and resvg's data: URL parser, as
// browsers do, reads "DATA:" as "data:": this is the one test of it that the
// SVG writer, the rasterizer and the PDF writer share, so that no stage takes
// for a resource to fetch what another drew as an inline image.
func IsData(href string) bool {
	href = strings.TrimSpace(href)
	return len(href) >= 5 && strings.EqualFold(href[:5], "data:")
}

// InlineImage reports whether href is a data: URI of an image format the
// engine draws (PNG, JPEG or GIF): part of the document, decoded in place
// rather than fetched. The scheme and the media type are case-insensitive.
func InlineImage(href string) bool {
	if !IsData(href) {
		return false
	}
	meta := strings.TrimSpace(href)[5:]
	for _, t := range [...]string{"image/png", "image/jpeg", "image/gif"} {
		if len(meta) > len(t) && strings.EqualFold(meta[:len(t)], t) && (meta[len(t)] == ';' || meta[len(t)] == ',') {
			return true
		}
	}
	return false
}

// DataURI returns the payload of a data: URI, base64 or percent-encoded.
func DataURI(href string, lim Limits) ([]byte, error) {
	href = strings.TrimSpace(href)
	if !IsData(href) {
		return nil, errors.New("not a data: URI")
	}
	comma := strings.IndexByte(href, ',')
	if comma < 0 {
		return nil, errors.New("malformed data URI")
	}
	meta := href[5:comma]
	payload := href[comma+1:]
	var data []byte
	if strings.HasSuffix(strings.ToLower(meta), ";base64") {
		if base64.StdEncoding.DecodedLen(len(payload)) > lim.MaxBytes+4 {
			return nil, lim.over("image data exceeds %d bytes", lim.MaxBytes)
		}
		// Strip whitespace that XML pretty-printing may have inserted.
		clean := strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\n', '\r', '\t':
				return -1
			}
			return r
		}, payload)
		var err error
		data, err = base64.StdEncoding.DecodeString(clean)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "="))
			if err != nil {
				return nil, fmt.Errorf("invalid base64 image data: %w", err)
			}
		}
	} else {
		if len(payload) > lim.MaxBytes {
			return nil, lim.over("image data exceeds %d bytes", lim.MaxBytes)
		}
		s, err := url.PathUnescape(payload)
		if err != nil {
			return nil, err
		}
		data = []byte(s)
	}
	if len(data) > lim.MaxBytes {
		return nil, lim.over("image data exceeds %d bytes", lim.MaxBytes)
	}
	return data, nil
}

// Config reads the format and size of an encoded image without decoding it,
// and checks them against lim. The format is "png", "jpeg" or "gif".
func Config(data []byte, lim Limits) (image.Config, string, error) {
	if len(data) > lim.MaxBytes {
		return image.Config{}, "", lim.over("image data exceeds %d bytes", lim.MaxBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, "", fmt.Errorf("unsupported image: %w", err)
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return image.Config{}, "", fmt.Errorf("unsupported image format %q", format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 65535 || cfg.Height > 65535 ||
		budget.MulInt(cfg.Width, cfg.Height) > lim.MaxPixels {
		return image.Config{}, "", lim.over("image dimensions %dx%d exceed the limit", cfg.Width, cfg.Height)
	}
	return cfg, format, nil
}

// Decode decodes an image Config accepts, under ctx (nil: never done). The
// standard decoders cannot be interrupted, but they read their input as they
// decode, and Decode polls ctx at each read: between two polls a decoder
// makes at most about a quarter of a million pixels (see pollReader; JPEG and GIF have stretches bounded only by the pixel limit), whatever the image's
// size, and Config has bounded that size before any of it is made. It
// returns ctx's error once ctx is done.
func Decode(ctx context.Context, data []byte, lim Limits) (image.Image, error) {
	_, format, err := Config(data, lim)
	if err != nil {
		return nil, err
	}
	var img image.Image
	r := reader(ctx, data, format)
	switch format {
	case "png":
		img, err = png.Decode(r)
	case "jpeg":
		img, err = jpeg.Decode(r)
	default:
		img, err = gif.Decode(r)
	}
	// A decoder may report the reader's error as its own (a truncated
	// image): ctx's error is what stopped it.
	if ctx != nil && err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
	}
	return img, err
}

const (
	// MaxFetched bounds the distinct images one document may have fetched:
	// each is a request to wherever the loader reaches.
	MaxFetched = 1024
	// fetchParallelism is how many images are fetched at once.
	fetchParallelism = 8
)

// Fetch fetches each of hrefs through load, several at a time, and returns
// the bytes by href. Each fetch's context carries a budget.Budget whose
// MaxLoadBytes is lim.MaxBytes, which the loaders read only up to. A fetch
// that fails leaves its href out, to be drawn as a broken image is: not at
// all. A fetch that fails over a limit fails them all, as does ctx ending,
// and so does one that panics: load runs on goroutines of Fetch's own, where
// a panic would end the process rather than reach the caller's recover
// (C15), so each recovers it and Fetch returns it as an error.
func Fetch(ctx context.Context, hrefs []string, load Loader, lim Limits) (map[string][]byte, error) {
	if load == nil || len(hrefs) == 0 {
		return nil, nil
	}
	if len(hrefs) > MaxFetched {
		return nil, lim.over("more than %d images to fetch", MaxFetched)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	data := make([][]byte, len(hrefs))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		limitErr error // a limit's, or a panic's: it ends every fetch
	)
	fatal := func(err error) {
		mu.Lock()
		if limitErr == nil {
			limitErr = err
			cancel()
		}
		mu.Unlock()
	}
	sem := make(chan struct{}, fetchParallelism)
	for i, href := range hrefs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			defer func() {
				if r := recover(); r != nil {
					fatal(fmt.Errorf("imageref: internal error fetching %q: %v", href, r))
				}
			}()
			b, err := load(budget.With(ctx, &budget.Budget{MaxLoadBytes: int64(lim.MaxBytes)}), href)
			if err == nil && len(b) > lim.MaxBytes {
				err = lim.over("image %q is over %d bytes", href, lim.MaxBytes)
			}
			if err != nil {
				if errors.Is(err, budget.ErrLimit) {
					fatal(err)
				}
				return
			}
			data[i] = b
		}()
	}
	wg.Wait()
	if limitErr != nil {
		return nil, limitErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(hrefs))
	for i, href := range hrefs {
		if data[i] != nil {
			out[href] = data[i]
		}
	}
	return out, nil
}
