# Build a static binary, then ship it on a minimal base. CGO is off so the
# result runs on distroless/scratch-style images without a libc.
FROM golang:1.24-alpine AS build

WORKDIR /src

# go.mod alone is enough: the project has no third-party dependencies.
COPY go.mod ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/redditwatch ./cmd/redditwatch

# Staged here so the final image carries a /data owned by the nonroot user: a
# fresh named volume inherits the ownership of the image directory it covers,
# and a root-owned one would leave the process unable to write its state.
RUN mkdir -p /data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/redditwatch /usr/local/bin/redditwatch

# State lives on a volume so restarts and image upgrades don't replay the
# backlog. 65532 is distroless's nonroot uid/gid.
COPY --from=build --chown=65532:65532 /data /data
WORKDIR /data
ENV STATE_FILE=/data/state.json

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/redditwatch"]
