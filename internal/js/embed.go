// Package js embeds the vendored Vega/Vega-Lite modules and the bridge script.
package js

import "embed"

//go:embed modules/*.js modules/manifest.json
var Modules embed.FS

//go:embed bridge.js
var BridgeJS string
