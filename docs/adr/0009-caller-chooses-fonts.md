# 0009: Registered families are the caller's, and so is the fallback chain

**Status:** accepted, 2026-10-11.

## Context

The documentation promised that registered fonts take priority over embedded ones, and that later registrations beat earlier ones. The code did the reverse. Embedded faces were listed first in every family, so `WithFont("Liberation Sans", …)` was ignored, and of equal faces the first registered won. The order of fallback for missing characters was fixed. A caller could not replace an embedded family, nor choose which font draws emoji or another script.

## Decision

- **A registered family is made of the faces registered for it**, as a CSS `@font-face` family is. Embedded and system faces of the same name are no longer that family, so registering a name replaces the embedded font (or a system one) of that name. Weight and style choose among the faces. Of faces that fit equally, the last registered wins, as the last of equal `@font-face` rules does.
- **`WithFontFallback(families...)` sets the fallback chain** for characters the requested and default families lack: the listed families in order, each one's best fit first. No families means no fallback. Unset, the chain stays as it was: registered fonts newest first, then the embedded ones.
- **A system family listed there is a fallback.** That is the explicit opt-in [0007](0007-system-fonts-never-fallback.md) asks for.

## Why

The caller knows which fonts its output must use (a corporate font, a licensed CJK font, a colour emoji font). Matching CSS's rules makes the behaviour predictable to anyone who has written `@font-face`.

## Consequences

- A behaviour change for callers who registered a font under an embedded name (now used, before ignored), or several equal faces under one name (now the last, before the first).
- The upstream comparisons are unaffected: they register DejaVu under its own names, and their reports are byte-identical before and after.
- `TestCallerControlsFonts` pins both rules.
