FROM node:24-alpine AS frontend-builder
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build
RUN find dist -type f -name '*.map' -delete

FROM golang:1.24-alpine AS backend-builder
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/server ./cmd/server

FROM alpine:3.22
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=backend-builder /out/server ./server
COPY backend/configs/config.docker.yaml ./configs/config.docker.yaml
COPY backend/scripts/migrations ./scripts/migrations
COPY --from=frontend-builder /src/frontend/dist ./frontend/dist
RUN mkdir -p /app/uploads && chown -R app:app /app
USER app
EXPOSE 18080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:18080/health >/dev/null || exit 1
CMD ["./server", "-c", "configs/config.docker.yaml"]
