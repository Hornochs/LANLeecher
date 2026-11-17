#!/bin/sh
# Installs the agent package in a clean container, checks files, user and
# permissions, runs "lanleecher-agent test" against a closed port (collection
# must work, sending must fail cleanly) and removes the package again.
#
#   sh packaging/test-install-agent.sh deb|rpm|archlinux|apk PACKAGE_FILE
set -eu

format=$1
pkg=$2

fail() {
    echo "FAIL: $*" >&2
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
lanleecher-agent version
id lanleecher-agent || fail "user lanleecher-agent missing"
[ -f /etc/lanleecher/agent.conf ] || fail "agent.conf missing"
[ "$(stat -c %a /etc/lanleecher/agent.conf)" = 640 ] || fail "agent.conf mode is not 640"
[ "$(stat -c %G /etc/lanleecher/agent.conf)" = lanleecher-agent ] || fail "agent.conf group is not lanleecher-agent"
[ -f /usr/lib/systemd/system/lanleecher-agent.service ] || fail "systemd unit missing"

echo "== the packaged placeholder configuration is refused"
if lanleecher-agent test >/tmp/agent.out 2>&1; then
    fail "placeholder configuration accepted"
fi
grep -q token /tmp/agent.out || fail "no token hint: $(cat /tmp/agent.out)"

echo "== plain http is refused without opt-in"
printf 'server: http://127.0.0.1:9\ntoken: llk_0123456789abcdefghijABCDEFGHIJ0123456789\n' > /tmp/agent-http.conf
if lanleecher-agent -c /tmp/agent-http.conf test >/tmp/agent.out 2>&1; then
    fail "http:// accepted"
fi
grep -q 'clear text' /tmp/agent.out || fail "no clear-text hint: $(cat /tmp/agent.out)"

echo "== collection works, sending to a closed port fails cleanly"
printf 'allow_insecure_http: true\n' >> /tmp/agent-http.conf
if lanleecher-agent -c /tmp/agent-http.conf test >/tmp/agent.out 2>&1; then
    fail "sending to a closed port succeeded"
fi
grep -q '^Mode: linux' /tmp/agent.out || fail "collection failed: $(cat /tmp/agent.out)"

echo "== remove"
case "$format" in
    deb) apt-get remove -y lanleecher-agent ;;
    rpm) if command -v dnf >/dev/null; then dnf remove -y lanleecher-agent; else rpm -e lanleecher-agent; fi ;;
    archlinux) pacman -R --noconfirm lanleecher-agent ;;
    apk) apk del lanleecher-agent ;;
esac
[ ! -e /usr/bin/lanleecher-agent ] || fail "binary still installed"
echo "OK: $pkg"
