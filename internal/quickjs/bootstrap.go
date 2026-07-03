package quickjs

import "strings"

// bootstrapScript builds the global script that installs the __aster_*
// bridge functions for the configured callbacks. JS reaches Go by reading
// synthetic files under /aster (see bridgeFS); each argument travels as a
// path segment, with long arguments uploaded in chunks first ("stash").
func bootstrapScript(b Bridge) string {
	var regs []string
	if b.MeasureText != nil {
		regs = append(regs, `
	{
		const cache = new Map();
		globalThis.__aster_measure_text = (text, font) => {
			const key = text + "\u0000" + font;
			let w = cache.get(key);
			if (w === undefined) {
				w = parseFloat(__asterCall("measure", [text, font]));
				if (cache.size > 20000) cache.clear();
				cache.set(key, w);
			}
			return w;
		};
	}`)
	}
	if b.Load != nil {
		regs = append(regs, `
	globalThis.__aster_load = async (url) => __asterCall("load", [url]);`)
	}
	if b.Sanitize != nil {
		regs = append(regs, `
	globalThis.__aster_sanitize = (uri) => __asterCall("sanitize", [uri]);`)
	}
	if len(regs) == 0 {
		return ""
	}

	return `(() => {
	const std = globalThis.std;
	if (!std || typeof std.loadFile !== "function") {
		throw new Error("aster: qjs std module unavailable");
	}
	// Keep encoded segments well under the guest's path budget.
	const MAXSEG = 700;
	let nextSlot = 0;
	function encArg(s) {
		s = String(s);
		if (s.length <= MAXSEG) return "_" + encodeURIComponent(s);
		const slot = nextSlot++;
		for (let i = 0; i < s.length; i += MAXSEG) {
			const resp = std.loadFile(
				"/aster/stash/" + slot + "/_" + encodeURIComponent(s.slice(i, i + MAXSEG)));
			if (resp === null || resp[0] !== "O") {
				throw new Error("aster bridge: stash upload failed");
			}
		}
		return "@" + slot;
	}
	function __asterCall(kind, args) {
		const path = "/aster/" + kind + "/" + args.map(encArg).join("/");
		nextSlot = 0;
		const resp = std.loadFile(path);
		if (resp === null) throw new Error("aster bridge: " + kind + " call failed");
		if (resp[0] === "E") throw new Error(resp.slice(1));
		return resp.slice(1);
	}
` + strings.Join(regs, "\n") + `
})();`
}
