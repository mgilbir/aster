.PHONY: vendor-js build test clean

vendor-js:
	go run ./cmd/vendor-js

build: vendor-js
	go build ./...

test: vendor-js
	go test ./...

clean:
	rm -rf internal/js/modules/
