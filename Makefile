.PHONY: env build agent-binaries verify-agent-binaries openapi openapi-check test test-race test-web run-server

VERSION ?= dev
AGENT_ARCHES ?= amd64 arm64

env:
	tools/gen-env.sh

build:
	KLOUDVIEW_VERSION=$(VERSION) docker compose build

agent-binaries:
	mkdir -p dist
	docker run --rm --user $$(id -u):$$(id -g) -e GOCACHE=/tmp/go-cache -v $(CURDIR):/src -w /src/apps/agent golang:1.24-alpine sh -ec 'for arch in $(AGENT_ARCHES); do CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -buildvcs=false -trimpath -tags netgo,osusergo -ldflags "-s -w -buildid= -X main.version=$(VERSION)" -o /src/dist/kloudview-agent-linux-$$arch ./cmd/kloudview-agent; done'

verify-agent-binaries: agent-binaries
	@for arch in $(AGENT_ARCHES); do file dist/kloudview-agent-linux-$$arch | grep -q 'statically linked' || { echo "dist/kloudview-agent-linux-$$arch is not static"; exit 1; }; done
	sha256sum $$(for arch in $(AGENT_ARCHES); do printf 'dist/kloudview-agent-linux-%s ' $$arch; done) > dist/SHA256SUMS
	@# The version these binaries report. The server refuses to offer them when
	@# it disagrees with KLOUDVIEW_AGENT_TARGET_VERSION, because an agent that
	@# updates and still does not match the target updates again, every beat.
	printf '%s\n' '$(VERSION)' > dist/VERSION

openapi:
	docker run --rm --user $$(id -u):$$(id -g) -e GOCACHE=/tmp/go-cache -v $(CURDIR):/src -w /src golang:1.24-alpine sh -ec 'GO111MODULE=off go run ./tools/openapi/main.go -output docs/openapi.json'

openapi-check:
	docker run --rm -e GOCACHE=/tmp/go-cache -v $(CURDIR):/src -w /src golang:1.24-alpine sh -ec 'GO111MODULE=off go run ./tools/openapi/main.go -check -output docs/openapi.json'

test:
	docker run --rm -v $(CURDIR):/src -w /src/apps/server golang:1.24-alpine go test ./...
	docker run --rm -v $(CURDIR):/src -w /src/apps/agent golang:1.24-alpine go test ./...

test-race:
	docker run --rm -v $(CURDIR):/src -w /src/apps/server golang:1.24 go test -race ./...
	docker run --rm -v $(CURDIR):/src -w /src/apps/agent golang:1.24 go test -race ./...

test-web:
	docker run --rm -v $(CURDIR):/src -w /src/apps/web node:22-alpine npm test

run-server:
	docker compose up server

