# --- Stage 1: Build & test ---------------------------------------------------
FROM golang:1.24-alpine AS build
WORKDIR /src

# Copy module definition files
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code and verify
COPY . .
RUN go vet ./...

# Build statically linked binary with build-time version
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/api ./cmd/api

# --- Stage 2: Minimal runtime, non-root ---------------------------------------
FROM alpine:3.22

# Security: run as unprivileged user
RUN adduser -D -H -u 10001 voltik
USER voltik

COPY --from=build /out/api /api

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/v1/health || exit 1

ENTRYPOINT ["/api"]
