BINARY  := atlas-monitor
APPID   := com.atlas.Monitor
BINDIR  := bin
PREFIX  ?= $(HOME)/.local
DATADIR := $(PREFIX)/share/atlas-monitor
APPDIR  := $(PREFIX)/share/applications
ICONDIR := $(PREFIX)/share/icons/hicolor/scalable/apps
ICONACT := $(PREFIX)/share/icons/hicolor/scalable/actions

# Atlas's own symbolic icons, installed into the actions icon directory. They
# are all atlas-prefixed on purpose: icon lookup falls back to hicolor last, so
# a generic name here would lose to the system theme and never be used.
ICONS   := cpu memory disk gpu network wifi battery apps services settings update trash reset menu warning startup energy sensors theme opacity text timer branch folder document

# TAGS is passed to the Go build. Nothing here needs one; it is kept so an
# in-app update rebuilds with whatever the install was built with.
TAGS    ?=
.PHONY: build build-profile run install uninstall clean vet test test-race

build:
	go build -tags "$(TAGS)" -trimpath -ldflags="-s -w" -o $(BINDIR)/$(BINARY) .

# For measuring, not shipping: records a pprof profile when ATLAS_CPUPROFILE or
# ATLAS_MEMPROFILE is set, and ends the run on SIGTERM. See main_profile.go.
build-profile:
	$(MAKE) build TAGS="$(TAGS) profile" BINARY=atlas-monitor-profile

run: build
	./$(BINDIR)/$(BINARY)

vet:
	go vet ./...

test:
	go test ./...

# The race detector, over the packages that share state between goroutines and
# the one that builds real GObjects. internal/ui used to need checkptr switched
# off, for the unsafe pointer arithmetic in gotk4's old weak-reference
# dependency; gotk4 0.4.1 dropped it, and the pointer check runs everywhere.
test-race:
	go test -race -count=1 ./internal/stats/ ./internal/process/ ./internal/gpu/ ./internal/power/ \
		./internal/ease/ ./internal/desktop/ ./internal/sensors/ ./internal/ui/

install: build
	install -Dm755 $(BINDIR)/$(BINARY) $(PREFIX)/bin/$(BINARY)
	install -Dm644 assets/style.css $(DATADIR)/style.css
	printf '%s\n' "$(CURDIR)" > $(DATADIR)/source   # record source dir for in-app "Update and restart"
	printf '%s\n' "$(TAGS)" > $(DATADIR)/buildtags  # so an in-app update rebuilds the same flavour
	install -Dm644 assets/icon.svg $(ICONDIR)/$(APPID).svg
	for icon in $(ICONS); do \
		install -Dm644 assets/icons/atlas-$$icon-symbolic.svg $(ICONACT)/atlas-$$icon-symbolic.svg; \
	done
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
	for icon in $(ICONS); do rm -f $(ICONACT)/atlas-$$icon-symbolic.svg; done
	rm -rf $(DATADIR)
	-update-desktop-database $(APPDIR) 2>/dev/null || true

clean:
	rm -rf $(BINDIR)
