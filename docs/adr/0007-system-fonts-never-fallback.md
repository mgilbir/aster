# 0007: System fonts only by name, never as a fallback by default

**Status:** accepted.

## Decision

With `WithSystemFonts()`, an installed font is used only when a spec names its family. It is never used for characters the spec's fonts lack: the fallback chain is registered and embedded fonts only.

## Why

- **Licences:** some system fonts may not be drawn into documents for distribution (Apple Color Emoji's licence does not allow it). Using one as a silent fallback would put it into output nobody chose it for.
- **Reproducibility:** output must not change with the machine it renders on.
- **Attack surface:** what a spec can make aster read stays limited to what the host enabled and the spec named.

## Consequences

Characters missing from the configured fonts draw from Noto Emoji or as `.notdef`, even when the machine has a font for them. Register such fonts with `WithFont`, or name the system family in `WithFontFallback`: that explicit choice, and its licence, are the caller's ([0009](0009-caller-chooses-fonts.md)).
