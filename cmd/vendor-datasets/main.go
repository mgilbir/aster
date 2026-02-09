// Command vendor-datasets downloads vega-datasets data files from jsDelivr
// and saves them to testdata/vega-datasets/data/. These files are used by
// vega-lite example specs that reference external data via relative URLs
// like "data/cars.json".
//
// It also writes a LICENSE file noting the BSD-3-Clause license.
package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

const (
	vegaDatasetsVersion = "3.2.1"
	jsdelivrBase        = "https://cdn.jsdelivr.net/npm"
	outDir              = "testdata/vega-datasets"
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

	log.Printf("downloading %d data files from vega-datasets@%s...", len(dataFiles), vegaDatasetsVersion)

	for _, file := range dataFiles {
		filename := filepath.Base(file)
		url := fmt.Sprintf("%s/vega-datasets@%s/%s", jsdelivrBase, vegaDatasetsVersion, file)
		dest := filepath.Join(outDir, file)

		data, err := download(url)
		if err != nil {
			log.Fatalf("downloading %s: %v", filename, err)
		}

		if err := os.WriteFile(dest, data, 0o644); err != nil {
			log.Fatalf("writing %s: %v", dest, err)
		}

		hash := sha256.Sum256(data)
		log.Printf("  %s (%d bytes, sha256:%x)", filename, len(data), hash)
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

func download(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}

	return data, nil
}
