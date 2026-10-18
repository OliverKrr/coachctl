default:
    @just --list

# Build ./coachctl for this machine
build:
    go build -o coachctl ./cmd/coachctl

# Run the tests (no network: Intervals.icu, Hevy and Telegram are faked on 127.0.0.1)
test:
    go test ./...

# Format check, vet, lint, test: the gate before every commit and release
ci:
    test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
    go vet ./...
    golangci-lint run ./...
    @just test-race

# go test -race; on a kernel whose address space is too small for the race detector (39-bit VMA,
# common on arm64 boards) plain go test with a note. GitHub CI always runs -race.
test-race:
    #!/usr/bin/env bash
    set -uo pipefail
    if out=$(go test -race ./... 2>&1); then printf '%s\n' "$out"; exit 0; fi
    if grep -q 'unsupported VMA range' <<<"$out"; then
        echo "note: the race detector cannot run on this kernel (VMA range); running go test without -race. GitHub CI runs it with -race."
        exec go test ./...
    fi
    printf '%s\n' "$out"
    exit 1

# Format the code
fmt:
    gofmt -w .

# Local snapshot build of every release target into dist/ (no publish)
snapshot:
    goreleaser release --snapshot --clean

# Re-pin the Intervals.icu OpenAPI spec (docs/refs/README.md)
refresh-icu-spec:
    curl -fsS https://intervals.icu/api/v1/docs -o docs/refs/intervals-openapi.json

# Tag and push vX.Y.Z; CI builds and publishes the release (RELEASING.md)
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    [ -z "$(git status --porcelain)" ] || { echo "working tree not clean"; exit 1; }
    [ "$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "not on main"; exit 1; }
    git fetch -q origin
    [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || { echo "main is not in sync with origin/main"; exit 1; }
    just ci
    git tag -a "v{{version}}" -m "v{{version}}"
    git push origin "v{{version}}"
    echo "Tagged v{{version}}; the release workflow publishes the binaries: gh run watch"
