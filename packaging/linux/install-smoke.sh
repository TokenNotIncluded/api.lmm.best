#!/bin/sh
# Run only in a new disposable test container, never on a service host.
set -eu
[ "${LMM_PACKAGING_ISOLATED:-}" = 1 ] || exit 2
[ -f /.dockerenv ] || [ -f /run/.containerenv ] || exit 2
format=$1
arch=$2
[ -d /packages ] || exit 2
case "$format" in
  deb)
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y --no-install-recommends ca-certificates systemd curl
    install_packages() { dpkg --force-confold --force-confdef -i /packages/*-"$1"-"$arch".deb; }
    remove_packages() { dpkg -r lmm-core lmm-extensions lmm; }
    ;;
  rpm)
    if command -v dnf >/dev/null; then
      dnf install -y ca-certificates systemd curl-minimal
    else
      zypper --non-interactive install ca-certificates systemd curl
    fi
    install_packages() { rpm -Uvh /packages/*-"$1"-"$arch".rpm; }
    remove_packages() { rpm -e lmm-core lmm-extensions lmm; }
    ;;
  apk)
    apk add --no-cache ca-certificates openrc curl
    # Unsigned previews are permitted only inside this disposable test container.
    install_packages() { apk add --allow-untrusted /packages/*-"$1"-"$arch".apk; }
    remove_packages() { apk del lmm-core lmm-extensions lmm; }
    ;;
  archlinux)
    pacman -Syu --noconfirm --needed ca-certificates systemd curl
    install_packages() { pacman -U --noconfirm /packages/*-"$1"-"$arch".pkg.tar.zst; }
    remove_packages() { pacman -R --noconfirm lmm-core lmm-extensions lmm; }
    ;;
  *) exit 2 ;;
esac
install_packages 1
for program in lmm lmm-core lmm-extensions; do
    [ -x "/usr/bin/$program" ]
done
for retired in lmm-core-admin lmm-api-deploy lmm-api-go-deploy; do
    [ ! -e "/usr/bin/$retired" ]
done
/usr/bin/lmm --version
/usr/bin/lmm-core --version
# Installation must not start the daemons, modify a database or need Go/Docker.
if curl --silent --fail --max-time 1 http://127.0.0.1:8080/health/live; then exit 1; fi
printf '\n# operator-setting-survives\n' >> /etc/lmm.conf.d/core.toml
mkdir -p /var/lib/lmm/core /etc/lmm.conf.d/secrets/core
chmod 0700 /etc/lmm.conf.d/secrets/core
printf 'operator-data\n' > /var/lib/lmm/core/keep
printf 'synthetic-preservation-fixture\n' > /etc/lmm.conf.d/secrets/core/keep
chmod 0600 /etc/lmm.conf.d/secrets/core/keep
install_packages 2
grep -q operator-setting-survives /etc/lmm.conf.d/core.toml
[ "$(cat /var/lib/lmm/core/keep)" = operator-data ]
[ "$(cat /etc/lmm.conf.d/secrets/core/keep)" = synthetic-preservation-fixture ]
if [ "$format" != apk ]; then
    systemd-analyze verify /usr/lib/systemd/system/lmm-core.service /usr/lib/systemd/system/lmm-extensions.service
fi
# Exercise installed product executables without a DB, external upstream or funds.
# Core's business readiness must stay 503, not become a fake success.
smoke_dir=$(mktemp -d /tmp/lmm-package-smoke.XXXXXX)
chmod 0700 "$smoke_dir"
cat > "$smoke_dir/core.toml" <<'TOML'
version = 1
[server]
listen = "127.0.0.1:18080"
TOML
chmod 0600 "$smoke_dir/core.toml"
/usr/bin/lmm-core --config "$smoke_dir/core.toml" >/tmp/lmm-core.log 2>&1 &
core_pid=$!
trap 'kill "$core_pid" 2>/dev/null || true' EXIT HUP INT TERM
ready=0
for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl --silent --fail http://127.0.0.1:18080/health/live >/tmp/live.json; then ready=1; break; fi
    sleep 1
done
[ "$ready" = 1 ] || { cat /tmp/lmm-core.log; exit 1; }
[ "$(curl --silent -o /tmp/ready.json -w '%{http_code}' http://127.0.0.1:18080/health/ready)" = 503 ]
kill "$core_pid"
wait "$core_pid"
trap - EXIT HUP INT TERM
printf '0123456789abcdef0123456789abcdef\n' > "$smoke_dir/extension-token"
chmod 0600 "$smoke_dir/extension-token"
cat > "$smoke_dir/extensions.toml" <<TOML
version = 1
[host]
listen = "127.0.0.1:18081"
token_file = "$smoke_dir/extension-token"
modules = ["none"]
web_compat = false
TOML
chmod 0600 "$smoke_dir/extensions.toml"
/usr/bin/lmm-extensions --config "$smoke_dir/extensions.toml" >/tmp/lmm-extensions.log 2>&1 &
ext_pid=$!
trap 'kill "$ext_pid" 2>/dev/null || true' EXIT HUP INT TERM
ready=0
for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl --silent --fail http://127.0.0.1:18081/health/live >/tmp/extension-live.json; then ready=1; break; fi
    sleep 1
done
[ "$ready" = 1 ] || { cat /tmp/lmm-extensions.log; exit 1; }
kill "$ext_pid"
wait "$ext_pid"
trap - EXIT HUP INT TERM
remove_packages
[ "$(cat /var/lib/lmm/core/keep)" = operator-data ]
[ "$(cat /etc/lmm.conf.d/secrets/core/keep)" = synthetic-preservation-fixture ]
printf 'PASS: %s/%s install, upgrade, config preservation, product process, removal\n' "$format" "$arch"
