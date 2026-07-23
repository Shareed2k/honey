# syntax=docker/dockerfile:1
# Base image that embeds honey-plugin-init as its entrypoint. Plugin authors
# do: COPY --from=ghcr.io/shareed2k/honey-plugin-init:<ver> \
#        /usr/local/bin/honey-plugin-init /usr/local/bin/honey-plugin-init
FROM scratch
ARG TARGETARCH
COPY build/honey-plugin-init-linux-${TARGETARCH} /usr/local/bin/honey-plugin-init
ENTRYPOINT ["/usr/local/bin/honey-plugin-init"]
