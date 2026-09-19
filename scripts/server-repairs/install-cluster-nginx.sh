#!/usr/bin/env bash
set -Eeuo pipefail

stage=${1:?absolute staging directory required}
[[ $EUID == 0 && $stage == /* && -d $stage && ! -L $stage ]]
exec 9>/run/lock/lmm-api-go-deploy.lock
flock -n 9
[[ ! -e /var/lib/lmm-api-go-deploy/transaction.lock ]]
for name in lmm-api-http-map.conf lmm-api-locations.conf lmm-api-region-policy.conf peer.conf; do
  [[ -f $stage/$name && ! -L $stage/$name ]]
done

backup=$(mktemp -d /var/backups/lmm-api/nginx-cluster.XXXXXXXX)
chmod 700 "$backup"
for name in lmm-api-http-map.conf lmm-api-locations.conf lmm-api-region-policy.conf; do
  cp -a "/etc/nginx/$name" "$backup/$name"
done
peer=/etc/lmm-api/nginx/peers/cluster.conf
if [[ -e $peer ]]; then
  cp -a "$peer" "$backup/peer.conf"
fi

restore() {
  local status=$?
  trap - ERR
  for name in lmm-api-http-map.conf lmm-api-locations.conf lmm-api-region-policy.conf; do
    cp -a "$backup/$name" "/etc/nginx/$name"
  done
  if [[ -f $backup/peer.conf ]]; then
    cp -a "$backup/peer.conf" "$peer"
  else
    rm -f -- "$peer"
  fi
  nginx -t && systemctl reload nginx
  printf 'Cluster nginx activation failed; restored %s\n' "$backup" >&2
  exit "$status"
}
trap restore ERR

install -d -m 755 /etc/lmm-api/nginx /etc/lmm-api/nginx/peers
install -m 644 "$stage/peer.conf" "$peer"
for name in lmm-api-http-map.conf lmm-api-locations.conf lmm-api-region-policy.conf; do
  install -m 644 "$stage/$name" "/etc/nginx/$name.next"
  mv -f "/etc/nginx/$name.next" "/etc/nginx/$name"
done
nginx -t
systemctl reload nginx
systemctl is-active --quiet nginx
trap - ERR
printf 'Cluster nginx active; rollback files: %s\n' "$backup"
