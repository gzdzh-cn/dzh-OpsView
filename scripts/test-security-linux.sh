#!/usr/bin/env bash
set -euo pipefail
mkdir -p dist/tests
(cd server && GOOS=linux GOARCH=amd64 go test -c -o ../dist/tests/security.test ./internal/executor)
cert_mount=()
for cert_path in /etc/ssl/certs/ca-certificates.crt /etc/ssl/cert.pem; do
  if [ -f "$cert_path" ]; then
    cert_mount=(-v "$cert_path:/etc/ssl/certs/ca-certificates.crt:ro")
    break
  fi
done
# No host network, host system directories or Docker socket are mounted. NET_ADMIN
# applies solely to this disposable container's network namespace.
docker run --rm --platform linux/amd64 --cap-add NET_ADMIN \
  "${cert_mount[@]}" \
  -v "$PWD/dist/tests/security.test:/tmp/security.test:ro" \
  -e OPSVIEW_SECURITY_SMOKE=true -e OPSVIEW_FIREWALL_SMOKE=true \
  debian:bookworm-slim sh -ec '
    sed -i "s@http://@https://@g" /etc/apt/sources.list.d/debian.sources
    apt-get -o Acquire::Retries=3 update -qq
    DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::Retries=3 install -y -qq --no-install-recommends openssh-server ufw iproute2 > /dev/null
    mkdir -p /run/sshd
    echo "root:OpsView-disposable-container-test-only!" | chpasswd
    /tmp/security.test -test.run "^TestLinuxSecuritySSHAndFirewall$" -test.v -test.timeout=3m
  '
