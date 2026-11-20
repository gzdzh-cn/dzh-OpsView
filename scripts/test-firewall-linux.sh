#!/usr/bin/env bash
set -euo pipefail
arch="${OPSVIEW_TEST_ARCH:-amd64}"
backend_list="${OPSVIEW_TEST_FIREWALL_BACKENDS:-ufw firewalld}"
[ "$#" -gt 0 ] || set -- ubuntu:22.04 ubuntu:24.04 debian:bookworm-slim debian:trixie-slim
fixture="$PWD/dist/tests/firewall-matrix"
mkdir -p "$fixture"
(cd server && GOOS=linux GOARCH="$arch" go test -c -o "$fixture/firewall.test" ./internal/executor)
cp scripts/firewall-peer.py "$fixture/"
for cert_path in /etc/ssl/certs/ca-certificates.crt /etc/ssl/cert.pem; do
  if [ -f "$cert_path" ]; then cp "$cert_path" "$fixture/host-ca.pem"; break; fi
done
[ -f "$fixture/host-ca.pem" ] || { echo 'Missing HTTPS certificate bundle' >&2; exit 1; }
prefix="opsview-fw-test-$$"
network="$prefix-net"
server="$prefix-server"
peer="$prefix-peer"
images=()
cleanup() {
  docker rm -f "$server" "$peer" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  for image in "${images[@]}"; do docker rmi "$image" >/dev/null 2>&1 || true; done
}
trap cleanup EXIT
# Dedicated bridge and network namespaces. No host-network, host system paths,
# privileged mode, or Docker socket is exposed to a test container.
v6subnet="fd77:0:$(printf '%x' "$(( $$ & 65535 ))")::/64"
docker network create --ipv6 --subnet "$v6subnet" "$network" >/dev/null
index=0
for base in "$@"; do
  index=$((index+1))
  image="$prefix-fixture:$index"
  images+=("$image")
  docker build --platform "linux/$arch" --build-arg "BASE_IMAGE=$base" -t "$image" -f scripts/firewall.Dockerfile "$fixture" > "$fixture/build-$index.log" 2>&1 || { tail -80 "$fixture/build-$index.log"; exit 1; }
  if [ "$index" = 1 ]; then
    docker run -d --platform "linux/$arch" --network "$network" --name "$peer" "$image" python3 /tmp/firewall-peer.py >/dev/null
  fi
  peer_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$peer")
  for backend in $backend_list; do
    echo "Firewall acceptance: $base / $backend / $arch"
    docker run -d --platform "linux/$arch" --network "$network" --cap-add NET_ADMIN --sysctl net.ipv4.ip_forward=1 --name "$server" "$image" >/dev/null
    # A user bridge injects namespace-local DNS NAT chains. The fixtures use only
    # numeric peer addresses after startup; remove only those known test DNS
    # objects before firewalld initialization. Product code still rejects them.
    docker exec "$server" python3 -c '
import subprocess, shlex
for binary in ("iptables", "ip6tables"):
    data = subprocess.check_output([binary + "-save", "-t", "nat"], text=True)
    owned = {"DOCKER_OUTPUT", "DOCKER_POSTROUTING"}
    for line in data.splitlines():
        args = shlex.split(line)
        if args and args[0] == "-A" and "-j" in args and args[args.index("-j")+1] in owned:
            subprocess.run([binary, "-t", "nat", "-D"] + args[1:], check=True)
    for chain in owned:
        if ":" + chain + " " in data:
            subprocess.run([binary, "-t", "nat", "-F", chain], check=True)
            subprocess.run([binary, "-t", "nat", "-X", chain], check=True)
'
    if [ "$backend" = firewalld ]; then
      docker exec "$server" sh -ec 'mkdir -p /run/dbus; dbus-daemon --system --fork; /usr/sbin/firewalld --nofork --nopid > /tmp/firewalld.log 2>&1 &' 
      ready=false
      for attempt in $(seq 1 30); do
        if docker exec "$server" firewall-cmd --state >/dev/null 2>&1; then ready=true; break; fi
        sleep 1
      done
      [ "$ready" = true ] || { docker exec "$server" cat /tmp/firewalld.log; exit 1; }
    fi
    server_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$server")
    server_v6=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.GlobalIPv6Address}}{{end}}' "$server")
    if ! docker exec -e OPSVIEW_FIREWALL_ADMIN_SMOKE="$backend" -e OPSVIEW_FIREWALL_PEER="http://$peer_ip:18900" -e OPSVIEW_FIREWALL_PEER_IP="$peer_ip" -e OPSVIEW_FIREWALL_SERVER_IP="$server_ip" -e OPSVIEW_FIREWALL_SERVER_IPV6="$server_v6" "$server" /tmp/firewall.test -test.run '^TestLinuxFirewallAdmin$' -test.v -test.timeout=8m; then
      docker exec "$server" firewall-cmd --list-all-zones > "$fixture/runtime-$index.txt" 2>&1 || true
      docker exec "$server" firewall-cmd --permanent --list-all-zones > "$fixture/permanent-$index.txt" 2>&1 || true
      diff -u "$fixture/permanent-$index.txt" "$fixture/runtime-$index.txt" || true
      exit 1
    fi
    docker rm -f "$server" >/dev/null
  done
done
