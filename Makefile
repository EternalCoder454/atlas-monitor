BINARY  := atlas-monitor
APPID   := com.atlas.Monitor
BINDIR  := bin
PREFIX  ?= $(HOME)/.local
DATADIR := $(PREFIX)/share/atlas-monitor
APPDIR  := $(PREFIX)/share/applications
ICONDIR := $(PREFIX)/share/icons/hicolor/scalable/apps
ICONACT := $(PREFIX)/share/icons/hicolor/scalable/actions

# TAGS is passed to the Go build. `noai` drops the Assistant page, the Ollama
# client and the Markdown renderer — see `make build-lean`.
TAGS    ?=
.PHONY: build build-lean run install install-lean uninstall clean vet test setup-ai

build:
	go build -tags "$(TAGS)" -trimpath -ldflags="-s -w" -o $(BINDIR)/$(BINARY) .

# A monitor and nothing else: no assistant, no Ollama client, no Markdown.
build-lean:
	$(MAKE) build TAGS=noai

install-lean:
	$(MAKE) install TAGS=noai

run: build
	./$(BINDIR)/$(BINARY)

vet:
	go vet ./...

test:
	go test ./...

setup-ai:
	bash scripts/setup-ai.sh

install: build
	install -Dm755 $(BINDIR)/$(BINARY) $(PREFIX)/bin/$(BINARY)
	install -Dm644 assets/style.css $(DATADIR)/style.css
	printf '%s\n' "$(CURDIR)" > $(DATADIR)/source   # record source dir for in-app "Update and restart"
	printf '%s\n' "$(TAGS)" > $(DATADIR)/buildtags  # so an in-app update rebuilds the same flavour
	install -Dm644 assets/icon.svg $(ICONDIR)/$(APPID).svg
	install -Dm644 assets/icons/atlas-cpu-symbolic.svg $(ICONACT)/atlas-cpu-symbolic.svg
	install -Dm644 assets/icons/atlas-memory-symbolic.svg $(ICONACT)/atlas-memory-symbolic.svg
	install -Dm644 assets/icons/atlas-assistant-symbolic.svg $(ICONACT)/atlas-assistant-symbolic.svg
	install -d $(APPDIR)
	sed 's|@BIN@|$(PREFIX)/bin/$(BINARY)|g' assets/$(APPID).desktop > $(APPDIR)/$(APPID).desktop
	chmod 644 $(APPDIR)/$(APPID).desktop
	-update-desktop-database $(APPDIR) 2>/dev/null || true
	-gtk4-update-icon-cache -f -t $(PREFIX)/share/icons/hicolor 2>/dev/null || true
	@echo "Installed Atlas Monitor — press Super and search 'Atlas' to launch it."

uninstall:
	rm -f $(PREFIX)/bin/$(BINARY)
	rm -f $(APPDIR)/$(APPID).desktop
	rm -f $(ICONDIR)/$(APPID).svg
	rm -f $(ICONACT)/atlas-cpu-symbolic.svg $(ICONACT)/atlas-memory-symbolic.svg $(ICONACT)/atlas-assistant-symbolic.svg
	rm -rf $(DATADIR)
	-update-desktop-database $(APPDIR) 2>/dev/null || true

clean:
	rm -rf $(BINDIR)
