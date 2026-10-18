# 0021: The caller's code runs behind one boundary, and the loader vets a name before anything is sent to it

**Status:** accepted, 2026-10-18.

## Context

A call runs code the caller wrote: the Loader's `Sanitize`, `Load` and `Close`, the error handler, a `WithSignal` value's `MarshalJSON`, a custom projection's functions. Before, each place that called it made its own arrangement, and the arrangements differed (audit 2026-10-18, C15–C17):

- A Loader panic on a data load came back as `vega: internal error`, caught by the dataflow's recover. The same panic on an image that PNG or PDF output fetched killed the process, because those fetches run on goroutines of their own, which no recover reached.
- A panic in the Loader's `Close` left `Converter.Close` unable to finish: every later `Close` waited forever.
- A Loader whose `Close` called `Converter.Close`, or a projection that called it during a render, deadlocked: `Close` waited for the call it was part of.

`HTTPLoader`'s private-network check had a related split by transport shape (C18). With the package's own dialer it ran before the socket connected. With a caller's `DialContext` it ran on the connection already made, so an open port of the internal network saw a connection, and a closed one's error named the resolved address and said "connection refused": a port scanner driven by a specification.

## Decision

- **One boundary for the caller's code.** Every call into it goes through `Converter.callback`. The boundary marks the goroutine as inside a call, so a `Close` it makes returns at once instead of waiting for the call, and it recovers a panic into that code's error, which names what panicked. The owner decided (2026-10-18) that a Loader panic is an error, never a crash:
  - a Loader method's panic is the method's error: the resource is not loaded, which the error handler may let go like any failed load;
  - the error handler's and `MarshalJSON`'s panics fail the call;
  - the Loader's `Close` panic is `Converter.Close`'s error, and the lifecycle reaches Closed whatever happens (`idle` is closed in a defer).
- **A call marks its own goroutine as inside it**, once, for its whole length. The engine never calls `Close`, so a `Close` on that goroutine comes from the caller's code. This covers a custom projection's `Forward` and `Invert` without a per-point boundary, which the geographic walk could not afford. Their panics stay with the render's own recover (`recoverStage`), as before.
- **Every goroutine the library starts recovers first thing**, since no other recover reaches it. `imageref.Fetch`'s workers turn a panic into Fetch's error.
- **The loader resolves a name before any dial reaches it**, for every `*http.Transport`. A name with no public address is refused before anything is sent. A caller's `DialContext` (or `Dial`) is given the vetted public addresses as literals, never the name, so a second resolution cannot rebind. The request's Host and the TLS server name stay the name, because net/http makes the handshake over the plain connection. A caller's `DialTLSContext` makes the handshake itself and reads the server name from the address, so it is given the name and checked again on the connection it makes. Every failed dial to a name is one opaque error.

## Why

- A Loader is the caller's code, and a bug in it is the caller's bug. But a crash takes down every other call the process is serving. An error that names what panicked tells the caller as much, and costs nothing when nothing panics.
- One boundary is something a test can hold the code to. `TestPanicBoundaries` checks it on the source:
  - recover appears only in `callback` and `recoverStage`;
  - every `go` statement of the library starts with a deferred recover;
  - the raw Loader is read only by `New` and `closeLoader`.
- Resolving in the loader gives a caller's transport the guarantee its own dialer always had: nothing reaches a private address. Checking after the fact cannot give it, because the SYN, and with TLS the ClientHello, has already been sent.

## Consequences

- A dial function that routes by name, such as a service mesh's, sees addresses instead of names. A caller who needs names can set `AllowPrivateNetworks`, which uses its transport as it is, and confine it with `AllowedDomains`.
- A caller's `DialTLSContext` keeps one gap. If the name rebinds to a private address between the loader's lookup and the dial function's own, the dial function sends a SYN and a ClientHello there before the connection is checked and refused. security.md lists this residual risk.
- A failed dial to a name no longer says why: not "connection refused", "no such host" or "i/o timeout". This is the price of not telling the URL's author anything about the network. A failure for an address literal keeps its detail.
- A Loader's data-load panic used to read "vega: internal error: …" and is now a `*LoadError` that says "the Loader's Load panicked: …". The error handler may let it go, as it may any failed load.
