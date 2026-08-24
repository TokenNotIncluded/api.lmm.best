#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
here=$repo/deploy/production
: "${TMPDIR:?set TMPDIR to a private persistent test workspace}"
[[ -d $TMPDIR && -w $TMPDIR && ! -L $TMPDIR ]] || {
  printf 'go-package-roundtrip: TMPDIR is not a safe writable directory\n' >&2
  exit 1
}

for command in cc fakeroot makepkg pacman stat tar; do
  command -v "$command" >/dev/null 2>&1 || {
    printf 'go-package-roundtrip: required command is unavailable: %s\n' "$command" >&2
    exit 1
  }
done

tmp=$(mktemp -d "$TMPDIR/lmm-go-package-roundtrip.XXXXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
workspace=$tmp/workspace
payload_root=$tmp/payload-root
rollback_dir=$tmp/rollback
candidate_dir=$tmp/candidate
frontend=$tmp/frontend
pacman_root=$tmp/pacman-root
install -d -m0700 "$workspace/tmp" "$payload_root/metadata" \
  "$payload_root/core-root/etc/lmm-api" \
  "$rollback_dir" "$candidate_dir" "$frontend"
install -d -m0755 "$payload_root/core-root/usr/bin" \
  "$payload_root/core-root/usr/lib/systemd/system" \
  "$payload_root/core-root/usr/share/licenses/lmm-api" \
  "$payload_root/go-root/usr/lib/lmm-api/backends/go"
printf 'format=1\nrole=test\n' >"$workspace/.lmm-deploy-workspace"

old_core_version=0.1.0.r31.gfixture-1
old_go_version=0.1.0.r122.gfixture-1
candidate_version=0.1.0.r999.gfixture
printf 'lmm-api\t%s\n' "$old_core_version" >"$payload_root/metadata/packages.tsv"
printf 'lmm-api-go\t%s\n' "$old_go_version" >>"$payload_root/metadata/packages.tsv"
printf 'split\n' >"$payload_root/metadata/layout"
printf '#!/usr/bin/env bash\nexit 0\n' >"$payload_root/core-root/usr/bin/lmm-api"
cp -- "$payload_root/core-root/usr/bin/lmm-api" "$payload_root/core-root/usr/bin/lmm-api-select"
cp -- "$payload_root/core-root/usr/bin/lmm-api" "$payload_root/go-root/usr/lib/lmm-api/backends/go/lmm-api"
chmod 0755 "$payload_root/core-root/usr/bin/lmm-api" \
  "$payload_root/core-root/usr/bin/lmm-api-select" \
  "$payload_root/go-root/usr/lib/lmm-api/backends/go/lmm-api"
printf '[Service]\nExecStart=/usr/bin/lmm-api\n' >"$payload_root/core-root/usr/lib/systemd/system/lmm-api.service"
printf 'LMM_API_BACKEND=go\n' >"$payload_root/core-root/etc/lmm-api/backend.conf"
: >"$payload_root/core-root/etc/lmm-api/lmm-api.env"
chmod 0644 "$payload_root/core-root/usr/lib/systemd/system/lmm-api.service" \
  "$payload_root/core-root/etc/lmm-api/backend.conf"
chmod 0600 "$payload_root/core-root/etc/lmm-api/lmm-api.env"
printf 'fixture\n' >"$payload_root/core-root/usr/share/licenses/lmm-api/LICENSE"
chmod 0644 "$payload_root/core-root/usr/share/licenses/lmm-api/LICENSE"
tar --sort=name --numeric-owner --owner=0 --group=0 -C "$payload_root" -cf "$tmp/precutover-payload.tar" .

TMPDIR=$workspace/tmp "$here/build-precutover-packages.sh" \
  --workspace "$workspace" --payload "$tmp/precutover-payload.tar" --output-dir "$rollback_dir" >/dev/null

cat >"$tmp/candidate.c" <<EOF
#include <stdio.h>
int main(void) { puts("$candidate_version"); return 0; }
EOF
cc -O2 -s -o "$tmp/lmm-api-go" "$tmp/candidate.c"
printf '<!doctype html><title>LMM fixture</title>\n' >"$frontend/index.html"
"$repo/packaging/local/lmm-api-go/build-local-package.sh" \
  --workspace "$workspace" --binary "$tmp/lmm-api-go" \
  --frontend "$frontend" --output-dir "$candidate_dir" >/dev/null

old_core=$(find "$rollback_dir" -maxdepth 1 -type f -name 'lmm-api-*.pkg.tar.*' ! -name 'lmm-api-go-*' ! -name '*.sha256' -print -quit)
old_go=$(find "$rollback_dir" -maxdepth 1 -type f -name 'lmm-api-go-*.pkg.tar.*' ! -name '*.sha256' -print -quit)
new_go=$(find "$candidate_dir" -maxdepth 1 -type f -name 'lmm-api-go-*.pkg.tar.*' ! -name '*.sha256' -print -quit)
[[ -n $old_core && -n $old_go && -n $new_go ]] || {
  printf 'go-package-roundtrip: package fixture is incomplete\n' >&2
  exit 1
}

install -d -m0755 "$pacman_root/etc" "$pacman_root/usr" \
  "$pacman_root/var/lib/pacman/local" "$pacman_root/var/cache/pacman/pkg" "$pacman_root/var/log"
common=(--root "$pacman_root" --dbpath "$pacman_root/var/lib/pacman" \
  --cachedir "$pacman_root/var/cache/pacman/pkg" --logfile "$pacman_root/var/log/pacman.log")
# This fixture validates file ownership and conflict transitions, not dependency
# resolution. Keeping the database empty avoids copying the host's entire local
# pacman database into every test run.
install_args=(pacman "${common[@]}" --noconfirm --noscriptlet --nodeps --nodeps)
query_args=(pacman "${common[@]}")

fakeroot -- "${install_args[@]}" -U "$old_core" "$old_go" >/dev/null
[[ $("${query_args[@]}" -Q lmm-api 2>/dev/null) == "lmm-api $old_core_version" ]]
[[ $("${query_args[@]}" -Q lmm-api-go 2>/dev/null) == "lmm-api-go $old_go_version" ]]

fakeroot -- "${install_args[@]}" -Rdd lmm-api lmm-api-go >/dev/null
fakeroot -- "${install_args[@]}" -U "$new_go" >/dev/null
if "${query_args[@]}" -Q lmm-api >/dev/null 2>&1; then
  printf 'go-package-roundtrip: old core package remains installed\n' >&2
  exit 1
fi
[[ $("${query_args[@]}" -Q lmm-api-go-bin 2>/dev/null) == "lmm-api-go-bin $candidate_version-1" ]]
[[ -x $pacman_root/usr/bin/lmm-api ]]
[[ ! -e $pacman_root/usr/bin/lmm-api-go ]]
[[ ! -e $pacman_root/usr/bin/lmm-api-deploy ]]
[[ $(stat -c '%a' "$pacman_root/etc/lmm-api-go") == 700 ]]
[[ $(stat -c '%a' "$pacman_root/etc/lmm-api-go/lmm-api-go.env") == 600 ]]

fakeroot -- "${install_args[@]}" -Rdd lmm-api-go-bin >/dev/null
fakeroot -- "${install_args[@]}" -U "$old_core" "$old_go" >/dev/null
[[ $("${query_args[@]}" -Q lmm-api 2>/dev/null) == "lmm-api $old_core_version" ]]
[[ $("${query_args[@]}" -Q lmm-api-go 2>/dev/null) == "lmm-api-go $old_go_version" ]]
[[ -x $pacman_root/usr/bin/lmm-api && -x $pacman_root/usr/bin/lmm-api-select ]]
[[ -x $pacman_root/usr/lib/lmm-api/backends/go/lmm-api && ! -e $pacman_root/usr/bin/lmm-api-go ]]

direct_payload_root=$tmp/direct-payload-root
direct_rollback_dir=$tmp/direct-rollback
direct_pacman_root=$tmp/direct-pacman-root
install -d -m0700 "$direct_payload_root/metadata" "$direct_payload_root/go-root/etc/lmm-api-go" \
  "$direct_rollback_dir"
install -d -m0755 \
  "$direct_payload_root/go-root/usr/bin" \
  "$direct_payload_root/go-root/usr/lib/systemd/system" \
  "$direct_payload_root/go-root/usr/share/doc/lmm-api-go" \
  "$direct_payload_root/go-root/usr/share/licenses/lmm-api-go" \
  "$direct_payload_root/go-root/usr/share/lmm-api-go/frontend-dist"
printf 'direct\n' >"$direct_payload_root/metadata/layout"
printf 'lmm-api-go\t%s\n' "$old_go_version" >"$direct_payload_root/metadata/packages.tsv"
cat >"$tmp/old-direct.c" <<EOF
#include <stdio.h>
int main(void) { puts("${old_go_version%-1}"); return 0; }
EOF
cc -O2 -s -o "$direct_payload_root/go-root/usr/bin/lmm-api-go" "$tmp/old-direct.c"
printf '[Service]\nExecStart=/usr/bin/lmm-api-go serve\n' \
  >"$direct_payload_root/go-root/usr/lib/systemd/system/lmm-api-go.service"
: >"$direct_payload_root/go-root/etc/lmm-api-go/lmm-api-go.env"
printf 'fixture\n' >"$direct_payload_root/go-root/usr/share/doc/lmm-api-go/REVISION"
for license_file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do
  printf 'fixture\n' >"$direct_payload_root/go-root/usr/share/licenses/lmm-api-go/$license_file"
done
printf 'old direct frontend\n' >"$direct_payload_root/go-root/usr/share/lmm-api-go/frontend-dist/index.html"
find "$direct_payload_root/go-root/usr" -type f -exec chmod 0644 {} +
chmod 0600 "$direct_payload_root/go-root/etc/lmm-api-go/lmm-api-go.env"
chmod 0755 "$direct_payload_root/go-root/usr/bin/lmm-api-go"
tar --sort=name --numeric-owner --owner=0 --group=0 -C "$direct_payload_root" \
  -cf "$tmp/direct-precutover-payload.tar" .
TMPDIR=$workspace/tmp "$here/build-precutover-packages.sh" \
  --workspace "$workspace" --payload "$tmp/direct-precutover-payload.tar" \
  --output-dir "$direct_rollback_dir" >/dev/null
old_direct=$(find "$direct_rollback_dir" -maxdepth 1 -type f -name 'lmm-api-go-*.pkg.tar.*' \
  ! -name '*.sha256' -print -quit)
[[ -n $old_direct && -f $direct_rollback_dir/rollback-layout.direct ]] || {
  printf 'go-package-roundtrip: direct rollback fixture is incomplete\n' >&2
  exit 1
}

install -d -m0755 "$direct_pacman_root/etc" "$direct_pacman_root/usr" \
  "$direct_pacman_root/var/lib/pacman/local" "$direct_pacman_root/var/cache/pacman/pkg" \
  "$direct_pacman_root/var/log"
direct_common=(--root "$direct_pacman_root" --dbpath "$direct_pacman_root/var/lib/pacman" \
  --cachedir "$direct_pacman_root/var/cache/pacman/pkg" --logfile "$direct_pacman_root/var/log/pacman.log")
direct_install=(pacman "${direct_common[@]}" --noconfirm --noscriptlet --nodeps --nodeps)
direct_query=(pacman "${direct_common[@]}")

fakeroot -- "${direct_install[@]}" -U "$old_direct" >/dev/null
[[ $("${direct_query[@]}" -Q lmm-api-go 2>/dev/null) == "lmm-api-go $old_go_version" ]]
[[ $("$direct_pacman_root/usr/bin/lmm-api-go") == "${old_go_version%-1}" ]]
fakeroot -- "${direct_install[@]}" -Rdd lmm-api-go >/dev/null
fakeroot -- "${direct_install[@]}" -U "$new_go" >/dev/null
[[ $("${direct_query[@]}" -Q lmm-api-go-bin 2>/dev/null) == "lmm-api-go-bin $candidate_version-1" ]]
[[ $("$direct_pacman_root/usr/bin/lmm-api") == "$candidate_version" ]]
fakeroot -- "${direct_install[@]}" -Rdd lmm-api-go-bin >/dev/null
fakeroot -- "${direct_install[@]}" -U "$old_direct" >/dev/null
[[ $("${direct_query[@]}" -Q lmm-api-go 2>/dev/null) == "lmm-api-go $old_go_version" ]]
if "${direct_query[@]}" -Q lmm-api >/dev/null 2>&1; then
  printf 'go-package-roundtrip: direct rollback resurrected the split core package\n' >&2
  exit 1
fi
[[ $("$direct_pacman_root/usr/bin/lmm-api-go") == "${old_go_version%-1}" ]]
[[ -f $direct_pacman_root/usr/share/lmm-api-go/frontend-dist/index.html ]]

transition_packages=$tmp/transition-packages
transition_pacman_root=$tmp/transition-pacman-root
install -d -m0700 "$transition_packages"

build_transition_go_package() {
  local phase version work
  local transition_conflicts='' transition_replaces=''
  phase=$1
  version=$2
  work=$tmp/transition-$phase
  if [[ $phase == t1 ]]; then
    transition_conflicts="'lmm-api-deploy' 'lmm-api-deploy-bin'"
    transition_replaces="'lmm-api-deploy-bin'"
  fi
  install -d -m0700 "$work"
  cat >"$work/PKGBUILD" <<EOF
pkgname=lmm-api-go-bin
pkgver=$version
pkgrel=1
pkgdesc='LMM API unified CLI transition fixture'
arch=('any')
license=('AGPL-3.0-only')
provides=("lmm-api=$version")
conflicts=($transition_conflicts)
replaces=($transition_replaces)
options=('!strip')
package() {
  install -Dm0755 /usr/bin/true "\${pkgdir}/usr/bin/lmm-api"
  install -Dm0644 "$repo/packaging/common/lmm-api/lmm-api-operator.sysusers" \\
    "\${pkgdir}/usr/lib/sysusers.d/lmm-api-operator.conf"
  install -Dm0644 "$repo/packaging/common/lmm-api/lmm-api-operator.tmpfiles" \\
    "\${pkgdir}/usr/lib/tmpfiles.d/lmm-api-operator.conf"
  install -Dm0440 "$repo/packaging/common/lmm-api/lmm-api-operator.sudoers" \\
    "\${pkgdir}/etc/sudoers.d/lmm-api-operator"
EOF
  if [[ $phase == t0 ]]; then
    cat >>"$work/PKGBUILD" <<'EOF'
  ln -s lmm-api "${pkgdir}/usr/bin/lmm-api-go"
EOF
  fi
  printf '}\n' >>"$work/PKGBUILD"
  (cd "$work" && PKGDEST="$transition_packages" makepkg --force --nodeps --noconfirm >/dev/null)
}

legacy_deploy_work=$tmp/transition-legacy-deploy
install -d -m0700 "$legacy_deploy_work"
cat >"$legacy_deploy_work/PKGBUILD" <<'EOF'
pkgname=lmm-api-deploy-bin
pkgver=0.1.57
pkgrel=1
pkgdesc='Legacy deploy CLI transition fixture'
arch=('any')
license=('AGPL-3.0-only')
options=('!strip')
package() {
  install -Dm0755 /usr/bin/true "${pkgdir}/usr/bin/lmm-api-deploy"
}
EOF
(cd "$legacy_deploy_work" && PKGDEST="$transition_packages" makepkg --force --nodeps --noconfirm >/dev/null)
build_transition_go_package t0 0.1.58
build_transition_go_package t1 0.1.59

t0_package=$(find "$transition_packages" -maxdepth 1 -type f -name 'lmm-api-go-bin-0.1.58-1-*.pkg.tar.*' -print -quit)
t1_package=$(find "$transition_packages" -maxdepth 1 -type f -name 'lmm-api-go-bin-0.1.59-1-*.pkg.tar.*' -print -quit)
legacy_deploy_package=$(find "$transition_packages" -maxdepth 1 -type f -name 'lmm-api-deploy-bin-*.pkg.tar.*' -print -quit)
[[ -n $t0_package && -n $t1_package && -n $legacy_deploy_package ]] || {
  printf 'go-package-roundtrip: T0/T1 transition package fixture is incomplete\n' >&2
  exit 1
}

install -d -m0755 "$transition_pacman_root/etc" "$transition_pacman_root/usr" \
  "$transition_pacman_root/var/lib/pacman/local" "$transition_pacman_root/var/cache/pacman/pkg" \
  "$transition_pacman_root/var/log"
transition_common=(--root "$transition_pacman_root" --dbpath "$transition_pacman_root/var/lib/pacman" \
  --cachedir "$transition_pacman_root/var/cache/pacman/pkg" --logfile "$transition_pacman_root/var/log/pacman.log")
transition_install=(pacman "${transition_common[@]}" --noconfirm --noscriptlet --nodeps --nodeps)
transition_query=(pacman "${transition_common[@]}")

fakeroot -- "${transition_install[@]}" -U "$legacy_deploy_package" "$t0_package" >/dev/null
[[ $("${transition_query[@]}" -Q lmm-api-go-bin 2>/dev/null) == 'lmm-api-go-bin 0.1.58-1' ]]
[[ $("${transition_query[@]}" -Q lmm-api-deploy-bin 2>/dev/null) == 'lmm-api-deploy-bin 0.1.57-1' ]]
[[ -x $transition_pacman_root/usr/bin/lmm-api && -L $transition_pacman_root/usr/bin/lmm-api-go ]]
[[ -x $transition_pacman_root/usr/bin/lmm-api-deploy ]]

fakeroot -- "${transition_install[@]}" -U "$t1_package" >/dev/null
[[ $("${transition_query[@]}" -Q lmm-api-go-bin 2>/dev/null) == 'lmm-api-go-bin 0.1.59-1' ]]
if "${transition_query[@]}" -Q lmm-api-deploy-bin >/dev/null 2>&1; then
  printf 'go-package-roundtrip: T1 did not replace the legacy deploy package\n' >&2
  exit 1
fi
[[ -x $transition_pacman_root/usr/bin/lmm-api ]]
[[ ! -e $transition_pacman_root/usr/bin/lmm-api-go && ! -e $transition_pacman_root/usr/bin/lmm-api-deploy ]]
[[ -f $transition_pacman_root/etc/sudoers.d/lmm-api-operator ]]

fakeroot -- "${transition_install[@]}" -U "$t0_package" >/dev/null
[[ $("${transition_query[@]}" -Q lmm-api-go-bin 2>/dev/null) == 'lmm-api-go-bin 0.1.58-1' ]]
[[ -x $transition_pacman_root/usr/bin/lmm-api && -L $transition_pacman_root/usr/bin/lmm-api-go ]]
[[ $(readlink "$transition_pacman_root/usr/bin/lmm-api-go") == lmm-api ]]
[[ ! -e $transition_pacman_root/usr/bin/lmm-api-deploy ]]
[[ -f $transition_pacman_root/etc/sudoers.d/lmm-api-operator ]]

printf 'split cutover, direct Go, and T0-T1-T0 package roundtrips verified\n'
