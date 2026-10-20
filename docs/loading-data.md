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

Inline data (`"values"`) and embedded PNG, JPEG and GIF images (`data:image/png;base64,…`) need no loader. Another `data:` URL is the Loader's to allow. A data set's goes through `Sanitize` and `Load` like any URL. An image's goes through `Sanitize` only: a `data:` URL it returns is decoded in place, as upstream's renderer hands it to the browser, without `Load`, within the bounds of one image and not charged to the loaded-bytes allowance (it is part of the input, as an embedded image is).

## The loaders

| Loader | Accepts | Notes |
|---|---|---|
| `DenyLoader{}` | nothing | the default; `WithLoader(nil)` is the same |
| `HTTPLoader` / `NewHTTPLoader(client)` | absolute `http`/`https` URLs, and relative ones when `BaseURL` is set (an absolute one, to fetch them) | public addresses only unless `AllowPrivateNetworks`; see below |
| `FileLoader{BaseDir}` / `NewFileLoader(dir)` | relative paths inside `BaseDir` | `NewFileLoader` opens the directory at once (error if missing); the zero-value struct opens it on first use |
| `&StaticLoader{Value}` | every URI | returns `Value` as JSON whatever the URI; for tests |
| `FallbackLoader` / `NewFallbackLoader(...)` | what any child accepts | tries children in order; the first whose `Sanitize` accepts *and* whose `Load` succeeds answers. Its `Sanitize` returns what the first accepting child made of the URI (an `HTTPLoader`'s `BaseURL` put before it), and its `Load` routes that through the children again, so each child's `Sanitize` must be idempotent (see below). A nil child (the `*FileLoader` of a `NewFileLoader` whose error was dropped) makes `New` fail, at any depth of nesting |

### HTTPLoader fields

| Field | Default | Meaning |
|---|---|---|
| `Client *http.Client` | a client of the package's own (bounded connect, TLS and header waits; honours `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`) | `http.DefaultClient` selects the same |
| `AllowedDomains []string` | empty: every public host | exact host names, case-insensitive, one trailing dot ignored, ASCII only (write IDNs in punycode) |
| `BaseURL string` | empty: only absolute URLs | put before a relative URI as vega-loader's `baseURL` is: a string join, with a `/` between the two when neither has one, nothing resolved (`https://h/data` + `cars.json` is `https://h/data/cars.json`, and `/x` after `https://h/data/` is `https://h/data//x`). It must be an absolute `http`/`https` URL for the result to be fetched; a relative one (`/docs/`) still gives links |
| `MaxResponseBytes int64` | 0 = 64 MiB | body cap after decompression; negative = none. Over the cap: `ErrLimit` |
| `Timeout time.Duration` | 0 = 60 s | one request, redirects and body included; negative = none |
| `AllowPrivateNetworks bool` | false | let requests reach loopback, link-local, private, CGNAT, multicast and unspecified addresses |
| `BlockPrivateNetworks bool` | — | **deprecated, ignored**: private networks are blocked unless `AllowPrivateNetworks` |

#### The string, and the URL that is fetched

`Sanitize` returns exactly the string vega-loader's `sanitize` makes of the URI with `BaseURL` (`baseURL`), which is what Vega writes into a link or an image `href`, and then hands to `Load`: it passes the allow-list (tested on the URI with white space removed, so `java\tscript:` is refused), puts `BaseURL` before a relative URI, drops a `file://` prefix, and gives `//host` the protocol `http`. Nothing else changes: no case folding, no percent-encoding of a space or a bracket, no dot-segment removal, no parse. Without a `BaseURL` a relative URI comes back as it is (upstream writes the link); `mailto:`, `tel:` and the like come back too, as links.

`Load` never fetches that string as net/url would read it. It requests the URL node's `fetch` makes of it, the URL Standard's parse, and `Load` and `Sanitize` check the access policy on that URL, parsed once and the same for both:

- a backslash is a slash, and tabs and newlines are dropped (`http://allowed.com\@evil.com/` is a request to `allowed.com`, path `/@evil.com/`); leading and trailing control characters and spaces go;
- the scheme and host are lower-cased, `http:host/x` and `http:///host/x` are `http://host/x`, an empty or default port is dropped, and a port over 65535 is refused;
- `.`, `..` and their `%2e` spellings are resolved in the path (`/a/%2e%2e/x` is `/x`), and a space, `"`, `<`, `>`, `` ` ``, `{`, `}`, `^` or a non-ASCII character in the path is percent-encoded; the fragment is not sent;
- a host is percent-decoded before it is checked (`allowed.com%2eevil.com` is `allowed.com.evil.com`), and one with a forbidden code point (`%00`, a space, `<`, `|`, `%25`…) is refused;
- an IPv4 host in any spelling is the dotted address: `0x7f.1`, `2130706433` and `017700000001` are `127.0.0.1`, so the private-network check sees the address that is connected to; an IPv6 literal is serialized as the standard does, and one with a zone id is refused;
- credentials are refused (as `fetch` refuses them), also when written to hide as part of the host (`allowed.com@evil.com`); an empty `http://@host/` has none.

The few places the loader does not follow `fetch`, all refusals or an equal request: a host with a character outside ASCII is refused (`fetch` maps it to punycode; write that form: `xn--bcher-kva.de`); a `%` in the path that starts no escape is sent as `%25` (net/http refuses a path with one, `fetch` sends it as it is); a host with a character net/http's URL parser does not allow (`"`, `` ` ``, `{`, `}`) is refused; and a `|` in the path is sent as `%7C`, as net/http writes it. Redirects are followed by net/http, which resolves a `Location` as Go does, and every hop is checked as the URL net/http will request.

`Load` takes what `Sanitize` returned, and refuses a string that is not an absolute `http`/`https` URL (a relative reference because the `BaseURL` is empty or relative, `mailto:`, `data:`, a path from `file://`), with the error "not an absolute http or https URL": it can be a link, and cannot be fetched. `Sanitize` returns such a string; it refuses an `http`/`https` string that `Load` would refuse (policy, userinfo, a URL `fetch` cannot parse either), so a link to a private address or a disallowed host is left out of the SVG with a warning, though upstream would write it.

What it refuses: non-HTTP schemes (`ftp:`, `file:`, `data:`, `javascript:`) when fetching, URLs with userinfo (`user:pass@host`), hosts outside `AllowedDomains`, and (by default) any non-public address. The address check is made on the addresses a name resolves to, before anything is sent to them, and again on the address a connection is actually made to, in every spelling of it (`2130706433`, `0x7f.1`, `::ffff:127.0.0.1`, NAT64, 6to4), so names that resolve inward, DNS rebinding and redirects don't get around it. The same policy is applied to every redirect hop (at most 10).

Two cases weaken the address check, and both are deliberate:

- **A proxy.** The proxy resolves the name itself, so a proxied request gets only the URL checks (address literals, `localhost` names, `AllowedDomains`). Deny internal destinations at the proxy, or use `AllowedDomains`.
- **A custom `Client`** whose `Transport` is not an `*http.Transport`. It chooses where it connects, so only address literals and `localhost` are refused. Combine it with `AllowedDomains`.

An `*http.Transport` of your own is checked on every path it dials, and nothing reaches a private address of a name before the check:

- When it sets no plain dial function, the loader's guarded dialer makes plain connections (not net/http's own): it checks each address a name resolves to before its socket connects.
- Its `DialContext` (and the deprecated `Dial`) is called with the public addresses the loader resolved the name to, one literal `ip:port` at a time, never with the name: it connects where the check looked. The request's `Host` and the TLS server name (net/http makes the handshake over a plain connection) are still the name. A dial function that routes by name sees addresses instead; set `AllowPrivateNetworks` to have it called with names, unchecked.
- Its `DialTLSContext` (and `DialTLS`) makes the handshake itself, with the server name it reads from the address, so it is called with the name, once the loader's resolution of it found a public address. The connection it makes is checked again: a name that rebinds to a private address between the two lookups is refused, but only after the connection and its ClientHello.
- A connection to a proxy (`Proxy`) is made as the transport says: the operator chose it.

A refused connection's error says the host is private, not which address its name resolved to, and any other failure to connect to a name (it does not resolve, nothing answers, the port is closed) is the same error, "could not connect to the host": the error reaches whoever wrote the URL, and must not tell an open port of the internal network from a closed one. A failure for an address literal (`https://203.0.113.7/`) keeps its detail, which the URL said already.

HTTPLoaders built around the same `*http.Transport` share one checked copy of it, and so its connection pool: a loader made per request around a shared Transport does not keep connections of its own. The copy is dropped once the Transport is garbage, and closes a connection left idle for 90 s when the Transport sets no `IdleConnTimeout` (net/http keeps one forever otherwise). With a nil `Client`, every HTTPLoader shares the package's own transport.

### FileLoader fields

`BaseDir string` is the directory. `MaxBytes int64` caps one file: 0 means 64 MiB, negative means none, and a larger file is `ErrLimit`. It refuses absolute paths, `..`, control characters, URIs with a scheme, symlinks that escape `BaseDir`, and anything that is not a regular file. The URI is a path, so nothing is percent-decoded, and `22:48.csv` is an ordinary name. `Sanitize` returns the path as it was written (`./data/../a.csv`), as vega-loader does with no `baseURL`, so that is the link or image `href` in the SVG; `Load` cleans it and opens that.

## What goes through the Loader

| Resource | Methods called | When |
|---|---|---|
| a data set's `url` | `Sanitize`, then `Load` | during the render, once per data set |
| an image mark's `url` | `Sanitize`; `Load` of what it returned when the image is drawn (PNG, PDF) or its size is needed (no `width`/`height`), unless it returned a `data:` URL, which is decoded in place | each distinct URL once per call, shared by the SVG, PNG and PDF stages: PNG and PDF draw what the SVG stage resolved, and do not sanitize the href it wrote again |
| an `<image href>` in SVG given to `SVGToPNG`/`SVGToPDF`, and an `feImage`'s `href` (`SVGToPNG`) that is not a `data:` URI or a `#id` | `Sanitize`, then `Load` | once per distinct href |
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
- **Bytes.** Data is read as UTF-8 text (JSON, CSV, TSV, DSV, TopoJSON), without the one byte order mark (`EF BB BF`) it may start with, as upstream reads a URL (`Response.text()` drops it; vega-loader's file reader keeps it, and then fails to parse it; aster reads every body as the URL path does, whatever the Loader). Images are binary (PNG, JPEG, GIF), so return them unchanged.
- **What the engine calls.** For each resource, `Sanitize` once per call with the URL the specification names, then `Load` with exactly the string `Sanitize` returned, never `Sanitize` of that answer. So a `Sanitize` that rewrites (logical names to addresses, a proxy) need not accept its own output. A `FallbackLoader`'s children are the exception: it routes what a child returned through the children again, so a child of yours should return its own answer unchanged when given it (`Sanitize(Sanitize(u)) == Sanitize(u)`), as the loaders of this package do (`HTTPLoader` with a relative `BaseURL` is the exception: vega-loader's join puts the base before its own answer again, and what it returns could not be fetched anyway).
- **Errors.** An error from `Sanitize` or `Load` becomes the `Err` of the `*aster.LoadError` the caller sees, whose `URL` is the specification's, whatever `Sanitize` made of it; each failing resource is reported once per call. An error wrapping `aster.ErrLimit` is a limit: it fails the call even under an error handler, and a `FallbackLoader` does not try the next child.
- **Cancellation.** Honour `ctx`, which carries the call's deadline, the cancellation of `Close` and of the caller's `WithContext` context, and that context's values.
- **Size.** Bound what you read. The data and the images of one call share one allowance of loaded bytes (64 MiB by default, see [limits.md](limits.md)); one image is at most 32 MiB. The images PNG and PDF output fetch run eight at a time, and `HTTPLoader` and `FileLoader` charge the shared allowance as they read, so together they stop where it ends. That bound while reading holds for the package's loaders, and for a Loader of yours that delegates to them with the context it was given; what a Loader of your own returns otherwise is charged as soon as its `Load` returns, before it is decoded, so it may read past the allowance first. Bytes read are charged even when the load then fails, so a call fails with `ErrLimit` only when what its loads read adds up to more than the allowance, whatever order they ran in.
- **Panics.** A panic in `Sanitize`, `Load` or `Close` is recovered, on whatever goroutine the engine called it, and is that method's error: the resource is not loaded (a `*LoadError`, which an error handler may let go), or `Converter.Close` returns it. The Converter still closes.
- **Re-entrancy.** A Loader method may call the Converter's `Close` (the call it serves then fails with `context.Canceled`) or start another call on it.
- **Closing.** If your Loader implements `io.Closer`, `Converter.Close` closes it (once, even if `Close` panics, and it may call `Converter.Close` itself). Don't share such a Loader (`FileLoader`, `FallbackLoader`) between Converters: the first to close breaks it for the others. A `FileLoader` that was never used is closed without opening its directory.
