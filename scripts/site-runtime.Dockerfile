FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive container=docker
ARG APT_MIRROR
RUN if [ -n "$APT_MIRROR" ]; then sed -i -E "s|https?://[^ ]+/ubuntu(-ports)?/?|$APT_MIRROR|g" /etc/apt/sources.list.d/ubuntu.sources; fi \
    && apt-get -o Acquire::Retries=3 update \
    && apt-get -o Acquire::Retries=3 install -y --no-install-recommends \
       systemd systemd-sysv dbus ca-certificates curl iproute2 passwd util-linux python3 acl nodejs \
    && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
