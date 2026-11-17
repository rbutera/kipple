# kipple developer targets. Nothing here may touch the real home folder.
GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: all build test lint vet fmt-check check check-no-real-home

all: check

build:
	$(GO) build ./...

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@out="$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi; \
	echo "gofmt: clean"

# golangci-lint is mandatory in CI. Locally it is skipped with a message if absent.
lint:
	@if command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		$(GOLANGCI_LINT) run ./...; \
	else \
		echo "lint: SKIPPED (golangci-lint not installed locally; CI runs it)"; \
	fi

# Fails if the library resolves the real home, or a test package lacks the guard.
check-no-real-home:
	$(GO) run ./tools/checkhome .

check: fmt-check vet lint check-no-real-home test build
	@echo "make check: OK"
