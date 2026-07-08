.PHONY: vendor-js vendor-datasets vendor-resvg vendor-quickjs build test test-all lint clean bench bench-wazero

# The vendored JS modules under internal/js/modules/ are committed; run this
# only to update them (requires network, rewrites tracked files).
vendor-js:
	go run ./cmd/vendor-js

vendor-datasets:
	go run ./cmd/vendor-datasets

vendor-resvg:
	docker build -t aster-resvg-build resvg-wasm/
	@docker rm aster-resvg-extract 2>/dev/null || true
	docker create --name aster-resvg-extract aster-resvg-build /nonexistent
	docker cp aster-resvg-extract:/output/resvg.wasm internal/resvg/resvg.wasm
	@docker rm aster-resvg-extract 2>/dev/null || true

vendor-quickjs:
	docker build -t aster-quickjs-build quickjs-wasm/
	@docker rm aster-quickjs-extract 2>/dev/null || true
	docker create --name aster-quickjs-extract aster-quickjs-build /nonexistent
	docker cp aster-quickjs-extract:/output/quickjs.wasm internal/quickjs/quickjs.wasm
	@docker rm aster-quickjs-extract 2>/dev/null || true

build:
	go build ./...

test:
	go test -short ./...

test-all:
	go test ./...

lint:
	golangci-lint run ./...

bench:
	go test -run '^$$' -bench . -benchmem .

# Compare benchmarks: current andsifr (WASM runtime) vs another revision of it.
# Tune with COUNT=, BENCH=, REF= (see scripts/bench-wazero.sh).
bench-wazero:
	./scripts/bench-wazero.sh

# Remove benchmark comparison artifacts and compiled test binaries. Vendored
# assets are committed sources and are deliberately left alone.
clean:
	rm -f bench-baseline.txt bench-fork.txt wazero-fork.mod wazero-fork.sum *.test
