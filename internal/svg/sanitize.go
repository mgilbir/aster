package svg

import "github.com/mgilbir/aster/internal/loader"

// URLOptions are the loader options that shape sanitized URLs, mirroring the
// vega-loader options the scenegraph passes through.
type URLOptions struct {
	// BaseURL is prepended to relative URLs.
	BaseURL string
	// DefaultProtocol replaces the missing protocol of `//host` URLs ("http"
	// when empty); "file" drops the `//`, as upstream does for a file load.
	DefaultProtocol string
	// Target and Rel become attributes of the <a> element when non-empty.
	Target, Rel string
}

// HrefAttr is one attribute of the <a> element for a hyperlinked item.
type HrefAttr struct{ Name, Value string }

// SanitizeURL applies vega-loader's URI sanitizer (loader.SanitizeURI, the one
// implementation the Loaders and the writer share). ok is false for URIs it
// rejects (for example `javascript:` URLs); upstream then renders no link.
func SanitizeURL(uri string, opt URLOptions) (href string, ok bool) {
	href, err := loader.SanitizeURI(uri, loader.URIOptions{BaseURL: opt.BaseURL, DefaultProtocol: opt.DefaultProtocol})
	return href, err == nil
}

// DefaultHref is the link attributes vega emits for an href: the configured
// target and rel, then xlink:href with the sanitized URL. The order matters for
// byte compatibility.
func DefaultHref(opt URLOptions) func(uri string) ([]HrefAttr, bool) {
	return func(uri string) ([]HrefAttr, bool) {
		href, ok := SanitizeURL(uri, opt)
		if !ok {
			return nil, false
		}
		var attrs []HrefAttr
		if opt.Target != "" {
			attrs = append(attrs, HrefAttr{"target", opt.Target})
		}
		if opt.Rel != "" {
			attrs = append(attrs, HrefAttr{"rel", opt.Rel})
		}
		return append(attrs, HrefAttr{"xlink:href", href}), true
	}
}
