BIN := bin
CMD := ai-attributions
LDFLAGS := -s -w
# The tool drives git and git-filter-repo as subprocesses and links nothing of
# its own, so the shipped binary is static.
export CGO_ENABLED := 0

.PHONY: all build clean coverage fmt lint release test

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

## lint: the checks CI runs, both of them. `run` accepts an unknown key inside
## `linters.settings` and exits 0, which leaves that setting disabled while CI
## stays green, so `config verify` is what rejects a misspelled one.
lint:
	golangci-lint config verify
	golangci-lint run

## release: cut VERSION, both moves in one command. action.yml's version default
## names the release it was cut with, so the edit has to be in the commit the tag
## points at: publishing one without the other leaves @v1 running this release's
## action against the previous release's binary, and protect-release-tags has no
## bypass to take the tag back with. The release workflow checks the same thing
## before it publishes, for a tag cut by hand.
release:
	@set -e; \
	test -n "$(VERSION)" || { echo "usage: make release VERSION=1.3.4"; exit 1; }; \
	tag=v$(patsubst v%,%,$(VERSION)); \
	case $$tag in v[0-9]*.[0-9]*.[0-9]*) ;; *) echo "$$tag is not a release version"; exit 1;; esac; \
	test -z "$$(git status --porcelain)" || { echo "the working tree has changes"; exit 1; }; \
	test "$$(git rev-parse --abbrev-ref HEAD)" = main || { echo "not on main"; exit 1; }; \
	git rev-parse -q --verify "refs/tags/$$tag" >/dev/null && { echo "$$tag already exists"; exit 1; } || true; \
	sed -i "/^  version:/,/^  [a-z]/ s|^\(    default: \).*|\1$$tag|" action.yml; \
	got=$$(awk '/^  version:/{f=1} f && /^    default:/{print $$2; exit}' action.yml); \
	test "$$got" = "$$tag" || { git checkout action.yml; echo "action.yml still installs $$got"; exit 1; }; \
	git commit --quiet action.yml --message="Install $$tag by default"; \
	git tag "$$tag"; \
	git push --atomic origin main "$$tag"

clean:
	rm -rf $(BIN) dist
	rm -f coverage.txt
