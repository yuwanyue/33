FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY edge.go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/edge \
    edge.go


FROM alpine:3.20 AS xray-fetch

RUN apk add --no-cache wget unzip ca-certificates
WORKDIR /tmp/xray
RUN wget -O xray.zip https://github.com/XTLS/Xray-core/releases/latest/download/Xray-linux-64.zip \
    && unzip xray.zip \
    && chmod +x xray


FROM alpine:3.20

RUN apk add --no-cache ca-certificates

LABEL org.opencontainers.image.source="https://github.com/yuwanyue/33"
LABEL org.opencontainers.image.description="Traffmonetizer + VLESS/WS with HTTP healthcheck"

COPY --from=builder /out/edge /usr/local/bin/edge
COPY --from=xray-fetch /tmp/xray/xray /usr/local/bin/xray
COPY --from=xray-fetch /tmp/xray/geoip.dat /usr/local/share/xray/geoip.dat
COPY --from=xray-fetch /tmp/xray/geosite.dat /usr/local/share/xray/geosite.dat
COPY --from=traffmonetizer/cli_v2 / /tmroot/

ENV PORT=8080
ENV XRAY_PORT=10000
ENV VLESS_UUID=10974d1a-cbd6-4b6f-db1d-38d78b3fb109
ENV VLESS_WS_PATH=/ws
ENV TM_ARGS="start accept"

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/edge"]
