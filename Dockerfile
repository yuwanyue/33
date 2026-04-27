FROM traffmonetizer/cli_v2

LABEL org.opencontainers.image.source="https://github.com/yuwanyue/33"
LABEL org.opencontainers.image.description="Traffmonetizer CLI image for VM or Docker runtime"

COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENV TM_ARGS="start accept"

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
