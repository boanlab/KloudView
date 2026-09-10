FROM golang:1.24-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY apps/server/go.mod ./
COPY apps/server/ .
# The agent/server contract test walks up for this; without it the two sides
# build unverified.
COPY docs/contracts/ /docs/contracts/
RUN CGO_ENABLED=0 go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/kloudview-server ./cmd/kloudview-server

FROM alpine:3.21
RUN addgroup -S kloudview && adduser -S -G kloudview kloudview && mkdir -p /var/lib/kloudview && chown kloudview:kloudview /var/lib/kloudview
COPY --from=build /out/kloudview-server /usr/local/bin/kloudview-server
USER kloudview
EXPOSE 8080
ENTRYPOINT ["kloudview-server"]
