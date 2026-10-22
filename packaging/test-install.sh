#!/bin/sh
# Installs a package in a clean container, starts LANLeecher with the packaged
# configuration, checks /health and removes the package again.
#
#   sh packaging/test-install.sh deb|rpm|archlinux|apk PACKAGE_FILE
set -eu

format=$1
pkg=$2

fail() {
    echo "FAIL: $*" >&2
    [ -f /tmp/lanleecher.log ] && cat /tmp/lanleecher.log >&2
    exit 1
}

echo "== install $pkg"
case "$format" in
    deb) apt-get install -y "$pkg" ;;
    rpm) if command -v dnf >/dev/null; then dnf install -y "$pkg"; else rpm -i "$pkg"; fi ;;
    archlinux) pacman -U --noconfirm "$pkg" ;;
    apk) apk add --allow-untrusted "$pkg" ;;
    *) fail "unknown format $format" ;;
esac

echo "== installed files"
lanleecher version
id lanleecher || fail "user lanleecher missing"
[ -f /etc/lanleecher/config.yaml ] || fail "config missing"
[ -s /etc/lanleecher/secret.key ] || fail "secret.key missing"
[ "$(stat -c %a /etc/lanleecher/secret.key)" = 640 ] || fail "secret.key mode is not 640"
[ "$(stat -c %U /var/lib/lanleecher)" = lanleecher ] || fail "/var/lib/lanleecher not owned by lanleecher"
[ -f /usr/lib/systemd/system/lanleecher.service ] || fail "systemd unit missing"

echo "== admin user (as root, files must stay usable for the service)"
echo "smoke-test-password" | lanleecher -c /etc/lanleecher/config.yaml user create admin --role admin
[ "$(stat -c %U /var/lib/lanleecher/lanleecher.db)" = lanleecher ] || fail "database owned by root after CLI use"

echo "== start server as lanleecher"
su -s /bin/sh lanleecher -c 'LANLEECHER_CONFIG=/etc/lanleecher/config.yaml /usr/bin/lanleecher server' \
    >/tmp/lanleecher.log 2>&1 &
pid=$!
ok=
for _ in $(seq 1 40); do
    if out=$(wget -q -O - http://127.0.0.1:9040/health 2>/dev/null || curl -fsS http://127.0.0.1:9040/health 2>/dev/null); then
        [ "$out" = ok ] && ok=1 && break
    fi
    sleep 0.5
done
[ -n "$ok" ] || fail "server did not become healthy"
code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:9040/login 2>/dev/null || echo 200)
[ "$code" = 200 ] || fail "/login returned $code"
kill "$pid"
wait "$pid" 2>/dev/null || true

echo "== remove"
case "$format" in
    deb) apt-get remove -y lanleecher ;;
    rpm) if command -v dnf >/dev/null; then dnf remove -y lanleecher; else rpm -e lanleecher; fi ;;
    archlinux) pacman -R --noconfirm lanleecher ;;
    apk) apk del lanleecher ;;
esac
# Check the file, not `command -v`: the shell has cached the path since
# `lanleecher version` above and would still report the removed binary.
[ ! -e /usr/bin/lanleecher ] || fail "binary still installed"
[ -f /var/lib/lanleecher/lanleecher.db ] || fail "data was deleted on removal"
echo "OK: $pkg"
