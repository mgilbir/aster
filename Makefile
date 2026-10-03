.PHONY: vendor-datasets build test test-all check lint fmacheck fuzz clean bench benchgate

vendor-datasets:
	go run ./cmd/vendor-datasets

build:
	go build ./...

test:
	go test -short ./...

# The full suite; tests that compare with upstream need the node oracle:
# (cd testdata/oracle-node && npm ci) && (cd testdata/oracle-node-vl5 && npm ci)
test-all:
	go test ./...

# Every gate, with the oracle required, and a ledger of what ran.
check:
	scripts/check.sh

lint:
	golangci-lint run ./...

fmacheck:
	scripts/fmacheck.sh

# Every coverage-guided fuzz target in turn (scripts/fuzz-targets.txt), for the
# minutes listed there, or FUZZTIME each: make fuzz FUZZTIME=30s
fuzz:
	scripts/fuzz.sh

bench:
	go test -run '^$$' -bench . -benchmem .

# The benchmarks CI gates pull requests on: HEAD against BASE (default
# origin/main), minutes. See scripts/benchgate.sh.
benchgate:
	scripts/benchgate.sh $(BASE)

# Remove compiled test binaries. The oracle cache (testdata/oracle-cache) is
# recreated on demand; delete it by hand to force fresh answers.
clean:
	rm -f *.test
