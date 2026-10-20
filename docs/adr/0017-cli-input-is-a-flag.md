# 0017: The CLI's input is a flag, and nothing is read implicitly from a terminal

**Status:** accepted, 2026-10-18.

## Context

`aster svg`, `png`, `pdf` and `compile` take their input with `-i FILE`, and read standard input when `-i` is `-` or left out. Go's `flag` package stops at the first argument that is not a flag and leaves the rest unparsed, and the commands never looked at what it left. So the most natural first command, `aster svg chart.vl.json -o chart.svg`, ignored both the file and `-o`: on a terminal it waited for input with no prompt; in a script it read an empty input and failed with a hint (`pass -from vega or -from vega-lite`) that could not help. `aster svg -i chart.vl.json chart.svg` exited 0, wrote the SVG to stdout and never created `chart.svg` (audit 2026-10-18, C49, C59). Errors of the flag package were printed by it, unprefixed and followed by the whole usage, unlike every other message (C145).

## Decision

1. An argument that is not a flag is a usage error (exit 2) naming it. There is no positional input: one way to name the input, and no ambiguity between an input and an output.
2. Without `-i`, standard input is read only when it is a pipe or a file. A terminal, or another device, is a usage error asking for `-i FILE` or `-i -`; `-i -` reads it whatever it is.
3. Every usage error, the flag package's included, is one line on stderr prefixed `aster:`, exit 2. `-h` prints the usage.
4. The `-from` hint is given only for an input whose language cannot be told (ADR 0008), not for one that is not JSON.

## Why

A mistake on the command line must fail where it is made, not as a hang, a misleading parse error, or an output in the wrong place with exit status 0. Accepting one positional input instead would have fixed the first command but not the second (`-i a b`), and two ways to name the input is one more thing to document. A pipe (`cat spec | aster svg`) and a redirected file (`aster svg < spec`) keep working as before.

## Consequences

Scripts that passed a stray argument, which was ignored, now fail with exit status 2. `aster svg < /dev/null` is a usage error (`/dev/null` is a device); `aster svg -i - < /dev/null` reads it, an empty input. `docs/cli.md` documents the input and the exit statuses; `cmd/aster`'s `TestInputModel` holds them.
