package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// memLoader maps every URL form that serves vega-datasets, as the oracle and
// the corpus comparison do, so that both engines time the same local data
// (C148, audit 2026-10-18: it hard-coded 3 of the 8 forms). Each form of
// testdata/oracle-node/dataset-urls.json must have an example here.
func TestMemLoaderMapsEveryDatasetURL(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	examples := []string{
		"https://cdn.jsdelivr.net/npm/vega-datasets@3.2.1/data/cars.json",
		"https://cdn.jsdelivr.net/npm/vega-datasets/data/cars.json",
		"https://cdn.jsdelivr.net/gh/vega/vega-datasets@v3.2.1/data/cars.json",
		"https://raw.githubusercontent.com/vega/vega-datasets/main/data/cars.json",
		"https://vega.github.io/vega-datasets/data/cars.json",
		"https://vega.github.io/vega-lite/data/cars.json",
		"https://vega.github.io/vega/data/cars.json",
		"https://raw.githubusercontent.com/vega/vega/main/docs/data/cars.json",
		"https://raw.githubusercontent.com/vega/vega-lite/main/site/data/cars.json",
		"https://raw.githubusercontent.com/vega/vega-lite/main/data/cars.json",
	}
	b, err := os.ReadFile(filepath.Join(root, "testdata/oracle-node/dataset-urls.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Patterns []string }
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.Patterns {
		re := regexp.MustCompile(p)
		covered := false
		for _, u := range examples {
			covered = covered || re.MatchString(u)
		}
		if !covered {
			t.Errorf("no example URL for the dataset form %s", p)
		}
	}
	m, err := newMemLoader(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range examples {
		got, err := m.Load(context.Background(), u)
		if err != nil || len(got) == 0 {
			t.Errorf("%s: %v", u, err)
		}
	}
}
