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

# ---------------------------------------------------------------------------
# Linux test harness (openspec/changes/linux-port design L12). The suite runs
# in a container as the unprivileged user kipple (uid 1000) with a
# container-local home; the repository and the host module cache are mounted
# read-only, the network is off for Go (GOPROXY=off), the Go build cache
# lives in a named volume, /tmp is a tmpfs and /xdev a second tmpfs for
# cross-device tests, and the container has no network at all. Nothing from
# the host home is mounted writable.
DOCKER ?= docker
LINUX_IMAGE ?= kipple-linux-test
LINUX_VOLUME ?= kipple-linux-gocache
# LINUX_GOMOD=host mounts the host module cache read-only; LINUX_GOMOD=primed
# uses the copy make test-linux-prime downloads into the named volume.
LINUX_GOMOD ?= host
ifeq ($(LINUX_GOMOD),primed)
LINUX_GOMOD_FLAGS = -e GOMODCACHE=/cache/gomod
else
LINUX_GOMOD_FLAGS = -v "$$($(GO) env GOMODCACHE)":/gomod:ro -e GOMODCACHE=/gomod
endif
LINUX_RUN = $(DOCKER) run --rm --network none \
	-v "$(CURDIR)":/src:ro -w /src \
	$(LINUX_GOMOD_FLAGS) \
	-v $(LINUX_VOLUME):/cache \
	--tmpfs /tmp:rw,exec,mode=1777 --tmpfs /xdev:rw,mode=1777 \
	-e GOFLAGS="-buildvcs=false -mod=readonly" -e GOPROXY=off -e GOTOOLCHAIN=local \
	-e GOCACHE=/cache/go-build -e GOPATH=/tmp/gopath -e HOME=/home/kipple -e TMPDIR=/tmp

.PHONY: linux-image test-linux test-linux-prime test-linux-tarball

linux-image:
	$(DOCKER) build -q -t $(LINUX_IMAGE) test/linux >/dev/null

# The whole default suite with -race, go vet, the no-real-home check and
# the agent flow, as uid 1000; fails if the container home changed.
test-linux: linux-image
	$(LINUX_RUN) $(LINUX_IMAGE) test/linux/run.sh

# After make release-snapshot: the README's Linux install steps (checksum,
# unpack, install, kipple version) against the snapshot archive for this
# Docker's architecture, then version --json and a fixture scan with the
# installed binary, as uid 1000 with the home-unchanged check.
test-linux-tarball: linux-image
	$(LINUX_RUN) $(LINUX_IMAGE) test/linux/run.sh scripts/linux-tarball-check.sh dist

# Fallback when the read-only host module cache cannot be used: downloads the
# modules once into the named volume (network on for this step only). Then
# run make test-linux LINUX_GOMOD=primed.
test-linux-prime: linux-image
	$(DOCKER) run --rm -v "$(CURDIR)":/src:ro -w /src -v $(LINUX_VOLUME):/cache \
		-e GOMODCACHE=/cache/gomod -e GOFLAGS="-modcacherw -mod=readonly" -e GOTOOLCHAIN=local \
		-e GOCACHE=/cache/go-build -e GOPATH=/tmp/gopath --tmpfs /tmp:rw,exec,mode=1777 \
		$(LINUX_IMAGE) go mod download

# Real bind, tmpfs and overlay mounts inside a fixture home (task 2.7): a
# throwaway --privileged container whose only mount is the read-only repo,
# with the kipple binary cross-compiled into dist/ first. If Docker refuses
# privileged containers, this target fails saying so; it never passes
# without running the check.
.PHONY: test-linux-mounts
test-linux-mounts: linux-image
	@arch="$$($(DOCKER) version --format '{{.Server.Arch}}')" && \
	CGO_ENABLED=0 GOOS=linux GOARCH="$$arch" $(GO) build -trimpath -o dist/linux-mount-check/kipple ./cmd/kipple && \
	for pkg in internal/apply internal/tui cmd/kipple; do \
		CGO_ENABLED=0 GOOS=linux GOARCH="$$arch" $(GO) test -c -o "dist/linux-mount-check/$$(basename $$pkg).test" "./$$pkg" || exit 1; \
	done
	@$(DOCKER) run --rm --privileged --network none $(LINUX_IMAGE) true >/dev/null 2>&1 || \
		{ echo "test-linux-mounts: this Docker cannot run --privileged containers, so the real-mount check cannot run (the stub-based mount tests in make test-linux remain the contract)" >&2; exit 1; }
	$(DOCKER) run --rm --privileged --network none --user 0 -v "$(CURDIR)":/src:ro -w /src \
		--tmpfs /tmp:rw,exec,mode=1777 $(LINUX_IMAGE) scripts/linux-mount-check.sh /src/dist/linux-mount-check/kipple
