# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates tzdata
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.21 AS api
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 linknest
WORKDIR /app
COPY --from=build /out/api /app/api
COPY configs/config.example.yaml /app/configs/config.example.yaml
USER linknest
EXPOSE 18080
ENTRYPOINT ["/app/api"]

FROM alpine:3.21 AS worker
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 linknest
WORKDIR /app
COPY --from=build /out/worker /app/worker
COPY configs/config.example.yaml /app/configs/config.example.yaml
USER linknest
ENTRYPOINT ["/app/worker"]

