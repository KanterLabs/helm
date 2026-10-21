# Build the browser bundle in the same reproducible sequence used by CI.  The
# generated files are copied into the Go embed tree before the server build.
FROM node:22.23.2-bookworm-slim@sha256:83f487e0a63425e5b4d146fb5e5be574bcbe1b7b843d3ebafdd95eaf7767a7e5 AS frontend

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
COPY openapi.yaml /src/openapi.yaml
COPY internal/httpapi/openapi.json /src/internal/httpapi/openapi.json
RUN npm run openapi:check
RUN npm run check
RUN npm run build

# Codex publishes a statically linked native executable as an optional npm
# package for each supported platform. Resolve the package selected for the
# current build architecture and copy only that executable into the final
# scratch image; Node itself is not needed at runtime.
FROM node:22.23.2-bookworm-slim@sha256:83f487e0a63425e5b4d146fb5e5be574bcbe1b7b843d3ebafdd95eaf7767a7e5 AS codex

ARG CODEX_VERSION=0.153.0
RUN npm install --global --ignore-scripts "@openai/codex@${CODEX_VERSION}" \
    && native="$(find /usr/local/lib/node_modules/@openai/codex -type f -path '*/vendor/*/bin/codex' -print -quit)" \
    && test -n "$native" \
    && install -D -m 0755 "$native" /out/codex

# cloudflared runs the optional public webhook endpoint (Cloudflare Tunnel).
# Upstream binaries still embed vulnerable Go/x/net versions. Rebuild the
# checksum-pinned 2026.9.3 source with the security fixes, retaining its feature
# code. GOSUMDB verifies the pinned module update and its transitive modules.
FROM --platform=$BUILDPLATFORM golang:1.26.10-bookworm@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51 AS cloudflared

ARG TARGETARCH
WORKDIR /src/cloudflared
ENV GOTOOLCHAIN=local GOSUMDB=sum.golang.org GOFLAGS=-mod=mod
RUN case "$TARGETARCH" in \
      amd64|arm64) ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && curl -fsSL -o /tmp/cloudflared.tar.gz "https://codeload.github.com/cloudflare/cloudflared/tar.gz/96d39adbc812dc7363834bda908970c1a2560a72" \
    && echo "7459141c5769755f20de6f7edf32d3223f643a225a7f080772cb18baa2a08d27  /tmp/cloudflared.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/cloudflared.tar.gz --strip-components=1 \
    && go get golang.org/x/net@v0.60.0 \
    && CGO_ENABLED=0 GOOS=linux GOARCH="$TARGETARCH" go build -mod=readonly -trimpath -buildvcs=false \
       -ldflags="-s -w -X main.Version=2026.9.3-helm.1 -X main.BuildTime=2026-10-11-0000" \
       -o /out/cloudflared ./cmd/cloudflared

FROM golang:1.26.10-bookworm@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend /src/web/dist ./internal/webassets/dist
# Keep the contract's compatibility path populated as well. The current Go
# asset loader imports internal/webassets; older build tooling expects the
# internal/frontend path, and having both prevents a stale embedded UI.
COPY --from=frontend /src/web/dist ./internal/frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/helm ./cmd/helm \
    && install -d -m 0750 -o 65532 -g 65532 /out/data \
    && install -d -m 0700 -o 65532 -g 65532 /out/data/codex-users

# Keep the runtime small and unprivileged. The static binary and Go-native
# healthcheck make a shell and package manager unnecessary. Copy the CA bundle
# from the existing digest-pinned Debian build stage so Cloudflare JWKS HTTPS
# verification remains available in scratch.
FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/helm /usr/local/bin/helm
COPY --from=codex --chown=65532:65532 /out/codex /usr/local/bin/codex
COPY --from=cloudflared --chown=65532:65532 /out/cloudflared /usr/local/bin/cloudflared
COPY --from=build --chown=65532:65532 /out/data /data

WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
ENV HELM_ADDR=0.0.0.0:8080 \
    HELM_DB=/data/roadmap.db \
    HELM_CODEX_BINARY=/usr/local/bin/codex \
    HELM_CLOUDFLARED_BINARY=/usr/local/bin/cloudflared \
    HELM_CODEX_HOME_ROOT=/data/codex-users \
    HELM_PUBLIC_ORIGIN=http://localhost:8080 \
    HELM_ADMIN_EMAIL= \
    HELM_SECURE_COOKIES=true \
    HELM_DEMO_SEED=false \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
USER 65532:65532

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=5 \
    CMD ["/usr/local/bin/helm", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/helm"]
