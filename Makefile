BIN := bin
CMD := ai-attributions
LDFLAGS := -s -w
# The tool drives git and git-filter-repo as subprocesses and links nothing of
# its own, so the shipped binary is static.
export CGO_ENABLED := 0

.PHONY: all build clean coverage fmt lint test

all: build

## build: a static binary, stripped
build:
	@mkdir -p $(BIN)
	go build -ldflags="$(LDFLAGS)" -trimpath -o $(BIN)/$(CMD) .

## test: the whole suite. The fork tests build real repositories, so git has to
## be installed; nothing here needs git-filter-repo, which only apply drives.
test:
	go test ./...

## coverage: the suite with the race detector, then the per-function report.
## CGO_ENABLED=1 for this recipe only: -race needs cgo, and the export above
## turns it off for every other one because the shipped binary is static.
coverage:
	CGO_ENABLED=1 go test -race -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -func=coverage.txt

## fmt: apply the import grouping and gofmt rules CI checks
fmt:
	golangci-lint fmt

## lint: the same golangci-lint run CI does
lint:
	golangci-lint run

clean:
	rm -rf $(BIN) dist
	rm -f coverage.txt
