// Command vendor-js downloads Vega and Vega-Lite ESM bundles from jsDelivr,
// resolves all transitive dependencies, rewrites import paths to canonical
// module names, and saves the result to internal/js/modules/.
//
// It also generates a manifest.json with versions, checksums, and a
// topological load order suitable for QuickJS module registration.
package main

import (
	"crypto/sha256"
	"encoding/json"
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

const (
	vegaVersion     = "5.31.0"
	vegaLiteVersion = "6.4.0"

	jsdelivrBase = "https://cdn.jsdelivr.net"
)

// Manifest is written to internal/js/modules/manifest.json.
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

	outDir := filepath.Join("internal", "js", "modules")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("creating output dir: %v", err)
	}

	modules := make(map[string]*module) // name → module

	// Seed modules to download.
	seeds := []struct {
		name    string
		version string
	}{
		{"vega", vegaVersion},
		{"vega-lite", vegaLiteVersion},
	}

	// Download queue.
	type queueItem struct {
		name    string
		version string
	}
	queue := make([]queueItem, 0, 64)
	for _, s := range seeds {
		queue = append(queue, queueItem{s.name, s.version})
	}

	log.Printf("downloading Vega %s and Vega-Lite %s from jsDelivr...", vegaVersion, vegaLiteVersion)

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if _, exists := modules[item.name]; exists {
			continue
		}

		log.Printf("  fetching %s@%s", item.name, item.version)

		src, err := fetchESM(item.name, item.version)
		if err != nil {
			log.Fatalf("fetching %s@%s: %v", item.name, item.version, err)
		}

		mod := &module{
			name:    item.name,
			version: item.version,
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

	log.Printf("downloaded %d modules, computing load order...", len(modules))

	// Topological sort for load order.
	order, err := topoSort(modules)
	if err != nil {
		log.Fatalf("topological sort: %v", err)
	}

	// Write module files and build manifest.
	manifest := Manifest{
		VegaVersion:     vegaVersion,
		VegaLiteVersion: vegaLiteVersion,
		Modules:         make([]ManifestModule, 0, len(order)),
	}

	for _, name := range order {
		mod := modules[name]
		filename := name + ".js"
		outPath := filepath.Join(outDir, filename)

		if err := os.WriteFile(outPath, []byte(mod.source), 0o644); err != nil {
			log.Fatalf("writing %s: %v", outPath, err)
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
		log.Fatalf("marshaling manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestJSON, 0o644); err != nil {
		log.Fatalf("writing manifest: %v", err)
	}

	log.Printf("wrote %d modules + manifest to %s", len(order), outDir)
	for _, m := range manifest.Modules {
		log.Printf("  %s@%s (%s)", m.Name, m.Version, m.Filename)
	}
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
