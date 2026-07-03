.PHONY: vendor-js vendor-datasets vendor-resvg vendor-quickjs build test test-all lint clean bench bench-wazero

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

build: vendor-js
	go build ./...

test: vendor-js
	go test -short ./...

test-all: vendor-js
	go test ./...

lint:
	golangci-lint run ./...

bench: vendor-js
	go test -run '^$$' -bench . -benchmem .

# Compare benchmarks: current wazero vs the mgilbir/wazero fork (tecgonic-perf).
# Tune with COUNT=, BENCH=, REF= (see scripts/bench-wazero.sh).
bench-wazero: vendor-js
	./scripts/bench-wazero.sh

clean:
	rm -rf internal/js/modules/
