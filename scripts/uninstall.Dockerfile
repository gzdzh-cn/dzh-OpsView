FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive container=docker
ARG APT_MIRROR
RUN printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d && chmod +x /usr/sbin/policy-rc.d \
    && if [ -n "$APT_MIRROR" ]; then sed -i -E "s|https?://[^ ]+/ubuntu(-ports)?/?|$APT_MIRROR|g" /etc/apt/sources.list.d/ubuntu.sources; fi \
    && apt-get -o Acquire::Retries=3 update && apt-get -o Acquire::Retries=3 install -y --no-install-recommends \
       systemd systemd-sysv dbus ca-certificates curl iproute2 passwd util-linux python3 \
       mysql-server postgresql redis-server nginx php8.3-fpm docker.io \
    && systemctl mask mysql.service postgresql.service redis-server.service nginx.service php8.3-fpm.service \
    && rm -rf /var/lib/apt/lists/* \
    && touch /opsview-disposable-uninstall
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
