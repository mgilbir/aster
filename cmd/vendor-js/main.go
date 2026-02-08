// Command vendor-js downloads Vega and Vega-Lite ESM bundles from jsDelivr,
// resolves all transitive dependencies, rewrites import paths to canonical
// module names, and saves the result to internal/js/modules/.
//
// It also generates a manifest.json with versions, checksums, and a
// topological load order suitable for QuickJS module registration.
//
// Multiple Vega-Lite versions are supported. Use the -version flag to vendor
// only a single version set (e.g. -version vl5_8).
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const jsdelivrBase = "https://cdn.jsdelivr.net"

// versionSet defines a Vega-Lite version to vendor.
// The Vega version is auto-resolved from jsDelivr's transitive dependencies.
type versionSet struct {
	key             string // directory name, e.g. "vl5_8"
	vegaLiteVersion string // e.g. "5.8.0"
}

var versionSets = []versionSet{
	{key: "vl5_8", vegaLiteVersion: "5.8.0"},
	{key: "vl6_4", vegaLiteVersion: "6.4.0"},
}

// VersionIndex is written to internal/js/modules/versions.json.
// It lists all available Vega-Lite version sets and the default.
type VersionIndex struct {
	Default  string                `json:"default"`
	Versions map[string]VersionDef `json:"versions"`
}

// VersionDef describes a single vendored Vega-Lite version set.
type VersionDef struct {
	VegaVersion     string `json:"vegaVersion"`
	VegaLiteVersion string `json:"vegaLiteVersion"`
}

// Manifest is written to internal/js/modules/{key}/manifest.json.
type Manifest struct {
	VegaVersion     string           `json:"vegaVersion"`
	VegaLiteVersion string           `json:"vegaLiteVersion"`
	Modules         []ManifestModule `json:"modules"`
}

type ManifestModule struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Filename string `json:"filename"`
}

// module tracks a downloaded ESM module.
type module struct {
	name    string // canonical name, e.g. "d3-array"
	version string // e.g. "3.2.4"
	source  string // rewritten source code
	deps    []string
}

var (
	// Matches jsDelivr ESM import paths like: from"/npm/d3-array@3.2.4/+esm"
	// or: from "/npm/d3-array@3.2.4/+esm"
	importPathRe = regexp.MustCompile(`from\s*"(/npm/([^@]+)@([^/]+)/\+esm)"`)

	// Matches export-from statements: export{...}from"/npm/..."
	exportPathRe = regexp.MustCompile(`(?:export\s*\{[^}]*\}\s*from|export\s*\*\s*from)\s*"(/npm/([^@]+)@([^/]+)/\+esm)"`)
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("vendor-js: ")

	versionFlag := flag.String("version", "", "vendor only this version set key (e.g. vl5_8)")
	flag.Parse()

	sets := versionSets
	if *versionFlag != "" {
		var found bool
		for _, vs := range versionSets {
			if vs.key == *versionFlag {
				sets = []versionSet{vs}
				found = true
				break
			}
		}
		if !found {
			var keys []string
			for _, vs := range versionSets {
				keys = append(keys, vs.key)
			}
			log.Fatalf("unknown version %q, available: %s", *versionFlag, strings.Join(keys, ", "))
		}
	}

	outDir := filepath.Join("internal", "js", "modules")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("creating output dir: %v", err)
	}

	index := VersionIndex{
		Default:  "vl6_4",
		Versions: make(map[string]VersionDef),
	}

	for _, vs := range sets {
		vegaVer, err := vendorVersion(vs)
		if err != nil {
			log.Fatalf("vendoring %s: %v", vs.key, err)
		}
		index.Versions[vs.key] = VersionDef{
			VegaVersion:     vegaVer,
			VegaLiteVersion: vs.vegaLiteVersion,
		}
	}

	// Write top-level versions.json index.
	indexJSON, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		log.Fatalf("marshaling versions index: %v", err)
	}
	indexPath := filepath.Join(outDir, "versions.json")
	if err := os.WriteFile(indexPath, indexJSON, 0o644); err != nil {
		log.Fatalf("writing versions index: %v", err)
	}
	log.Printf("wrote versions index to %s", indexPath)
}

func vendorVersion(vs versionSet) (string, error) {
	outDir := filepath.Join("internal", "js", "modules", vs.key)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("creating output dir: %w", err)
	}

	modules := make(map[string]*module) // name → module

	// Seed with vega-lite; vega version is auto-resolved from its dependencies.
	type queueItem struct {
		name    string
		version string
	}
	queue := []queueItem{
		{"vega-lite", vs.vegaLiteVersion},
	}

	log.Printf("[%s] downloading Vega-Lite %s from jsDelivr...", vs.key, vs.vegaLiteVersion)

	var vegaVersion string

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if _, exists := modules[item.name]; exists {
			continue
		}

		log.Printf("  [%s] fetching %s@%s", vs.key, item.name, item.version)

		src, err := fetchESM(item.name, item.version)
		if err != nil {
			return "", fmt.Errorf("fetching %s@%s: %w", item.name, item.version, err)
		}

		mod := &module{
			name:    item.name,
			version: item.version,
		}

		// Track vega version as it's resolved.
		if item.name == "vega" {
			vegaVersion = item.version
		}

		// Find all dependencies from import and export statements.
		var deps []string
		seen := make(map[string]bool)

		for _, matches := range importPathRe.FindAllStringSubmatch(src, -1) {
			depName := matches[2]
			depVersion := matches[3]
			if !seen[depName] {
				seen[depName] = true
				deps = append(deps, depName)
				queue = append(queue, queueItem{depName, depVersion})
			}
		}

		for _, matches := range exportPathRe.FindAllStringSubmatch(src, -1) {
			depName := matches[2]
			depVersion := matches[3]
			if !seen[depName] {
				seen[depName] = true
				deps = append(deps, depName)
				queue = append(queue, queueItem{depName, depVersion})
			}
		}

		mod.deps = deps

		// Rewrite import paths from jsDelivr URLs to canonical module names.
		rewritten := importPathRe.ReplaceAllStringFunc(src, func(match string) string {
			sub := importPathRe.FindStringSubmatch(match)
			return strings.Replace(match, sub[1], sub[2], 1)
		})
		rewritten = exportPathRe.ReplaceAllStringFunc(rewritten, func(match string) string {
			sub := exportPathRe.FindStringSubmatch(match)
			return strings.Replace(match, sub[1], sub[2], 1)
		})

		mod.source = rewritten
		modules[item.name] = mod
	}

	if vegaVersion == "" {
		return "", fmt.Errorf("vega version not resolved from dependencies")
	}

	log.Printf("[%s] resolved Vega %s, downloaded %d modules, computing load order...", vs.key, vegaVersion, len(modules))

	// Topological sort for load order.
	order, err := topoSort(modules)
	if err != nil {
		return "", fmt.Errorf("topological sort: %w", err)
	}

	// Write module files and build manifest.
	manifest := Manifest{
		VegaVersion:     vegaVersion,
		VegaLiteVersion: vs.vegaLiteVersion,
		Modules:         make([]ManifestModule, 0, len(order)),
	}

	for _, name := range order {
		mod := modules[name]
		filename := name + ".js"
		outPath := filepath.Join(outDir, filename)

		if err := os.WriteFile(outPath, []byte(mod.source), 0o644); err != nil {
			return "", fmt.Errorf("writing %s: %w", outPath, err)
		}

		hash := sha256.Sum256([]byte(mod.source))
		manifest.Modules = append(manifest.Modules, ManifestModule{
			Name:     name,
			Version:  mod.version,
			SHA256:   fmt.Sprintf("%x", hash),
			Filename: filename,
		})
	}

	manifestPath := filepath.Join(outDir, "manifest.json")
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestJSON, 0o644); err != nil {
		return "", fmt.Errorf("writing manifest: %w", err)
	}

	log.Printf("[%s] wrote %d modules + manifest to %s", vs.key, len(order), outDir)
	for _, m := range manifest.Modules {
		log.Printf("  [%s] %s@%s (%s)", vs.key, m.Name, m.Version, m.Filename)
	}

	return vegaVersion, nil
}

func fetchESM(name, version string) (string, error) {
	url := fmt.Sprintf("%s/npm/%s@%s/+esm", jsdelivrBase, name, version)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response from %s: %w", url, err)
	}

	return string(body), nil
}

// topoSort computes a topological ordering of modules such that dependencies
// come before dependents. Uses Kahn's algorithm.
func topoSort(modules map[string]*module) ([]string, error) {
	// Build adjacency and in-degree.
	inDegree := make(map[string]int)
	dependents := make(map[string][]string) // dep → list of modules that depend on it

	for name := range modules {
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
	}

	for name, mod := range modules {
		for _, dep := range mod.deps {
			if _, ok := modules[dep]; !ok {
				continue // external dep not in our set, skip
			}
			dependents[dep] = append(dependents[dep], name)
			inDegree[name]++
		}
	}

	// Seed queue with zero in-degree nodes.
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue) // deterministic order

	var order []string
	for len(queue) > 0 {
		// Pop from front.
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)

		// Sort dependents for determinism.
		deps := dependents[node]
		sort.Strings(deps)

		for _, dep := range deps {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(order) != len(modules) {
		return nil, fmt.Errorf("cycle detected: got %d of %d modules", len(order), len(modules))
	}

	return order, nil
}
