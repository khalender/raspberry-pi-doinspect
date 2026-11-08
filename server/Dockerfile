# doinspect: static Go binary on scratch, linux/arm64 for the Pi's Docker VM.
# Build from the repository root: docker buildx build --platform linux/arm64 --build-arg VERSION=0.2.2 -f server/Dockerfile -t doinspect:0.2.2 --load .
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS=linux TARGETARCH=arm64 VERSION=0.2.2
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/doinspect ./cmd/doinspect \
 && mkdir -p /out/data

FROM scratch
COPY --from=build /out/doinspect /doinspect
# CA certificates for HTTPS to the speed test and RIPEstat (T05)
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=10001:10001 /out/data /data
USER 10001:10001
ENV DOINSPECT_LISTEN=:8080 DOINSPECT_DATA=/data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD ["/doinspect", "healthcheck"]
ENTRYPOINT ["/doinspect"]
