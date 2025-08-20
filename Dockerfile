# syntax=docker/dockerfile:1

# Builder
FROM golang:1.24-bookworm AS build

ENV GOPROXY=https://proxy.golang.org,direct
WORKDIR /src

# Cache modules separately
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Build statically linked binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/ingestor .

# Runtime
FROM alpine:3.20

# Needed for TLS (MQTT over TLS)
RUN apk add --no-cache ca-certificates

# Create non-root user
RUN addgroup -S app && adduser -S -G app app
USER app:app

WORKDIR /app
COPY --from=build /out/ingestor /usr/local/bin/ingestor

# Document runtime env (set these at `docker run -e` time)
# ENV TTN_REGION_HOST=au1.cloud.thethings.network
# ENV TTN_APP_ID=your_app
# ENV TTN_API_KEY=your_key
# ENV PG_DSN=postgres://user:pass@host:5432/db?sslmode=disable

ENTRYPOINT ["/usr/local/bin/ingestor"]
# Default: quiet. Add "-debug" at runtime if you want verbose logs.
# CMD ["-debug"]
