.PHONY: all build test clean

CMDS := livesync-pull livesync-push livesync-sync

all: build

build: $(CMDS)

$(CMDS):
	go build -o $@ ./cmd/$@

test:
	go test ./...

clean:
	rm -f $(CMDS)
