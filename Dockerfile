FROM node:22-alpine AS web
WORKDIR /src/internal/management/web
COPY internal/management/web/package.json internal/management/web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY internal/management/web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web /src/internal/management/web/dist ./internal/management/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cfst-web ./cmd/cfst-web

FROM alpine:3.22
RUN addgroup -S -g 10001 cfst && adduser -S -D -H -u 10001 -G cfst cfst \
    && mkdir -p /var/lib/cfst /tmp \
    && chown -R 10001:10001 /var/lib/cfst
COPY --from=backend /out/cfst-web /usr/local/bin/cfst-web
USER 10001:10001
ENV CFST_LISTEN=0.0.0.0:8080 CFST_DATA_DIR=/var/lib/cfst CFST_RUNTIME_DIR=/tmp/cfst
EXPOSE 8080
VOLUME ["/var/lib/cfst"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/cfst-web"]
