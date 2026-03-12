VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build install clean

build:
	go build -ldflags "$(LDFLAGS)" -o maestro ./cmd/maestro/

install: build
	cp maestro $(HOME)/bin/maestro

clean:
	rm -f maestro
