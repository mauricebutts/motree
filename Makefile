BINARY := mt
INSTALL_PATH := $(HOME)/Util/mt-go

.PHONY: build test install

build:
	go build -o $(BINARY) ./cmd/mt

test:
	go test ./...

# install copies the built binary to ~/Util/mt-go. It deliberately never
# touches the existing bash tool (~/Util/motree.sh) or the `mt` alias in
# .zshrc — that cutover (repointing the alias, migrating ~/.motree) is a
# separate, deliberate later step, not something `make install` does on
# its own. Point MOTREE_STATE_FILE at this binary to try it out alongside
# the bash tool without disturbing it (see README.md).
install: build
	mkdir -p $(HOME)/Util
	cp $(BINARY) $(INSTALL_PATH)
	@echo "Installed to $(INSTALL_PATH) -- ~/Util/motree.sh and the mt alias are untouched."
