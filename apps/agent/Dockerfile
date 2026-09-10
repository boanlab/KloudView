FROM golang:1.24-alpine AS build
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY apps/agent/go.mod ./
COPY apps/agent/ .
# The agent/server contract test walks up for this; without it the two sides
# build unverified.
COPY docs/contracts/ /docs/contracts/
RUN CGO_ENABLED=0 go test ./... && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -buildvcs=false -trimpath -tags netgo,osusergo -ldflags="-s -w -buildid= -X main.version=${VERSION}" -o /out/kloudview-agent ./cmd/kloudview-agent

FROM alpine:3.21
RUN addgroup -S kloudview && adduser -S -G kloudview kloudview && mkdir -p /var/lib/kloudview && chown kloudview:kloudview /var/lib/kloudview
COPY --from=build /out/kloudview-agent /usr/local/bin/kloudview-agent
USER kloudview
ENTRYPOINT ["kloudview-agent"]
