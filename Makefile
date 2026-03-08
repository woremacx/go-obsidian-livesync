.PHONY: all build test clean

CMDS := livesync-pull livesync-push livesync-sync
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "(dev)")
LDFLAGS := -X github.com/woremacx/go-obsidian-livesync/internal/version.Version=$(VERSION)

all: build

build: $(CMDS)

$(CMDS):
	go build -ldflags '$(LDFLAGS)' -o $@ ./cmd/$@

test:
	go test ./...

clean:
	rm -f $(CMDS)
