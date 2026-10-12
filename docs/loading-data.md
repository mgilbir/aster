# Loading data and images

A specification can name resources by URL: a data set's `"url"`, an image mark's `"url"`, a link's `"href"`. The `Loader` you give `WithLoader` decides which of them a render may use and fetches them. The default, `DenyLoader`, refuses all of them, so a chart whose data lives at a URL fails with an [`*aster.LoadError`](errors.md) until you choose a loader:

```go
// HTTP(S) to public hosts:
c, err := aster.New(aster.WithLoader(aster.NewHTTPLoader(nil)))

// ...only to these hosts, with relative URLs resolved against a base:
c, err := aster.New(aster.WithLoader(&aster.HTTPLoader{
    AllowedDomains: []string{"cdn.jsdelivr.net"},
    BaseURL:        "https://cdn.jsdelivr.net/npm/vega-datasets@3.2.1/data/",
}))

// Files under a directory (relative paths only, contained with os.Root):
c, err := aster.New(aster.WithLoader(&aster.FileLoader{BaseDir: "./data"}))

// Local files first, then HTTP:
c, err := aster.New(aster.WithLoader(aster.NewFallbackLoader(
    &aster.FileLoader{BaseDir: "./data"},
    aster.NewHTTPLoader(nil),
)))
```

CLI: `-allow-http` (public hosts), `-allow-domain host` (repeatable; implies `-allow-http`), `-allow-private-networks`, `-base-url URL` (relative URLs over HTTP; implies `-allow-http`), `-allow-file DIR` (repeatable: relative paths from the files under `DIR`). Given together, the directories are tried first, in order, then HTTP, as a `FallbackLoader` tries its children. See [cli.md](cli.md).

Inline data (`"values"`) and embedded PNG, JPEG and GIF images (`data:image/png;base64,…`) need no loader. Other `data:` URLs go through it like any URL.

## The loaders

| Loader | Accepts | Notes |
|---|---|---|
| `DenyLoader{}` | nothing | the default; `WithLoader(nil)` is the same |
| `HTTPLoader` / `NewHTTPLoader(client)` | absolute `http`/`https` URLs, and relative ones when `BaseURL` is set | public addresses only unless `AllowPrivateNetworks`; see below |
| `FileLoader{BaseDir}` / `NewFileLoader(dir)` | relative paths inside `BaseDir` | `NewFileLoader` opens the directory at once (error if missing); the zero-value struct opens it on first use |
| `StaticLoader{Value}` | every URI | returns `Value` as JSON whatever the URI; for tests |
| `FallbackLoader` / `NewFallbackLoader(...)` | what any child accepts | tries children in order; the first whose `Sanitize` accepts *and* whose `Load` succeeds answers. Its `Sanitize` returns what the first accepting child made of the URI (an `HTTPLoader`'s `BaseURL` resolved), and its `Load` routes that through the children again, so each child's `Sanitize` must be idempotent (see below) |

### HTTPLoader fields

| Field | Default | Meaning |
|---|---|---|
| `Client *http.Client` | a client of the package's own (bounded connect, TLS and header waits; honours `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`) | `http.DefaultClient` selects the same |
| `AllowedDomains []string` | empty: every public host | exact host names, case-insensitive, one trailing dot ignored, ASCII only (write IDNs in punycode) |
| `BaseURL string` | empty: only absolute URLs | relative URIs are resolved against it |
| `MaxResponseBytes int64` | 0 = 64 MiB | body cap after decompression; negative = none. Over the cap: `ErrLimit` |
| `Timeout time.Duration` | 0 = 60 s | one request, redirects and body included; negative = none |
| `AllowPrivateNetworks bool` | false | let requests reach loopback, link-local, private, CGNAT, multicast and unspecified addresses |
| `BlockPrivateNetworks bool` | — | **deprecated, ignored**: private networks are blocked unless `AllowPrivateNetworks` |

What it refuses: non-HTTP schemes (`ftp:`, `file:`, `data:`, `javascript:`), URLs with userinfo (`user:pass@host`), hosts outside `AllowedDomains`, and (by default) any non-public address. The address check is made on the address a connection is actually made to, in every spelling of it (`2130706433`, `0x7f.1`, `::ffff:127.0.0.1`, NAT64, 6to4), so names that resolve inward, DNS rebinding and redirects don't get around it. The same policy is applied to every redirect hop (at most 10).

Two cases weaken the address check, and both are deliberate:

- **A proxy.** The proxy resolves the name itself, so a proxied request gets only the URL checks (address literals, `localhost` names, `AllowedDomains`). Deny internal destinations at the proxy, or use `AllowedDomains`.
- **A custom `Client`** whose `Transport` is not an `*http.Transport`. It chooses where it connects, so only address literals and `localhost` are refused. Combine it with `AllowedDomains`.

An `*http.Transport` of your own is checked on every path it dials: its `DialContext` and `DialTLSContext` (and the deprecated `Dial` and `DialTLS`, used as their context forms) are wrapped with the check, and when it sets no plain dial function the loader's guarded dialer makes the connection, not net/http's own. A refused connection's error says the host is private, not which address its name resolved to: the error reaches whoever wrote the URL.

### FileLoader fields

`BaseDir string` is the directory. `MaxBytes int64` caps one file: 0 means 64 MiB, negative means none, and a larger file is `ErrLimit`. It refuses absolute paths, `..`, control characters, URIs with a scheme, symlinks that escape `BaseDir`, and anything that is not a regular file. The URI is a path, so nothing is percent-decoded, and `22:48.csv` is an ordinary name.

## What goes through the Loader

| Resource | Methods called | When |
|---|---|---|
| a data set's `url` | `Sanitize`, then `Load` | during the render, once per data set |
| an image mark's `url` | `Sanitize`; `Load` of what it returned when the image is drawn (PNG, PDF) or its size is needed (no `width`/`height`) | each distinct URL once per call, shared by the SVG, PNG and PDF stages: PNG and PDF draw what the SVG stage resolved, and do not sanitize the href it wrote again |
| an `<image href>` in SVG given to `SVGToPNG`/`SVGToPDF` | `Sanitize`, then `Load` | once per distinct href |
| a mark's `href` (a link in the SVG) | `Sanitize` only; the returned string is what is written | once per distinct URL per call, while writing SVG |

Links are also checked against vega-loader's scheme allow-list after your `Sanitize`, so a permissive Loader cannot put a `javascript:` URL into the output. A refused link is left out and reported as a [`Warning`](errors.md).

## Writing a Loader

```go
type Loader interface {
    // Sanitize validates and may rewrite uri before it is loaded or written into
    // the output. Return an error to refuse it.
    Sanitize(ctx context.Context, uri string) (string, error)
    // Load returns the bytes at uri, as Sanitize returned it.
    Load(ctx context.Context, uri string) ([]byte, error)
}
```

- **Concurrency.** One Converter calls its Loader from many goroutines at once, and one call fetches images in parallel. It must be safe for concurrent use.
- **Bytes.** Data is read as UTF-8 text (JSON, CSV, TSV, DSV, TopoJSON). Images are binary (PNG, JPEG, GIF), so return them unchanged.
- **What the engine calls.** For each resource, `Sanitize` once per call with the URL the specification names, then `Load` with exactly the string `Sanitize` returned, never `Sanitize` of that answer. So a `Sanitize` that rewrites (logical names to addresses, a proxy) need not accept its own output. A `FallbackLoader`'s children are the exception: it routes what a child returned through the children again, so a child of yours should return its own answer unchanged when given it (`Sanitize(Sanitize(u)) == Sanitize(u)`), as the loaders of this package do.
- **Errors.** An error from `Sanitize` or `Load` becomes the `Err` of the `*aster.LoadError` the caller sees, whose `URL` is the specification's, whatever `Sanitize` made of it; each failing resource is reported once per call. An error wrapping `aster.ErrLimit` is a limit: it fails the call even under an error handler, and a `FallbackLoader` does not try the next child.
- **Cancellation.** Honour `ctx`, which carries the call's deadline, the cancellation of `Close` and of the caller's `WithContext` context, and that context's values.
- **Size.** Bound what you read. The data and the images of one call share one allowance of loaded bytes (64 MiB by default, see [limits.md](limits.md)), charged as `Load` returns them; one image is at most 32 MiB.
- **Re-entrancy.** A Loader method may call the Converter's `Close` (the call it serves then fails with `context.Canceled`) or start another call on it.
- **Closing.** If your Loader implements `io.Closer`, `Converter.Close` closes it. Don't share such a Loader (`FileLoader`, `FallbackLoader`) between Converters: the first to close breaks it for the others.
