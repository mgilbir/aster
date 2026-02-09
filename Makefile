.PHONY: vendor-js vendor-datasets vendor-resvg build test test-all clean

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

build: vendor-js
	go build ./...

test: vendor-js
	go test -short ./...

test-all: vendor-js
	go test ./...

clean:
	rm -rf internal/js/modules/
