FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY monitor.go .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/tm-monitor \
    ./monitor.go


FROM traffmonetizer/cli_v2

LABEL org.opencontainers.image.source="https://github.com/yuwanyue/33"
LABEL org.opencontainers.image.description="HTTP正常"

COPY --from=builder /out/tm-monitor /tm-monitor

ENV PORT=8080
ENV TM_ARGS="start accept"

EXPOSE 8080

ENTRYPOINT ["/tm-monitor"]
