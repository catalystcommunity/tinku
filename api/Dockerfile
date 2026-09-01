# tinku-api container image.
#
# Build from the REPOSITORY ROOT, not api/. api/go.mod carries
#   replace github.com/catalystcommunity/tinku/coredb => ../coredb
# so the build needs that sibling module's source alongside it:
#
#   docker build -f api/Dockerfile -t tinku/api:dev .
#
# ./tools.sh build-images and docker-compose.yaml both build it that way.
FROM golang:1.26-alpine AS builder

WORKDIR /build/coredb
COPY coredb/go.mod coredb/go.sum ./
COPY coredb/ ./

WORKDIR /build/api
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ ./
# CGO_ENABLED=0 costs nothing here: the SQLite driver is modernc.org/sqlite,
# a pure-Go translation, so a static binary still speaks both backends.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /tinku .

# distroless/static: no shell and no package manager, just the binary, the
# CA bundle pgx TLS needs, and the /etc/passwd entry for the nonroot UID.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /tinku /tinku
# 8080 is CSIL-RPC and the auth callback; 9090 is /metrics, /healthz and
# /readyz. They are separate listeners so an ingress can expose one without
# exposing the other.
EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/tinku"]
CMD ["serve"]
