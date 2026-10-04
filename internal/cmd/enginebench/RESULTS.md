# Engine comparison

## 2026-10-03: the pure-Go engine after the performance work

Produced by `NODE="volta run --node 24 node" run.sh` on the same Apple M1 Pro as the run below, at 819a348 (after #48–#57, which cut the PDF stage, the PNG encoder and rasterizer, and the dataflow). Raw JSON in `results-2026-10-03/`. The machine had about one and a half cores of background load (a Docker VM, Spotlight, a browser), which falls on node and the engine alike; node 24 measured about 8% slower than on 2026-09-30, so compare the engine with its own earlier times (SVG geomean 2.40 → 1.92 ms, PNG 7.89 → 7.17 ms) as well as with node.

### Engines

| engine | version | environment | init | first VL→SVG | peak RSS | failed cases |
|---|---|---|---:|---:|---:|---:|
| node 24 | Vega 6.4.0 / Vega-Lite 6.4.3, node-canvas 3.2.3 | node v24.21.0 darwin/arm64, Apple M1 Pro | 390 ms | 65.2 ms | 1025 MB | 3 |
| aster | Vega 6.4.0 / Vega-Lite 6.4.3, pure Go | go1.27.1 darwin/arm64, GOMAXPROCS=10 | 0.00 ms | 9.42 ms | 270 MB | 1 |

Times are the median of repeated runs on a warm engine (budget 300 ms per case, at least 3 runs unless one run exceeds 2 s). Only cases every engine completed are compared.

### vl-examples, Vega-Lite → Vega JSON (627 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 24 (geomean) | faster than node 24 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 24 | 0.52 ms | 0.42 ms | 1.22 ms | 9.52 ms | 425 ms | — | — |
| aster | 0.30 ms | 0.25 ms | 0.65 ms | 3.11 ms | 238 ms | 1.72× | 627/627 |

### vl-examples, spec → SVG (627 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 24 (geomean) | faster than node 24 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 24 | 6.62 ms | 6.21 ms | 20.9 ms | 420 ms | 7472 ms | — | — |
| aster | 1.92 ms | 1.62 ms | 7.49 ms | 129 ms | 2816 ms | 3.45× | 614/627 |

### vl-examples, spec → PNG (626 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 24 (geomean) | faster than node 24 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 24 | 14.8 ms | 13.2 ms | 42.1 ms | 398 ms | 14.7 s | — | — |
| aster | 7.17 ms | 7.01 ms | 21.8 ms | 207 ms | 7602 ms | 2.06× | 616/626 |

### vg-gallery, spec → SVG (92 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 24 (geomean) | faster than node 24 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 24 | 7.95 ms | 6.70 ms | 65.4 ms | 417 ms | 2726 ms | — | — |
| aster | 2.07 ms | 1.34 ms | 26.8 ms | 395 ms | 1103 ms | 3.84× | 90/92 |

### vg-gallery, spec → PNG (90 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 24 (geomean) | faster than node 24 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 24 | 30.2 ms | 32.1 ms | 123 ms | 849 ms | 5492 ms | — | — |
| aster | 14.0 ms | 13.5 ms | 66.0 ms | 410 ms | 2607 ms | 2.16× | 87/90 |

### Highlights (end-to-end SVG and PNG)

| spec | stage | node 24 | aster |
|---|---|---:|---:|
| vl-examples/bar | svg | 5.77 ms | 0.61 ms |
| vl-examples/bar | png | 11.9 ms | 2.89 ms |
| vl-examples/trellis_bar | svg | 7.74 ms | 1.61 ms |
| vl-examples/trellis_bar | png | 23.3 ms | 12.0 ms |
| vl-examples/repeat_splom | svg | 224 ms | 18.1 ms |
| vl-examples/repeat_splom | png | 365 ms | 80.7 ms |
| vl-examples/geo_choropleth | svg | 193 ms | 73.3 ms |
| vl-examples/geo_choropleth | png | 202 ms | 90.6 ms |
| vg-gallery/scatter-plot | svg | 8.99 ms | 1.18 ms |
| vg-gallery/scatter-plot | png | 55.2 ms | 8.40 ms |
| vg-gallery/stacked-area-chart | svg | 4.12 ms | 0.28 ms |
| vg-gallery/stacked-area-chart | png | 18.2 ms | 4.41 ms |
| vg-gallery/treemap | svg | 2.11 ms | 0.53 ms |
| vg-gallery/treemap | png | 53.3 ms | 22.2 ms |
| vg-gallery/county-unemployment | svg | 203 ms | 67.2 ms |
| vg-gallery/county-unemployment | png | 849 ms | 101 ms |
| vg-gallery/force-directed-layout | svg | 47.0 ms | 18.1 ms |
| vg-gallery/force-directed-layout | png | 65.5 ms | 30.1 ms |
| vg-gallery/beeswarm-plot | svg | 14.5 ms | 6.62 ms |
| vg-gallery/beeswarm-plot | png | 21.0 ms | 11.2 ms |
| vg-gallery/contour-plot | svg | 12.9 ms | 6.18 ms |
| vg-gallery/contour-plot | png | 45.5 ms | 43.3 ms |
| vg-gallery/world-map | svg | 44.2 ms | 8.02 ms |
| vg-gallery/world-map | png | 216 ms | 34.0 ms |

### Slowest svg cases (by the slowest engine)

| spec | node 24 | aster |
|---|---:|---:|
| vl-examples/geo_circle | 420 ms | 110 ms |
| vg-gallery/platformer | 417 ms | 25.4 ms |
| vg-gallery/word-cloud | 399 ms | 395 ms |
| vl-examples/interactive_geo_facet_species | 281 ms | 118 ms |
| vl-examples/geo_trellis | 263 ms | 129 ms |
| vl-examples/repeat_splom | 224 ms | 18.1 ms |
| vg-gallery/county-unemployment | 203 ms | 67.2 ms |
| vl-examples/geo_choropleth | 193 ms | 73.3 ms |
| vg-gallery/time-units | 175 ms | 28.1 ms |
| vg-gallery/map-with-tooltip | 119 ms | 48.4 ms |

### Slowest png cases (by the slowest engine)

| spec | node 24 | aster |
|---|---:|---:|
| vg-gallery/county-unemployment | 849 ms | 101 ms |
| vg-gallery/word-cloud | 341 ms | 410 ms |
| vl-examples/geo_circle | 398 ms | 184 ms |
| vl-examples/bar_count_minimap | 366 ms | 117 ms |
| vl-examples/repeat_splom | 365 ms | 80.7 ms |
| vl-examples/geo_trellis | 352 ms | 207 ms |
| vl-examples/interactive_geo_facet_species | 330 ms | 162 ms |
| vg-gallery/wind-vectors | 279 ms | 41.2 ms |
| vg-gallery/world-map | 216 ms | 34.0 ms |
| vg-gallery/tree-layout | 208 ms | 51.9 ms |

### Failures

- **node 24**: 3
  - vl-examples/scatter_image png: Image given has not completed loading
  - vg-gallery/platformer png: Image given has not completed loading
  - vg-gallery/projections png: the surface type is not appropriate for the operation
- **aster**: 1
  - vg-gallery/projections png: aster: rendering PNG: raster: SVG has no valid size (width/height or viewBox required)

## 2026-09-30: the run that retired the QuickJS engine

Produced by `run.sh` (node 20 via the default node, then node 24 with `volta run --node 24 node internal/cmd/enginebench/bench.mjs`), on an Apple M1 Pro, before the QuickJS engine was retired. aster is the QuickJS + resvg engine; purego is the pure-Go engine that replaced it.

### Engines

| engine | version | environment | init | first VL→SVG | peak RSS | failed cases |
|---|---|---|---:|---:|---:|---:|
| node 20 | Vega 6.4.0 / Vega-Lite 6.4.3, node-canvas 3.2.3 | node v20.19.5 darwin/arm64, Apple M1 Pro | 296 ms | 69.8 ms | 2802 MB | 2 |
| node 24 | Vega 6.4.0 / Vega-Lite 6.4.3, node-canvas 3.2.3 | node v24.21.0 darwin/arm64, Apple M1 Pro | 391 ms | 63.7 ms | 2113 MB | 2 |
| aster | Vega 6.4.0 / Vega-Lite 6.4.3, QuickJS + resvg (WASM, andsifr) | go1.27.1 darwin/arm64, GOMAXPROCS=10 | 516 ms | 37.9 ms | 2074 MB | 13 |
| purego | Vega 6.4.0 / Vega-Lite 6.4.3, pure Go | go1.27.1 darwin/arm64, GOMAXPROCS=10 | 0.00 ms | 7.22 ms | 306 MB | 10 |

Times are the median of repeated runs on a warm engine (budget 300 ms per case, at least 3 runs unless one run exceeds 2 s). Only cases every engine completed are compared.

### vl-examples, Vega-Lite → Vega JSON (626 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 20 (geomean) | faster than node 20 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 20 | 0.80 ms | 0.60 ms | 3.09 ms | 31.2 ms | 767 ms | — | — |
| node 24 | 0.50 ms | 0.41 ms | 1.11 ms | 6.21 ms | 392 ms | 1.61× | 619/626 |
| aster | 7.34 ms | 5.99 ms | 17.0 ms | 87.9 ms | 5845 ms | 0.11× | 1/626 |
| purego | 0.36 ms | 0.30 ms | 0.74 ms | 3.88 ms | 280 ms | 2.26× | 626/626 |

### vl-examples, spec → SVG (626 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 20 (geomean) | faster than node 20 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 20 | 8.28 ms | 6.79 ms | 37.0 ms | 917 ms | 12.5 s | — | — |
| node 24 | 6.03 ms | 5.36 ms | 18.9 ms | 429 ms | 7059 ms | 1.37× | 509/626 |
| aster | 101 ms | 84.6 ms | 308 ms | 9846 ms | 138.8 s | 0.08× | 3/626 |
| purego | 2.40 ms | 2.05 ms | 10.3 ms | 174 ms | 3517 ms | 3.45× | 618/626 |

### vl-examples, spec → PNG (625 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 20 (geomean) | faster than node 20 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 20 | 17.1 ms | 14.6 ms | 62.1 ms | 908 ms | 20.4 s | — | — |
| node 24 | 13.7 ms | 12.4 ms | 40.8 ms | 505 ms | 13.8 s | 1.25× | 399/625 |
| aster | 118 ms | 95.1 ms | 357 ms | 11.7 s | 165.9 s | 0.14× | 3/625 |
| purego | 7.89 ms | 7.31 ms | 24.2 ms | 239 ms | 8485 ms | 2.17× | 616/625 |

### vg-gallery, spec → SVG (87 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 20 (geomean) | faster than node 20 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 20 | 5.22 ms | 4.12 ms | 47.2 ms | 168 ms | 1429 ms | — | — |
| node 24 | 5.58 ms | 4.13 ms | 49.2 ms | 140 ms | 1543 ms | 0.94× | 27/87 |
| aster | 132 ms | 87.8 ms | 1431 ms | 10.4 s | 52.7 s | 0.04× | 0/87 |
| purego | 2.15 ms | 1.68 ms | 26.0 ms | 66.3 ms | 680 ms | 2.43× | 85/87 |

### vg-gallery, spec → PNG (87 specs)

| engine | geomean | median | p90 | max | total | speedup vs node 20 (geomean) | faster than node 20 |
|---|---:|---:|---:|---:|---:|---:|---:|
| node 20 | 20.9 ms | 19.3 ms | 86.8 ms | 195 ms | 3230 ms | — | — |
| node 24 | 23.2 ms | 20.0 ms | 99.1 ms | 188 ms | 3593 ms | 0.90× | 11/87 |
| aster | 177 ms | 141 ms | 1793 ms | 8283 ms | 56.9 s | 0.12× | 0/87 |
| purego | 13.9 ms | 13.6 ms | 60.9 ms | 123 ms | 2121 ms | 1.50× | 84/87 |

### Highlights (end-to-end SVG and PNG)

| spec | stage | node 20 | node 24 | aster | purego |
|---|---|---:|---:|---:|---:|
| vl-examples/bar | svg | 2.89 ms | 3.48 ms | 38.3 ms | 0.76 ms |
| vl-examples/bar | png | 6.61 ms | 5.59 ms | 43.1 ms | 3.11 ms |
| vl-examples/trellis_bar | svg | 6.54 ms | 5.98 ms | 91.1 ms | 2.00 ms |
| vl-examples/trellis_bar | png | 20.9 ms | 24.6 ms | 156 ms | 13.8 ms |
| vl-examples/repeat_splom | svg | 87.1 ms | 116 ms | 1683 ms | 27.2 ms |
| vl-examples/repeat_splom | png | 155 ms | 241 ms | 2139 ms | 99.6 ms |
| vl-examples/geo_choropleth | svg | 481 ms | 218 ms | 6697 ms | 85.3 ms |
| vl-examples/geo_choropleth | png | 592 ms | 257 ms | 6828 ms | 101 ms |
| vg-gallery/scatter-plot | svg | 5.12 ms | 4.84 ms | 96.1 ms | 1.54 ms |
| vg-gallery/scatter-plot | png | 17.8 ms | 18.6 ms | 162 ms | 10.1 ms |
| vg-gallery/stacked-area-chart | svg | 1.20 ms | 1.25 ms | 25.1 ms | 0.33 ms |
| vg-gallery/stacked-area-chart | png | 7.14 ms | 7.80 ms | 31.2 ms | 4.78 ms |
| vg-gallery/treemap | svg | 1.64 ms | 1.47 ms | 30.9 ms | 0.69 ms |
| vg-gallery/treemap | png | 25.2 ms | 24.5 ms | 72.2 ms | 23.6 ms |
| vg-gallery/county-unemployment | svg | 141 ms | 140 ms | 10.4 s | 66.3 ms |
| vg-gallery/county-unemployment | png | 191 ms | 187 ms | 8283 ms | 100.0 ms |
| vg-gallery/force-directed-layout | svg | 53.3 ms | 90.9 ms | 4216 ms | 18.0 ms |
| vg-gallery/force-directed-layout | png | 70.1 ms | 95.6 ms | 4195 ms | 32.2 ms |
| vg-gallery/beeswarm-plot | svg | 17.7 ms | 14.7 ms | 1543 ms | 6.51 ms |
| vg-gallery/beeswarm-plot | png | 24.9 ms | 21.9 ms | 1741 ms | 11.1 ms |
| vg-gallery/contour-plot | svg | 13.1 ms | 13.6 ms | error | error |
| vg-gallery/contour-plot | png | 43.6 ms | 45.5 ms | error | error |
| vg-gallery/world-map | svg | 17.4 ms | 25.8 ms | 417 ms | 9.22 ms |
| vg-gallery/world-map | png | 48.2 ms | 76.4 ms | 486 ms | 37.5 ms |

### Slowest svg cases (by the slowest engine)

| spec | node 20 | node 24 | aster | purego |
|---|---:|---:|---:|---:|
| vg-gallery/county-unemployment | 141 ms | 140 ms | 10.4 s | 66.3 ms |
| vl-examples/geo_circle | 917 ms | 418 ms | 9846 ms | 174 ms |
| vl-examples/interactive_geo_facet_species | 499 ms | 296 ms | 8267 ms | 149 ms |
| vl-examples/geo_trellis | 699 ms | 429 ms | 8024 ms | 131 ms |
| vl-examples/geo_choropleth | 481 ms | 218 ms | 6697 ms | 85.3 ms |
| vl-examples/layer_point_line_loess | 42.8 ms | 30.4 ms | 4773 ms | 21.5 ms |
| vg-gallery/loess-regression | 29.3 ms | 22.2 ms | 4518 ms | 16.8 ms |
| vg-gallery/annual-precipitation | 70.5 ms | 79.3 ms | 4433 ms | 37.5 ms |
| vg-gallery/map-with-tooltip | 104 ms | 114 ms | 4293 ms | 56.2 ms |
| vg-gallery/force-directed-layout | 53.3 ms | 90.9 ms | 4216 ms | 18.0 ms |

### Slowest png cases (by the slowest engine)

| spec | node 20 | node 24 | aster | purego |
|---|---:|---:|---:|---:|
| vl-examples/interactive_geo_facet_species | 540 ms | 450 ms | 11.7 s | 192 ms |
| vl-examples/geo_circle | 790 ms | 361 ms | 11.0 s | 239 ms |
| vg-gallery/county-unemployment | 191 ms | 187 ms | 8283 ms | 100.0 ms |
| vl-examples/geo_trellis | 908 ms | 505 ms | 8125 ms | 211 ms |
| vl-examples/geo_choropleth | 592 ms | 257 ms | 6828 ms | 101 ms |
| vl-examples/layer_point_line_loess | 54.3 ms | 42.6 ms | 4861 ms | 33.2 ms |
| vg-gallery/loess-regression | 46.7 ms | 41.5 ms | 4829 ms | 27.5 ms |
| vg-gallery/map-with-tooltip | 144 ms | 148 ms | 4559 ms | 86.4 ms |
| vg-gallery/annual-precipitation | 100 ms | 100 ms | 4313 ms | 63.7 ms |
| vl-examples/parallel_coordinate | 88.2 ms | 91.4 ms | 4197 ms | 54.5 ms |

### Failures

- **node 20**: 2
  - vl-examples/scatter_image png: Image given has not completed loading
  - vg-gallery/projections png: the surface type is not appropriate for the operation
- **node 24**: 2
  - vl-examples/scatter_image png: Image given has not completed loading
  - vg-gallery/projections png: the surface type is not appropriate for the operation
- **aster**: 13
  - vl-examples/facet_independent_scale_layer_broken vl2vg: aster/runtime: eval: TypeError: not a function
  - vl-examples/facet_independent_scale_layer_broken svg: aster/runtime: eval: TypeError: not a function
  - vl-examples/facet_independent_scale_layer_broken png: aster/runtime: eval: TypeError: not a function
  - vg-gallery/contour-plot svg: aster/runtime: eval: TypeError: not a function
  - vg-gallery/contour-plot png: aster/runtime: eval: TypeError: not a function
  - vg-gallery/density-heatmaps svg: aster/runtime: eval: TypeError: not a function
  - vg-gallery/density-heatmaps png: aster/runtime: eval: TypeError: not a function
  - vg-gallery/labeled-scatter-plot svg: aster/runtime: eval: TypeError: not a function
  - … 5 more
- **purego**: 10
  - vg-gallery/contour-plot svg: purego: rendering Vega: the heatmap transform needs a canvas and is not supported
  - vg-gallery/contour-plot png: purego: rendering Vega: the heatmap transform needs a canvas and is not supported
  - vg-gallery/density-heatmaps svg: purego: rendering Vega: the heatmap transform needs a canvas and is not supported
  - vg-gallery/density-heatmaps png: purego: rendering Vega: the heatmap transform needs a canvas and is not supported
  - vg-gallery/labeled-scatter-plot svg: purego: rendering Vega: the label transform needs a canvas and is not supported
  - vg-gallery/labeled-scatter-plot png: purego: rendering Vega: the label transform needs a canvas and is not supported
  - vg-gallery/projections svg: purego: rendering Vega: unrecognized projection type: airy
  - vg-gallery/projections png: purego: rendering Vega: unrecognized projection type: airy
  - … 2 more
