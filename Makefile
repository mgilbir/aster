.PHONY: vendor-datasets build test test-all check lint fmacheck fuzz clean bench benchgate vectors

vendor-datasets:
	go run ./cmd/vendor-datasets

build:
	go build ./...

# The -short suite (no node needed), ending with a summary of the tests that
# skipped and why: under -short every comparison with upstream does, and the
# replays of upstream's test vectors do without scripts/record-upstream-vectors.sh.
test:
	go test -short -json ./... | go run ./internal/cmd/testsummary

# The full suite; tests that compare with upstream need the node oracle:
# (cd testdata/oracle-node && npm ci) && (cd testdata/oracle-node-vl5 && npm ci)
test-all:
	go test ./...

# The gates CI runs, with the oracle required, and a ledger of what ran
# (scripts/check.sh; `scripts/check.sh -fast` for the quick ones).
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

# Regenerate every committed vector file (testdata/vectors.txt) with the
# pinned node and fail on any difference; `make vectors WRITE=1` re-records.
vectors:
	go run ./internal/cmd/vectors $(if $(WRITE),-write,-repeat 2)

# Remove compiled test binaries. The oracle cache (testdata/oracle-cache) is
# recreated on demand; delete it by hand to force fresh answers.
clean:
	rm -f *.test
