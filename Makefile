BINARY := grove
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin

.PHONY: build install vet test clean

build:
	go build -o bin/$(BINARY) ./cmd/grove

install: build
	install -d $(BINDIR)
	install -m 0755 bin/$(BINARY) $(BINDIR)/$(BINARY)
	@echo "Installed $(BINARY) -> $(BINDIR)/$(BINARY)"
	@echo "Source shell/grove.bash (bash/zsh) or shell/grove.fish (fish) for the cd-wrapper."

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -rf bin
