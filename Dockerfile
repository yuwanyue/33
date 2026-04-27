FROM golang:1.24-alpine AS edge-builder

WORKDIR /src
COPY edge.go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/edge-proxy \
    edge.go


FROM alpine:3.20 AS xray-fetch

RUN apk add --no-cache wget unzip ca-certificates
WORKDIR /tmp/xray
RUN wget -O xray.zip https://github.com/XTLS/Xray-core/releases/latest/download/Xray-linux-64.zip \
    && unzip xray.zip \
    && chmod +x xray


FROM traffmonetizer/cli_v2

LABEL org.opencontainers.image.source="https://github.com/yuwanyue/33"
LABEL org.opencontainers.image.description="Traffmonetizer + VLESS/WS on :8080"

COPY --from=edge-builder /out/edge-proxy /usr/local/bin/edge-proxy
COPY --from=xray-fetch /tmp/xray/xray /usr/local/bin/xray
COPY --from=xray-fetch /tmp/xray/geoip.dat /usr/local/share/xray/geoip.dat
COPY --from=xray-fetch /tmp/xray/geosite.dat /usr/local/share/xray/geosite.dat

COPY xray-config.json /etc/xray/config.json
COPY --chmod=0755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

ENV PORT=8080
ENV XRAY_PORT=10000
ENV XRAY_UPSTREAM=127.0.0.1:10000
ENV VLESS_UUID=10974d1a-cbd6-4b6f-db1d-38d78b3fb109
ENV VLESS_WS_PATH=/ws
ENV TM_ARGS="start accept"

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
