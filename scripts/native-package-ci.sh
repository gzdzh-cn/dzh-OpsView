#!/usr/bin/env bash
set -euo pipefail
: "${NATIVE_IMAGE:?}" "${NATIVE_ENTRY:?}" "${PACKAGE_MODE:?}"
image_name="opsview-native-${PACKAGE_MODE}"
container_name="opsview-native-${PACKAGE_MODE}"
docker build --build-arg "BASE=$NATIVE_IMAGE" -t "$image_name" -f .github/native/Dockerfile .github/native
trap 'docker rm -f "$container_name" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$container_name" --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw -v "$PWD/dist/native-smoke.test:/native-smoke.test:ro" -v "$PWD/dist/native:/output" "$image_name"
for attempt in $(seq 1 40); do
  state="$(docker exec "$container_name" systemctl is-system-running 2>/dev/null || true)"
  if [ "$state" = running ] || [ "$state" = degraded ]; then break; fi
  sleep 1
done
[ "$state" = running ] || [ "$state" = degraded ] || { docker logs "$container_name"; exit 1; }
set +e
docker exec -e GITHUB_ACTIONS=true -e "OPSVIEW_PACKAGE_MODE=$PACKAGE_MODE" -e "OPSVIEW_NATIVE_ENTRY=$NATIVE_ENTRY" -e OPSVIEW_NATIVE_OUTPUT=/output "$container_name" /native-smoke.test -test.run '^TestFastPackageAcceptance$' -test.v -test.timeout=85m 2>&1 | tee dist/native-acceptance.log
result=${PIPESTATUS[0]}
set -e
if [ "$result" -ne 0 ]; then
  python3 - <<'REPORT'
import pathlib
text='\n'.join(pathlib.Path('dist/native-acceptance.log').read_text(errors='replace').splitlines()[-14:])[-3000:]
text=text.replace('%','%25').replace('\r','%0D').replace('\n','%0A')
print(f'::error title=Native acceptance failed::{text}')
REPORT
  exit "$result"
fi
