# Build a static, dependency-free binary and ship nothing else.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY src ./src
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cheap-shot ./src/cmd/cheap-shot

FROM scratch
COPY --from=build /out/cheap-shot /cheap-shot
# No TLS client and no user lookups, so no CA bundle and no /etc/passwd are
# needed; a numeric UID is enough. Writing to a v4l2loopback device needs the
# host's "video" group, which compose adds with group_add.
USER 65534:65534
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s \
    CMD ["/cheap-shot", "healthcheck", "--url", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/cheap-shot"]
CMD ["serve", "--config", "/config.json"]
