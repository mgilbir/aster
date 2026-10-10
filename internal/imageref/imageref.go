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

// IsData reports whether href is a data: URI.
func IsData(href string) bool { return strings.HasPrefix(strings.TrimSpace(href), "data:") }

// DataURI returns the payload of a data: URI, base64 or percent-encoded.
func DataURI(href string, lim Limits) ([]byte, error) {
	href = strings.TrimSpace(href)
	if !strings.HasPrefix(href, "data:") {
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

// Decode decodes an image Config accepts.
func Decode(data []byte, lim Limits) (image.Image, error) {
	_, format, err := Config(data, lim)
	if err != nil {
		return nil, err
	}
	switch format {
	case "png":
		return png.Decode(bytes.NewReader(data))
	case "jpeg":
		return jpeg.Decode(bytes.NewReader(data))
	default:
		return gif.Decode(bytes.NewReader(data))
	}
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
// all. A fetch that fails over a limit fails them all, as does ctx ending.
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
		limitErr error
	)
	sem := make(chan struct{}, fetchParallelism)
	for i, href := range hrefs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			b, err := load(budget.With(ctx, &budget.Budget{MaxLoadBytes: int64(lim.MaxBytes)}), href)
			if err == nil && len(b) > lim.MaxBytes {
				err = lim.over("image %q is over %d bytes", href, lim.MaxBytes)
			}
			if err != nil {
				if errors.Is(err, budget.ErrLimit) {
					mu.Lock()
					if limitErr == nil {
						limitErr = err
						cancel()
					}
					mu.Unlock()
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
