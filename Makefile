# Local entry points for the kos-devicemanager extension package. CI runs
# verify, lint, test and acceptance, one step per target. Nothing here pushes or
# publishes: the repository is the delivery.

SHELL := /bin/sh
GO ?= go

.PHONY: all verify lint fmt-check vet test acceptance package clean

all: verify test

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt required for:"; echo "$$unformatted"; exit 1; \
	fi

vet:
	$(GO) vet ./...

verify: fmt-check vet

lint: fmt-check vet

test:
	$(GO) test ./...

acceptance:
	$(GO) test ./tests/... -count=1 -v

package:
	$(GO) run ./cmd/kosdevicemanager -out dist

clean:
	rm -rf dist
