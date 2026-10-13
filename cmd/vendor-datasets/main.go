// Command vendor-datasets downloads vega-datasets data files from jsDelivr
// and saves them to testdata/vega-datasets/data/. These files are used by
// vega-lite example specs that reference external data via relative URLs
// like "data/cars.json".
//
// Every file is checked against testdata/vega-datasets/SHA256SUMS, the
// checked-in manifest of what that version serves: a download that differs
// (a compromised or changed CDN answer) fails the command before any file is
// written. It also writes a LICENSE file noting the BSD-3-Clause license.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	vegaDatasetsVersion = "3.2.1"
	jsdelivrBase        = "https://cdn.jsdelivr.net/npm"
	outDir              = "testdata/vega-datasets"
	manifestFile        = "SHA256SUMS"
	// maxFileBytes bounds one download; the largest file is 2 MB.
	maxFileBytes = 64 << 20
)

// dataFiles lists the 43 data files referenced by vega-lite v6.4.0 example specs.
var dataFiles = []string{
	"data/airports.csv",
	"data/anscombe.json",
	"data/barley.json",
	"data/cars.json",
	"data/co2-concentration.csv",
	"data/countries.json",
	"data/disasters.csv",
	"data/driving.json",
	"data/earthquakes.json",
	"data/flights-2k.json",
	"data/flights-5k.json",
	"data/flights-airport.csv",
	"data/gapminder-health-income.csv",
	"data/gapminder.json",
	"data/github.csv",
	"data/income.json",
	"data/londonBoroughs.json",
	"data/londonCentroids.json",
	"data/londonTubeLines.json",
	"data/lookup_groups.csv",
	"data/lookup_people.csv",
	"data/monarchs.json",
	"data/movies.json",
	"data/normal-2d.json",
	"data/ohlc.json",
	"data/penguins.json",
	"data/population_engineers_hurricanes.csv",
	"data/population.json",
	"data/seattle-weather.csv",
	"data/seattle-weather-hourly-normals.csv",
	"data/sp500.csv",
	"data/species.csv",
	"data/stocks.csv",
	"data/unemployment-across-industries.json",
	"data/unemployment.tsv",
	"data/us-10m.json",
	"data/us-state-capitals.json",
	"data/weather.csv",
	"data/weekly-weather.json",
	"data/wheat.json",
	"data/windvectors.csv",
	"data/world-110m.json",
	"data/zipcodes.csv",
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("vendor-datasets: ")

	dataDir := filepath.Join(outDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("creating output dir: %v", err)
	}

	manifest, err := readManifest(filepath.Join(outDir, manifestFile))
	if err != nil {
		log.Fatal(err)
	}
	if err := checkListed(manifest); err != nil {
		log.Fatal(err)
	}

	log.Printf("downloading %d data files from vega-datasets@%s...", len(dataFiles), vegaDatasetsVersion)

	// Everything is downloaded and verified before anything is written, so a
	// bad answer leaves the vendored files as they were.
	got := make(map[string][]byte, len(dataFiles))
	for _, file := range dataFiles {
		url := fmt.Sprintf("%s/vega-datasets@%s/%s", jsdelivrBase, vegaDatasetsVersion, file)
		data, err := download(url)
		if err != nil {
			log.Fatalf("downloading %s: %v", file, err)
		}
		if err := verify(manifest, file, data); err != nil {
			log.Fatal(err)
		}
		got[file] = data
		log.Printf("  %s (%d bytes, verified)", file, len(data))
	}
	for _, file := range dataFiles {
		dest := filepath.Join(outDir, file)
		if err := os.WriteFile(dest, got[file], 0o644); err != nil {
			log.Fatalf("writing %s: %v", dest, err)
		}
	}

	// Write LICENSE file.
	licenseText := fmt.Sprintf(`The data files in this directory are from vega-datasets v%s.
Source: https://github.com/vega/vega-datasets
License: BSD-3-Clause (see package.json in the source repository)
`, vegaDatasetsVersion)
	licensePath := filepath.Join(outDir, "LICENSE")
	if err := os.WriteFile(licensePath, []byte(licenseText), 0o644); err != nil {
		log.Fatalf("writing LICENSE: %v", err)
	}

	log.Printf("done: %d data files + LICENSE written to %s", len(dataFiles), outDir)
}

// httpClient bounds each download so a stalled CDN connection cannot hang the
// build indefinitely.
var httpClient = &http.Client{Timeout: 60 * time.Second}

func download(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, maxFileBytes)
	}
	return data, nil
}

// readManifest reads a sha256sum-format manifest: "<hex>  <path>" per line,
// "#" comments.
func readManifest(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the manifest: %w", err)
	}
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		sum, file, ok := bytes.Cut([]byte(line), []byte("  "))
		if !ok || len(sum) != 64 {
			return nil, fmt.Errorf("%s:%d: want <sha256>  <path>", path, n)
		}
		if _, dup := out[string(file)]; dup {
			return nil, fmt.Errorf("%s:%d: %s is listed twice", path, n, file)
		}
		out[string(file)] = string(sum)
	}
	return out, sc.Err()
}

// checkListed requires the manifest and dataFiles to name the same files.
func checkListed(manifest map[string]string) error {
	if len(manifest) != len(dataFiles) {
		return fmt.Errorf("the manifest lists %d files, dataFiles %d", len(manifest), len(dataFiles))
	}
	for _, f := range dataFiles {
		if _, ok := manifest[f]; !ok {
			return fmt.Errorf("%s is not in the manifest", f)
		}
	}
	return nil
}

// verify checks data against the manifest's hash for file.
func verify(manifest map[string]string, file string, data []byte) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != manifest[file] {
		return fmt.Errorf("%s: sha256 %s, the manifest says %s; nothing written", file, got, manifest[file])
	}
	return nil
}
