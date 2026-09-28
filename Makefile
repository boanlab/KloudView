.PHONY: env build agent-binaries verify-agent-binaries openapi openapi-check test test-race test-web test-e2e run-server

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

# The store's postgres tests skip themselves unless a database is named, and a
# suite that skips the only tests covering the SQL reports success while the
# queries are broken. This starts one, points them at it, and takes it down.
PGTEST_IMAGE ?= postgres:17-alpine
PGTEST_NET ?= kloudview-pgtest
PGTEST_DB ?= kloudview-pgtest-db

test:
	-docker network create $(PGTEST_NET) >/dev/null 2>&1
	-docker rm -f $(PGTEST_DB) >/dev/null 2>&1
	docker run -d --name $(PGTEST_DB) --network $(PGTEST_NET) -e POSTGRES_DB=kvtest -e POSTGRES_USER=kvtest -e POSTGRES_PASSWORD=kvtest $(PGTEST_IMAGE) >/dev/null
	@# pg_isready answers during initialisation too, so wait for a real connection.
	@for i in $$(seq 1 60); do docker exec $(PGTEST_DB) psql -U kvtest -d kvtest -c 'select 1' >/dev/null 2>&1 && break; sleep 1; done
	docker run --rm --network $(PGTEST_NET) -e KLOUDVIEW_TEST_DATABASE_URL='postgres://kvtest:kvtest@$(PGTEST_DB):5432/kvtest?sslmode=disable' -v $(CURDIR):/src -w /src/apps/server golang:1.24-alpine go test ./... ; status=$$? ; \
		docker rm -f $(PGTEST_DB) >/dev/null 2>&1 ; docker network rm $(PGTEST_NET) >/dev/null 2>&1 ; exit $$status
	docker run --rm -v $(CURDIR):/src -w /src/apps/agent golang:1.24-alpine go test ./...

test-race:
	docker run --rm -v $(CURDIR):/src -w /src/apps/server golang:1.24 go test -race ./...
	docker run --rm -v $(CURDIR):/src -w /src/apps/agent golang:1.24 go test -race ./...

test-web:
	docker run --rm -v $(CURDIR):/src -w /src/apps/web node:22-alpine npm test

# Drives the console in a browser against a stack of its own. Needs the agent
# binaries, because the alert it waits for is raised by a real reading from a
# real agent rather than a fixture.
PLAYWRIGHT_IMAGE ?= mcr.microsoft.com/playwright:v1.55.0-jammy
E2E_PORT ?= 8099
E2E_PROJECT ?= kloudview-e2e
E2E_COMPOSE = -p $(E2E_PROJECT) --project-directory $(CURDIR) -f $(CURDIR)/docker-compose.yml -f $(CURDIR)/apps/web/e2e/compose.e2e.yml

test-e2e: agent-binaries
	KLOUDVIEW_E2E_PORT=$(E2E_PORT) docker compose $(E2E_COMPOSE) up -d
	@for i in $$(seq 1 60); do curl -sf http://127.0.0.1:$(E2E_PORT)/healthz >/dev/null 2>&1 && break; sleep 1; done
	-docker rm -f $(E2E_PROJECT)-agent >/dev/null 2>&1
	docker run -d --name $(E2E_PROJECT)-agent --network host \
		-v $(CURDIR)/dist/kloudview-agent-linux-amd64:/agent:ro \
		-e KLOUDVIEW_SERVER_URL=http://127.0.0.1:$(E2E_PORT) \
		-e KLOUDVIEW_ENROLLMENT_TOKEN=e2e-enrollment-token \
		-e KLOUDVIEW_STATE_PATH=/tmp/agent.json -e KLOUDVIEW_INTERVAL=5s \
		alpine:3.20 /agent >/dev/null
	@# Let it enrol and report before the run asks the console about it.
	@sleep 15
	docker run --rm --network host --user $$(id -u):$$(id -g) -e HOME=/tmp \
		-e KLOUDVIEW_E2E_BASE=http://127.0.0.1:$(E2E_PORT) \
		-v $(CURDIR)/apps/web:/web:ro -v $(CURDIR)/apps/web/e2e/walkthrough.mjs:/walkthrough.mjs:ro \
		$(PLAYWRIGHT_IMAGE) node /walkthrough.mjs ; status=$$? ; \
		docker rm -f $(E2E_PROJECT)-agent >/dev/null 2>&1 ; \
		KLOUDVIEW_E2E_PORT=$(E2E_PORT) docker compose $(E2E_COMPOSE) down -v >/dev/null 2>&1 ; \
		exit $$status

run-server:
	docker compose up server

