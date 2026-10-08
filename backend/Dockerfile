# --- Fase 1: testar e compilar -------------------------------------------------
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
# COPY go.sum ./   (descomentar quando houver dependências)
RUN go mod download
COPY . .
# Os testes correm aqui: se falharem, a imagem não é criada e nada é publicado
RUN go vet ./... && go test ./...
ARG VERSAO=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.versao=${VERSAO}" -o /out/api ./cmd/api

# --- Fase 2: imagem final, pequena e sem privilégios ---------------------------
FROM alpine:3.22
RUN adduser -D -H -u 10001 voltik
USER voltik
COPY --from=build /out/api /api
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD wget -qO- http://127.0.0.1:8080/v1/saude || exit 1
ENTRYPOINT ["/api"]
