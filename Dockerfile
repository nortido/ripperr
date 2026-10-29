# сборщик на платформе хоста: Go кросс-компилирует сам, без эмуляции qemu
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder
ARG TARGETOS TARGETARCH
ARG VERSION=dev
#RUN go install github.com/go-delve/delve/cmd/dlv@latest
ENV GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0
COPY . /app
WORKDIR /app
RUN go build -ldflags "-X ripperr/internal/web.Version=$VERSION" -o main ./cmd && ls -la
EXPOSE 6410 2345
#CMD ["/go/bin/dlv", "--listen=:2345", "--headless=true", "--api-version=2", "--accept-multiclient", "exec", "/app/main"]
#CMD /app/main

FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/nortido/ripperr"
WORKDIR /app
COPY --from=builder /app/main /app/
RUN mkdir -p /config/blackhole
ENV CONFIG_DIR=/config
ENV RIPPERR_IN_DOCKER=1
VOLUME ["/config"]
EXPOSE 6410
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 CMD wget -q --spider http://127.0.0.1:6410/readyz || exit 1
CMD ["/app/main"]