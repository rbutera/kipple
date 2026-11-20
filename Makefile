# kipple developer targets. Nothing here may touch the real home folder.
GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: all build test test-permanent test-gui lint vet fmt-check check check-no-real-home release-snapshot agent-flow-check gui-screenshots

all: check

# Both builds: the default (no Fyne) and the optional GUI (gui tag).
build:
	$(GO) build ./...
	$(GO) build -tags gui -o /dev/null ./cmd/kipple-gui

test:
	$(GO) test -race ./...

# --permanent is built only with the kipple_permanent tag (not in v0.1.0);
# its code and tests must stay green.
test-permanent:
	$(GO) test -race -tags kipple_permanent ./internal/apply/... ./cmd/...

vet:
	$(GO) vet ./...
	$(GO) vet -tags gui ./cmd/kipple-gui/...

fmt-check:
	@out="$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi; \
	echo "gofmt: clean"

# The GUI's widget tests run on Fyne's test driver (software, no window).
# Fyne writes a font cache and preferences under $$HOME, so the run gets a
# temp HOME (TestMain redirects it too, and fails if the real files change);
# Go's own build cache, module cache and settings keep their real locations,
# and go runs by its real path (a version-manager shim may need the real HOME).
test-gui:
	@tmp="$$(mktemp -d "$${TMPDIR:-/tmp}/kipple-gui-home.XXXXXX")"; \
	gobin="$$($(GO) env GOROOT)/bin/go"; \
	GOCACHE="$$($(GO) env GOCACHE)" GOMODCACHE="$$($(GO) env GOMODCACHE)" GOPATH="$$($(GO) env GOPATH)" GOENV="$$($(GO) env GOENV)" \
	HOME="$$tmp" XDG_CACHE_HOME="$$tmp/.cache" XDG_CONFIG_HOME="$$tmp/.config" \
	"$$gobin" test -race -tags gui ./cmd/kipple-gui/...; status=$$?; command rm -rf "$$tmp"; exit $$status

# golangci-lint is mandatory in CI. Locally it is skipped with a message if absent.
lint:
	@if command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		$(GOLANGCI_LINT) run ./... && $(GOLANGCI_LINT) run --build-tags kipple_permanent ./... && \
		$(GOLANGCI_LINT) run --build-tags gui ./...; \
	else \
		echo "lint: SKIPPED (golangci-lint not installed locally; CI runs it)"; \
	fi

# Fails if the library resolves the real home, or a test package lacks the guard.
check-no-real-home:
	$(GO) run ./tools/checkhome .

check: fmt-check vet lint check-no-real-home test test-permanent test-gui build
	@echo "make check: OK"

# Builds the release archives into dist/ exactly as a tag would, without
# signing or publishing anything.
release-snapshot:
	goreleaser release --snapshot --clean --skip=sign

# Runs the README "For agents" commands against a fixture in a temp folder.
agent-flow-check:
	scripts/agent-flow-check.sh

# Captures every GUI screen from a fixture home into OUT (never the real home).
gui-screenshots:
	scripts/gui-screenshots.sh $(or $(OUT),dist/gui-screenshots)
