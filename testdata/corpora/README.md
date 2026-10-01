# External corpora

Two corpora of other people's specifications are compared with the node
oracle (`TestCorpusWild`, `TestCorpusDeneb`). They are fetched, not
redistributed: nothing but the manifests in this directory is checked in.

`scripts/fetch-corpora.sh` downloads each repository at a pinned commit,
extracts only the files below into `testdata/corpora-cache/` (ignored by
version control, licence file included), and verifies the sha256 of every file
against `wild.sha256` / `deneb.sha256`. A mismatch fails the fetch, so a corpus
cannot drift. Changing a pin means running
`scripts/fetch-corpora.sh --update-manifest` and reviewing the manifest diff.

| corpus | source | commit | files | licence |
|---|---|---|---|---|
| wild  | https://github.com/hyungkwonko/chart-llm, `docs/data/chart/*.vl.json` | `6e3d3e2bf1c30aa6df7b916289f42a6ed7721a24` | 1981 Vega-Lite specs | MIT |
| deneb | https://github.com/avatorl/Deneb-Vega-Templates, every `*.json` | `2f4a29c1af6556ab0e58e3584a7b7dc54073bfae` | 63 templates | MIT |

The wild corpus is human-written charts collected from public repositories.
The `benchmark/` directories of the same repository are excluded on purpose:
they duplicate Vega-Lite's own examples, which are compared already.

The Deneb templates are for a Power BI visual. The generator
`testdata/oracle-node/sweeps/deneb.mjs` turns them into plain Vega that both
engines run: it synthesises the root dataset from `usermeta.dataset`,
replaces `pbiColor(n)` with literal colours, strips `//` comments and drops
the `containerSize()` update of the size signals. One template is left out:
`flow/sankey` calls `pbiPatternSVG`, which returns a generated SVG pattern, so
there is nothing honest to substitute. Templates that use the `label`
transform are included.

The claim is agreement with upstream, not success: a spec that upstream
refuses and the engine refuses too counts as agreement. The expectations are
`testdata/sweeps/wild.txt` and `testdata/sweeps/deneb.txt`.
