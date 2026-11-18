ARG BASE_IMAGE=debian:bookworm-slim
FROM ${BASE_IMAGE}
COPY host-ca.pem /tmp/opsview-host-ca.pem
RUN mkdir -p /etc/ssl/certs /usr/local/share/ca-certificates && \
    cp /tmp/opsview-host-ca.pem /etc/ssl/certs/ca-certificates.crt && \
    cp /tmp/opsview-host-ca.pem /usr/local/share/ca-certificates/opsview-test-host.crt && \
    find /etc/apt -type f \( -name '*.list' -o -name '*.sources' \) -exec sed -i 's@http://@https://@g' {} + && \
    apt-get -o Acquire::Retries=3 update -qq && \
    DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::Retries=3 install -y -qq --no-install-recommends \
      ca-certificates ufw firewalld ipset dbus iproute2 fail2ban python3-systemd openssh-server iputils-ping && \
    mkdir -p /run/sshd && \
    rm -rf /var/lib/apt/lists/*
COPY firewall.test /tmp/firewall.test
COPY firewall-peer.py /tmp/firewall-peer.py
CMD ["sleep", "infinity"]
