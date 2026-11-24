# The infrapi-cli image GoReleaser publishes on every release
# (ghcr.io/infrapi/cli, see .goreleaser.yaml): the infrapi-cli binary it has
# just built, for each platform, on Alpine. For a CI job or a script: log in
# as a service account with the platform's token, then call the APIs. The
# contexts live in $HOME/.config/infrapi/config, the container's own unless
# you mount one (INFRAPI_CONTEXT_FILE). Not for `dev` (it runs compose on the
# host) nor `app` (it needs Go).
#
#   podman run --rm ghcr.io/infrapi/cli --version
#   podman run --rm -e KAYRA_ID_TOKEN --entrypoint sh ghcr.io/infrapi/cli -c '
#     infrapi-cli auth login --server https://kayra.example.com \
#       --service-account acme/deployer --id-token env:KAYRA_ID_TOKEN
#     infrapi-cli get vms -n acme'
#
# In a GitLab CI job, which runs a shell in the image: entrypoint: [""].
FROM docker.io/library/alpine:3.22

# ca-certificates: Kayra and the APIs over TLS
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 -h /home/infrapi infrapi

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/infrapi-cli /usr/local/bin/

USER infrapi
WORKDIR /home/infrapi
ENTRYPOINT ["infrapi-cli"]
