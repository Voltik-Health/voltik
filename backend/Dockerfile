# --- Stage 1: test and build ---------------------------------------------------
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
# COPY go.sum ./   (uncomment once there are dependencies)
RUN go mod download
COPY . .
# Tests run here: if they fail, no image is built and nothing is deployed
RUN go vet ./... && go test ./...
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/api ./cmd/api

# --- Stage 2: small final image, no root -----------------------------------------
FROM alpine:3.22
RUN adduser -D -H -u 10001 voltik
USER voltik
COPY --from=build /out/api /api
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD wget -qO- http://127.0.0.1:8080/v1/health || exit 1
ENTRYPOINT ["/api"]
