# ---- Frontend build stage ----
FROM node:22-alpine AS frontend

WORKDIR /web
COPY web/package*.json ./
RUN npm ci --prefer-offline
COPY web/ ./
RUN npm run build

# ---- Go build stage ----
FROM golang:1.25.8-alpine AS builder

WORKDIR /build

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and pre-built frontend
COPY . .
COPY --from=frontend /internal/web/dist ./internal/web/dist

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o vodarr ./cmd/vodarr

# ---- Runtime stage ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata ffmpeg

WORKDIR /app

COPY --from=builder /build/vodarr /app/vodarr
COPY config.example.yml /app/config.example.yml

# Default config location (override with -config flag or mount config.yml)
VOLUME ["/config", "/data"]

EXPOSE 9090 9091 9092

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:9090/api/health || exit 1

ENTRYPOINT ["/app/vodarr"]
CMD ["-config", "/config/config.yml"]
