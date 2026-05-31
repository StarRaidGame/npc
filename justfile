# StarRaid npc — install, build, run, test. Run `just` to list recipes.
#
# This module imports the generated Go bindings from the `protocol` repo. Point
# `protocol_path` at wherever you checked that repo out. It defaults to a sibling
# directory (`../protocol`); override it if yours lives elsewhere:
#
#     just protocol_path=/path/to/protocol build
#     # or, persistently:
#     export STARRAID_PROTOCOL_PATH=/path/to/protocol

protocol_path := env_var_or_default("STARRAID_PROTOCOL_PATH", "../protocol")

# List available recipes
default:
    @just --list

# One-time setup: wire the local Go workspace to the protocol checkout, fetch deps
install: _workspace
    go mod download

# Create the local go.work pointing at the protocol bindings (idempotent).
# go.work is gitignored: it is per-developer local config, never committed.
_workspace:
    #!/usr/bin/env sh
    set -eu
    if [ ! -d "{{protocol_path}}/gen/go" ]; then
        echo "protocol Go bindings not found at {{protocol_path}}/gen/go" >&2
        echo "clone the protocol repo as a sibling, or set protocol_path=/path/to/protocol" >&2
        exit 1
    fi
    if [ ! -f go.work ]; then
        go work init . "{{protocol_path}}/gen/go"
        echo "created go.work -> {{protocol_path}}/gen/go"
    fi

# Build all packages
build: _workspace
    go build ./...

# Run the reference bot. Pass flags directly, e.g.
#   just run -server localhost:60000 -user dev -secret s3cr3t
run *args: _workspace
    go run ./cmd/npc {{args}}

# Run the dispatcher (spawns bots on the server's request)
run-dispatcher *args: _workspace
    go run ./cmd/dispatcher {{args}}

# Run the tests
test: _workspace
    go test ./...

# Format Go code
fmt:
    go fmt ./...

# Vet
vet: _workspace
    go vet ./...

# Remove the local Go workspace files (recreate with `just install`)
clean:
    rm -f go.work go.work.sum
