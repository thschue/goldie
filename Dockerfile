# The goldie image: one static binary with the web UI built in (ADR 0019),
# the example content under /content, and no shell. Build it from the repository root:
#   docker build -t goldie .
# compose.yaml runs it. Renovate updates the three base images (renovate.json).

# The SPA. The build writes to internal/ui/dist/app, which the Go build embeds.
FROM node:24-alpine@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5 AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web ./
RUN npm run build

FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/internal/ui/dist/app internal/ui/dist/app
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w \
      -X github.com/thschue/goldie/internal/platform.Version=${VERSION} \
      -X github.com/thschue/goldie/internal/platform.Commit=${COMMIT} \
      -X github.com/thschue/goldie/internal/platform.BuildDate=${BUILD_DATE}" \
    -o /out/goldie ./cmd/goldie

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/goldie /usr/local/bin/goldie
COPY examples/knowledge /content
# Listeners on every interface of the container: compose publishes them on the
# host's loopback only. --dev is not used, it picks random loopback ports.
ENV GOLDIE_MODE=all \
    GOLDIE_ADDR=:8080 \
    GOLDIE_ADMIN_ADDR=:8081 \
    GOLDIE_CONTENT_DIR=/content
EXPOSE 8080 8081
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/goldie", "health", "--ready"]
ENTRYPOINT ["/usr/local/bin/goldie"]
CMD ["server"]
