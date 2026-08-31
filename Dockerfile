# Production single-image deployment (frontend is embedded into the Go binary).
# Standard hardened run (add ELYSIA_API_MASTER_KEY only when using an external key):
# docker run -d --name elysia-api --restart unless-stopped --init --read-only --tmpfs /tmp:size=64m,mode=1777 --security-opt no-new-privileges:true --cap-drop ALL -p "${ELYSIA_BIND_ADDRESS:-127.0.0.1}:${ELYSIA_HTTP_PORT:-8765}:8765" -v elysia-data:/data -e ELYSIA_API_HOST=0.0.0.0 [-e ELYSIA_API_MASTER_KEY] elysia-api:local

FROM node:22-bookworm-slim AS frontend-builder
WORKDIR /workspace
COPY package.json ./
COPY packages/webui/package.json packages/webui/package.json
RUN npm install --no-audit --no-fund --package-lock=false
COPY packages/webui/ packages/webui/
RUN npm run build --workspace @root/webui

FROM golang:1.25-bookworm AS backend-builder
WORKDIR /workspace/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend-builder /workspace/packages/webui/dist ./webui/dist
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/elysia-api .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 elysia \
    && adduser -S -D -H -u 65532 -G elysia elysia \
    && mkdir -p /data \
    && chown 65532:65532 /data \
    && printf '%s\n' \
       '#!/bin/sh' \
       'set -eu' \
       '/usr/local/bin/elysia-api "$@" &' \
       'pid=$!' \
       'shutdown() {' \
       '  wget -q -T 2 -O /dev/null --post-data="" http://127.0.0.1:8765/__shutdown || kill -TERM "$pid" 2>/dev/null || true' \
       '}' \
       'trap shutdown TERM INT' \
       'status=0' \
       'wait "$pid" || status=$?' \
       'trap - TERM INT' \
       'exit "$status"' \
       > /usr/local/bin/docker-entrypoint.sh \
    && chmod 0755 /usr/local/bin/docker-entrypoint.sh
COPY --from=backend-builder /out/elysia-api /usr/local/bin/elysia-api
WORKDIR /data
ENV HOME=/data
USER 65532:65532
EXPOSE 8765
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -q -T 2 -O /dev/null http://127.0.0.1:8765/health || exit 1
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["--config", "/data/config.json"]
