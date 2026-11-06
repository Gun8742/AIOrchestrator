FROM alpine:3.22 AS download

RUN apk add --no-cache ca-certificates curl
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      arm64) platform=linux-arm-musl; archive=cli_linux_arm64_musl.tar.gz; checksum=24a1ac175251ebe8d90dcb05c98450f5ceeed361036a679cc0b56670fdf74dae97267ae9bb605765e0b4d2ee348239d0443836da6713be28f838e6853a328a17 ;; \
      amd64) platform=linux-x64-musl; archive=cli_linux_x64_musl.tar.gz; checksum=8cb3308356d776eb21ccdad8002162bb63a48b75a0861414fcadd9f3af60402c27ec61085f25b0b118cf029f316cc9aefdb4103ebf1053efbd92cceaa791291e ;; \
      *) echo 'Unsupported architecture' >&2; exit 1 ;; \
    esac && \
    curl --fail --silent --show-error --location "https://storage.googleapis.com/antigravity-public/antigravity-cli/1.4.9-4962164969177088/$platform/$archive" -o /tmp/agy.tar.gz && \
    echo "$checksum  /tmp/agy.tar.gz" | sha512sum -c - && \
    tar -xzf /tmp/agy.tar.gz -C /tmp antigravity

FROM alpine:3.22
LABEL io.pixel-agent-office.auth="google-oauth-no-overage-v1"
RUN apk add --no-cache ca-certificates && adduser -D -u 1000 agent && \
    mkdir -p /home/agent/.gemini && chown agent:agent /home/agent/.gemini && chmod 700 /home/agent/.gemini
COPY --from=download --chmod=755 /tmp/antigravity /usr/local/bin/agy
COPY --chmod=755 antigravity-entrypoint.sh /usr/local/bin/antigravity-entrypoint
USER agent
ENV HOME=/home/agent
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/antigravity-entrypoint"]
