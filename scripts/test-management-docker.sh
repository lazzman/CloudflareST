#!/bin/sh
set -eu

image=${1:-cloudflarest-management:r3-acceptance}
suffix="$(date +%s)-$$"
volume="cfst-r3-test-$suffix"
container="cfst-r3-test-$suffix"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

docker volume create "$volume" >/dev/null
docker run -d --name "$container" --network none --read-only \
  --cap-drop ALL --cap-add NET_RAW --security-opt no-new-privileges:true \
  --mount "type=volume,src=$volume,dst=/var/lib/cfst" \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m,uid=10001,gid=10001 "$image" >/dev/null

healthy() {
  count=0
  until docker exec "$container" wget -q -O /dev/null http://127.0.0.1:8080/healthz; do
    count=$((count + 1))
    if [ "$count" -ge 20 ]; then
      echo '容器未在20秒内就绪，检查镜像与数据卷权限。' >&2
      return 1
    fi
    sleep 1
  done
}

healthy
docker exec "$container" sh -eu -c '
  test "$(id -u)" = 10001
  test "$(stat -c %a /tmp/cfst)" = 700
  test "$(stat -c %a /tmp/cfst/admin-password)" = 600
  test "$(stat -c %a /var/lib/cfst/master.key)" = 600
  test ! -e /var/lib/cfst/admin-password
  wget -q -O /tmp/page http://127.0.0.1:8080/
  grep -q "assets/" /tmp/page
  if wget -q -O /tmp/unauthorized http://127.0.0.1:8080/api/v1/tasks; then exit 1; fi
  test -s /var/lib/cfst/management.sqlite
'
password_before=$(docker exec "$container" sha256sum /tmp/cfst/admin-password)
key_before=$(docker exec "$container" sha256sum /var/lib/cfst/master.key)
docker restart "$container" >/dev/null
healthy
password_after=$(docker exec "$container" sha256sum /tmp/cfst/admin-password)
key_after=$(docker exec "$container" sha256sum /var/lib/cfst/master.key)
test "$password_before" != "$password_after"
test "$key_before" = "$key_after"
echo '通过：断网容器、非root、只读根文件系统、嵌入静态页面、未认证拦截、随机密码权限与重启轮换、卷主密钥保留。'
