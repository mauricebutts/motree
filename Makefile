BINARY := mt

.PHONY: build test install

build:
	go build -o $(BINARY) ./cmd/mt

test:
	go test ./...

# install is just build, kept as a separate target for habit/consistency.
# This Makefile doesn't place mt anywhere outside the repo — the actual
# way to get it onto PATH is the conventional Go one: `go install
# ./cmd/mt`, which drops it at `$(go env GOPATH)/bin/mt`, already on PATH
# for most Go setups. That's a separate, deliberate step you take when
# you're ready, same as repointing the `mt` alias in .zshrc and migrating
# ~/.motree — none of that happens automatically from this Makefile.
install: build
