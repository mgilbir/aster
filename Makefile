.PHONY: vendor-js vendor-datasets build test test-all clean

vendor-js:
	go run ./cmd/vendor-js

vendor-datasets:
	go run ./cmd/vendor-datasets

build: vendor-js
	go build ./...

test: vendor-js
	go test -short ./...

test-all: vendor-js
	go test ./...

clean:
	rm -rf internal/js/modules/
