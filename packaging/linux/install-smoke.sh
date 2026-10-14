#!/bin/sh
# Run only in a new disposable test container, never on a service host.
set -eu
[ "${LMM_PACKAGING_ISOLATED:-}" = 1 ] || exit 2
[ -f /.dockerenv ] || [ -f /run/.containerenv ] || exit 2
format=$1
arch=$2
[ -d /packages ] || exit 2
[ "$(id -u)" -eq 0 ] || exit 2
case "$format" in
  deb)
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y --no-install-recommends ca-certificates systemd curl util-linux
    install_packages() { dpkg --force-confold --force-confdef -i /packages/*-"$1"-"$arch".deb; }
    remove_packages() { dpkg -r lmm-core lmm-extensions lmm; }
    ;;
  rpm)
    if command -v dnf >/dev/null; then
      dnf install -y ca-certificates systemd curl-minimal util-linux
    else
      zypper --non-interactive install ca-certificates systemd curl util-linux
    fi
    install_packages() { rpm -Uvh /packages/*-"$1"-"$arch".rpm; }
    remove_packages() { rpm -e lmm-core lmm-extensions lmm; }
    ;;
  apk)
    apk add --no-cache ca-certificates openrc curl su-exec
    # Unsigned previews are permitted only inside this disposable test container.
    install_packages() { apk add --allow-untrusted /packages/*-"$1"-"$arch".apk; }
    remove_packages() { apk del lmm-core lmm-extensions lmm; }
    ;;
  archlinux)
    pacman -Syu --noconfirm --needed ca-certificates systemd curl util-linux
    install_packages() { pacman -U --noconfirm /packages/*-"$1"-"$arch".pkg.tar.zst; }
    remove_packages() { pacman -R --noconfirm lmm-core lmm-extensions lmm; }
    ;;
  *) exit 2 ;;
esac
assert_not_activated() {
    for port in 8080 8081; do
        if curl --silent --fail --max-time 1 "http://127.0.0.1:$port/health/live"; then exit 1; fi
    done
}
install_packages 1
assert_not_activated
# Provision the shipped static accounts explicitly, not through installation hooks.
# Alpine's preinstall hook already creates its own non-login accounts.
if [ "$format" != apk ]; then
    systemd-sysusers /usr/lib/sysusers.d/lmm-core.conf /usr/lib/sysusers.d/lmm-extensions.conf
fi
core_uid=$(id -u lmm-core)
core_gid=$(id -g lmm-core)
ext_uid=$(id -u lmm-extensions)
ext_gid=$(id -g lmm-extensions)
[ "$core_uid" -ne 0 ] && [ "$ext_uid" -ne 0 ] && [ "$core_uid" -ne "$ext_uid" ]
[ "$core_gid" -ne 0 ] && [ "$ext_gid" -ne 0 ] && [ "$core_gid" -ne "$ext_gid" ]
# Call only in a subshell: exec keeps $! equal to the installed program's PID.
run_service() {
    service=$1
    shift
    uid=$(id -u "$service")
    gid=$(id -g "$service")
    case "$format" in
        apk) exec su-exec "$uid:$gid" "$@" ;;
        *) exec setpriv --reuid="$uid" --regid="$gid" --clear-groups --no-new-privs "$@" ;;
    esac
}
for program in lmm lmm-core lmm-extensions; do
    [ -x "/usr/bin/$program" ]
done
for retired in lmm-core-admin lmm-api-deploy lmm-api-go-deploy; do
    [ ! -e "/usr/bin/$retired" ]
done
/usr/bin/lmm --version
(run_service lmm-core /usr/bin/lmm-core --version)
# Installation must not start the daemons, modify a database or need Go/Docker.
printf '\n# operator-setting-survives\n' >> /etc/lmm.conf.d/core.toml
mkdir -p /var/lib/lmm/core /etc/lmm.conf.d/secrets/core
chmod 0700 /etc/lmm.conf.d/secrets/core
printf 'operator-data\n' > /var/lib/lmm/core/keep
printf 'synthetic-preservation-fixture\n' > /etc/lmm.conf.d/secrets/core/keep
chmod 0600 /etc/lmm.conf.d/secrets/core/keep
install_packages 2
assert_not_activated
grep -q operator-setting-survives /etc/lmm.conf.d/core.toml
[ "$(cat /var/lib/lmm/core/keep)" = operator-data ]
[ "$(cat /etc/lmm.conf.d/secrets/core/keep)" = synthetic-preservation-fixture ]
if [ "$format" != apk ]; then
    systemd-analyze verify /usr/lib/systemd/system/lmm-core.service /usr/lib/systemd/system/lmm-extensions.service
fi
# This tests installed processes under service UIDs, not service-manager startup
# or its sandbox. No DB, external upstream or funds are used.
smoke_dir=$(mktemp -d /tmp/lmm-package-smoke.XXXXXX)
chmod 0755 "$smoke_dir"
for component in core extensions; do
    mkdir "$smoke_dir/$component"
    chown "lmm-$component:lmm-$component" "$smoke_dir/$component"
    chmod 0700 "$smoke_dir/$component"
done
printf '0123456789abcdef0123456789abcdef\n' > "$smoke_dir/core/token"
printf 'abcdef0123456789abcdef0123456789\n' > "$smoke_dir/extensions/token"
# Leave both secrets root-owned initially, while their parent is traversable
# by the intended service. Failure must be due to the credential, not the TOML.
chown root:root "$smoke_dir/core/token" "$smoke_dir/extensions/token"
chmod 0600 "$smoke_dir/core/token" "$smoke_dir/extensions/token"
write_core_config() {
    cat > "$smoke_dir/core.toml" <<TOML
version = 1
[server]
listen = "127.0.0.1:18080"
[rpc]
socket = "$smoke_dir/core/core.sock"
token_file = "$1"
TOML
    chmod 0644 "$smoke_dir/core.toml"
}
write_extensions_config() {
    cat > "$smoke_dir/extensions.toml" <<TOML
version = 1
[host]
listen = "127.0.0.1:18081"
token_file = "$1"
modules = ["none"]
web_compat = false
TOML
    chmod 0644 "$smoke_dir/extensions.toml"
}
write_core_config "$smoke_dir/core/token"
write_extensions_config "$smoke_dir/extensions/token"
service_pid=
stop_service() {
    [ -n "$service_pid" ] || return 0
    stopped_pid=$service_pid
    service_pid=
    kill -TERM "$stopped_pid" 2>/dev/null || true
    status=0
    wait "$stopped_pid" || status=$?
    # Some programs exit through the default TERM handler; both outcomes reap
    # the real child. Other failures must not be hidden by successful cleanup.
    [ "$status" -eq 0 ] || [ "$status" -eq 143 ] || return "$status"
    if kill -0 "$stopped_pid" 2>/dev/null; then return 1; fi
}
trap 'stop_service' EXIT
trap 'exit 1' HUP INT TERM
start_service() {
    run_service "lmm-$component" "/usr/bin/lmm-$component" --config "$smoke_dir/$component.toml" >"$smoke_dir/$component-$stage.log" 2>&1 &
    service_pid=$!
}
assert_service_identity() {
    uid=$(id -u "lmm-$component")
    gid=$(id -g "lmm-$component")
    # Real/effective/saved/fs IDs must all match, with no inherited root groups.
    awk -v uid="$uid" -v gid="$gid" '
        /^Uid:/ { if ($2 != uid || $3 != uid || $4 != uid || $5 != uid) exit 1; u=1 }
        /^Gid:/ { if ($2 != gid || $3 != gid || $4 != gid || $5 != gid) exit 1; g=1 }
        /^Groups:/ { for (i=2; i<=NF; i++) if ($i != gid) exit 1 }
        END { if (!u || !g) exit 1 }
    ' "/proc/$service_pid/status"
    [ "$(run_service "lmm-$component" readlink "/proc/$service_pid/exe")" = "/usr/bin/lmm-$component" ]
}
expect_credential_rejection() {
    start_service
    for _ in 1 2 3 4 5 6 7 8 9 10; do
        if curl --silent --fail --max-time 1 "http://127.0.0.1:$port/health/live" >/dev/null; then
            echo "FAIL: $component accepted $stage credential" >&2
            return 1
        fi
        kill -0 "$service_pid" 2>/dev/null || break
        sleep 1
    done
    if kill -0 "$service_pid" 2>/dev/null; then
        echo "FAIL: $component did not reject $stage credential before bind" >&2
        return 1
    fi
    status=0
    wait "$service_pid" || status=$?
    service_pid=
    [ "$status" -ne 0 ]
    case "$component" in
        core) grep -Eq 'core (configuration file|path) (unavailable|must be private)' "$smoke_dir/$component-$stage.log" ;;
        extensions) grep -q 'private credential file unavailable' "$smoke_dir/$component-$stage.log" ;;
    esac
    printf 'PASS: %s rejects %s before bind\n' "$component" "$stage"
}
for component in core extensions; do
    case "$component" in core) port=18080 ;; extensions) port=18081 ;; esac
    stage=root-owned-0600
    expect_credential_rejection
    chown "lmm-$component:lmm-$component" "$smoke_dir/$component/token"
    stage='service-owned-0600'
    start_service
    ready=0
    for _ in 1 2 3 4 5 6 7 8 9 10; do
        if curl --silent --fail --max-time 1 "http://127.0.0.1:$port/health/live" >"$smoke_dir/$component-live.json"; then ready=1; break; fi
        kill -0 "$service_pid" 2>/dev/null || break
        sleep 1
    done
    [ "$ready" = 1 ] || { cat "$smoke_dir/$component-$stage.log"; exit 1; }
    assert_service_identity
    if [ "$component" = core ]; then
        # RPC reads a real private credential without provisioning a database.
        # Business readiness must remain 503, not become a fake success.
        [ "$(curl --silent --max-time 1 -o "$smoke_dir/ready.json" -w '%{http_code}' http://127.0.0.1:18080/health/ready)" = 503 ]
    fi
    printf 'PASS: %s service-owned-0600 live, UID=%s GID=%s PID=%s\n' "$component" "$uid" "$gid" "$service_pid"
    stop_service
    stage='service-owned-0640'
    chmod 0640 "$smoke_dir/$component/token"
    expect_credential_rejection
    chmod 0600 "$smoke_dir/$component/token"
done
# Both directions must fail at the OS boundary and at product configuration load.
if (run_service lmm-core cat "$smoke_dir/extensions/token") >/dev/null 2>&1; then exit 1; fi
if (run_service lmm-extensions cat "$smoke_dir/core/token") >/dev/null 2>&1; then exit 1; fi
write_core_config "$smoke_dir/extensions/token"
write_extensions_config "$smoke_dir/core/token"
for component in core extensions; do
    case "$component" in core) port=18080 ;; extensions) port=18081 ;; esac
    stage=cross-service-0600
    expect_credential_rejection
done
remove_packages
assert_not_activated
[ "$(cat /var/lib/lmm/core/keep)" = operator-data ]
[ "$(cat /etc/lmm.conf.d/secrets/core/keep)" = synthetic-preservation-fixture ]
printf 'PASS: %s/%s install, upgrade, preservation, service-UID processes, private credentials, removal (not service-manager acceptance)\n' "$format" "$arch"
