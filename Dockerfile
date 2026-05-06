FROM golang:1.26-alpine AS builder
ARG PKG=github.com/ishioni/docker-external-dns
ARG VERSION=dev
ARG REVISION=dev

RUN echo 'nobody:x:65534:65534:Nobody:/:' > /tmp/passwd && \
    apk add --no-cache upx=5.0.2-r0

WORKDIR /src

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=${VERSION} -X main.Gitsha=${REVISION}" \
    ./cmd/docker-external-dns && upx --best --lzma docker-external-dns

FROM scratch
COPY --from=builder /tmp/passwd /etc/passwd
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder --chmod=555 /src/docker-external-dns /docker-external-dns

ENTRYPOINT ["/docker-external-dns"]
