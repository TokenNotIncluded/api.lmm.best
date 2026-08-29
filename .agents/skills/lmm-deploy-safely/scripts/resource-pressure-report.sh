#!/usr/bin/env bash

set -euo pipefail
umask 077

# This command is deliberately a read-only observer. It never invokes
# systemctl start/stop/restart, swapoff/swapon, rm, or any cleanup command.
readonly DEFAULT_EXPECTED_HOST='arch-dmit'
readonly DEFAULT_SERVICE='lmm-api.service'
readonly DEFAULT_BASE_URL='http://127.0.0.1:3000'
readonly DEFAULT_SAMPLES=2
readonly DEFAULT_INTERVAL_SECONDS=1
readonly EXPECTED_ROOT_BYTES=$((20 * 1024 * 1024 * 1024))
readonly EXPECTED_MEMORY_BYTES=$((951 * 1024 * 1024))
readonly MIN_ROOT_FREE_BYTES=$((4 * 1024 * 1024 * 1024))
readonly HOST_PATTERN='^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$'
readonly SERVICE_PATTERN='^[A-Za-z0-9][A-Za-z0-9_.@:-]{0,127}\.service$'
readonly URL_PATTERN='^https?://([A-Za-z0-9.-]+|\[[0-9A-Fa-f:]+\])(:[0-9]{1,5})?$'

usage() {
  cat >&2 <<'EOF'
Usage: resource-pressure-report.sh [options]

Read-only, low-cost ArchDmit resource and health report.

Options:
  --expected-host HOST   Expected static hostname (default: arch-dmit)
  --service UNIT         systemd service (default: lmm-api.service)
  --base-url URL         Local API base URL (default: http://127.0.0.1:3000)
  --samples N             Swap samples, 1-5 (default: 2)
  --interval SECONDS     Delay between samples, 0-60 (default: 1)
  --proc-root PATH       Read-only proc fixture root (for offline tests)
  --format kv|json       Output format (default: kv)
  -h, --help             Show this help

Exit status:
  0  all observed gates are green
  1  warning observed
  2  invalid input or an unavailable required local probe
  3  hostname does not match --expected-host
  4  stop-level pressure or failed service/health gate

The command never clears swap, restarts services, deletes files, or installs
timers. Run it on the target, for example:

  ssh ArchDmit 'bash -s -- --expected-host arch-dmit' < resource-pressure-report.sh
EOF
}

die() {
  printf 'error: %s\n' "$1" >&2
  exit 2
}

reject_unsafe_text() {
  local value=$1
  [[ $value != *$'\n'* && $value != *$'\r'* && $value != *$'\t'* ]] ||
    die 'value contains control characters'
}

validate_host() {
  [[ $1 =~ $HOST_PATTERN ]] || die 'invalid hostname'
}

validate_service() {
  [[ $1 =~ $SERVICE_PATTERN ]] || die 'service must be a simple .service unit name'
}

validate_url() {
  reject_unsafe_text "$1"
  [[ $1 =~ $URL_PATTERN ]] || die 'base URL must be an http(s) origin without a path or query'
}

validate_integer() {
  local name=$1
  local value=$2
  local minimum=$3
  local maximum=$4
  [[ $value =~ ^[0-9]+$ ]] || die "$name must be an integer"
  ((value >= minimum && value <= maximum)) || die "$name must be between $minimum and $maximum"
}

validate_proc_root() {
  local path=$1
  local canonical
  reject_unsafe_text "$path"
  [[ $path == /* ]] || die 'proc root must be absolute'
  canonical=$(realpath -e -- "$path" 2>/dev/null) || die 'proc root does not exist'
  [[ $canonical == "$path" ]] || die 'proc root must be canonical'
  [[ -d $canonical && ! -L $canonical ]] || die 'proc root must be a directory'
  printf '%s\n' "$canonical"
}

proc_file() {
  printf '%s/%s\n' "$proc_root" "$1"
}

read_kib_value() {
  local file=$1
  local key=$2
  local value
  [[ -f $file && ! -L $file ]] || die "required proc file is missing: $file"
  value=$(awk -v key="$key" '$1 == key ":" {print $2; exit}' "$file")
  [[ $value =~ ^[0-9]+$ ]] || die "required proc value is missing or invalid: $key"
  printf '%s\n' "$((value * 1024))"
}

read_page_value() {
  local file=$1
  local key=$2
  local value
  [[ -f $file && ! -L $file ]] || die "required proc file is missing: $file"
  value=$(awk -v key="$key" '$1 == key {print $2; exit}' "$file")
  [[ $value =~ ^[0-9]+$ ]] || die "required proc value is missing or invalid: $key"
  printf '%s\n' "$value"
}

human_bytes() {
  awk -v bytes="$1" 'BEGIN {
    if (bytes >= 1073741824) printf "%.2f GiB", bytes / 1073741824;
    else if (bytes >= 1048576) printf "%.0f MiB", bytes / 1048576;
    else if (bytes >= 1024) printf "%.0f KiB", bytes / 1024;
    else printf "%.0f B", bytes;
  }'
}

json_escape() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  printf '%s' "$value"
}

declare -a report_keys=()
declare -A report_values=()

set_report() {
  local key=$1
  local value=$2
  if [[ ! -v report_values[$key] ]]; then
    report_keys+=("$key")
  fi
  report_values[$key]=$value
}

overall_state='green'

raise_state() {
  case "$1" in
    stop) overall_state='stop' ;;
    warning) [[ $overall_state != stop ]] && overall_state='warning' ;;
    green) ;;
    *) die "unknown report state: $1" ;;
  esac
}

set_pressure_state() {
  local key=$1
  local state=$2
  set_report "$key" "$state"
  raise_state "$state"
}

current_host() {
  local host=''
  if command -v hostnamectl >/dev/null 2>&1; then
    host=$(hostnamectl --static 2>/dev/null || true)
  fi
  if [[ -z $host ]] && command -v hostname >/dev/null 2>&1; then
    host=$(hostname -s 2>/dev/null || true)
  fi
  [[ -n $host ]] || die 'unable to determine static hostname'
  validate_host "$host"
  printf '%s\n' "$host"
}

sample_swap() {
  swap_total_bytes=$(read_kib_value "$(proc_file meminfo)" SwapTotal)
  swap_free_bytes=$(read_kib_value "$(proc_file meminfo)" SwapFree)
  ((swap_free_bytes <= swap_total_bytes)) || die 'SwapFree exceeds SwapTotal'
  swap_used_bytes=$((swap_total_bytes - swap_free_bytes))
  swap_in_pages=$(read_page_value "$(proc_file vmstat)" pswpin)
  swap_out_pages=$(read_page_value "$(proc_file vmstat)" pswpout)
}

read_root_filesystem() {
  local line
  line=$(df -P -B1 -- / 2>/dev/null | awk 'NR == 2 {print $2, $3, $4, $5; exit}')
  read -r root_total_bytes root_used_bytes root_available_bytes root_used_percent <<< "$line"
  [[ $root_total_bytes =~ ^[0-9]+$ && $root_used_bytes =~ ^[0-9]+$ &&
     $root_available_bytes =~ ^[0-9]+$ && $root_used_percent =~ ^[0-9]+%$ ]] ||
    die 'unable to read root filesystem usage'
  root_used_percent=${root_used_percent%%%}
  ((root_used_bytes + root_available_bytes <= root_total_bytes)) ||
    die 'root filesystem counters are inconsistent'
}

declare -A service_values=()

read_service() {
  local output line key value
  output=$(systemctl show --no-pager \
    --property=ActiveState,SubState,MainPID,NRestarts,MemoryCurrent,MemoryHigh,MemoryMax,MemorySwapMax \
    "$service" 2>/dev/null) || die "unable to inspect $service with systemctl show"
  while IFS='=' read -r key value; do
    case "$key" in
      ActiveState|SubState|MainPID|NRestarts|MemoryCurrent|MemoryHigh|MemoryMax|MemorySwapMax)
        service_values[$key]=$value
        ;;
    esac
  done <<< "$output"
  local required
  for required in ActiveState SubState MainPID NRestarts MemoryCurrent MemoryHigh MemoryMax MemorySwapMax; do
    [[ -v service_values[$required] ]] || die "systemd did not return $required for $service"
    set_report "service_${required}" "${service_values[$required]}"
  done
}

probe_health() {
  local name=$1
  local path=$2
  local output http_code seconds latency_ms
  if output=$(curl --silent --show-error --output /dev/null \
    --write-out '%{http_code} %{time_total}' --connect-timeout 2 --max-time 10 \
    -- "$base_url$path" 2>/dev/null); then
    read -r http_code seconds <<< "$output"
  else
    http_code='000'
    seconds='-'
  fi
  [[ $http_code =~ ^[0-9]{3}$ ]] || http_code='000'
  if [[ $seconds =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    latency_ms=$(awk -v seconds="$seconds" 'BEGIN {printf "%.0f", seconds * 1000}')
  else
    latency_ms='-'
  fi
  set_report "${name}_http_status" "$http_code"
  set_report "${name}_latency_ms" "$latency_ms"
  if [[ $http_code == 200 ]]; then
    set_report "${name}_state" 'ok'
  else
    set_report "${name}_state" 'failed'
    raise_state stop
  fi
}

emit_report() {
  if [[ $format == kv ]]; then
    local key
    for key in "${report_keys[@]}"; do
      printf '%s=%q\n' "$key" "${report_values[$key]}"
    done
  else
    local key index=0
    printf '{\n'
    for key in "${report_keys[@]}"; do
      ((index == 0)) || printf ',\n'
      printf '  "%s": "%s"' "$key" "$(json_escape "${report_values[$key]}")"
      index=$((index + 1))
    done
    printf '\n}\n'
  fi
}

expected_host=$DEFAULT_EXPECTED_HOST
service=$DEFAULT_SERVICE
base_url=$DEFAULT_BASE_URL
samples=$DEFAULT_SAMPLES
interval_seconds=$DEFAULT_INTERVAL_SECONDS
proc_root='/proc'
format='kv'

while (($# > 0)); do
  case "$1" in
    --expected-host)
      (($# >= 2)) || die 'missing value for --expected-host'
      expected_host=$2
      shift 2
      ;;
    --service)
      (($# >= 2)) || die 'missing value for --service'
      service=$2
      shift 2
      ;;
    --base-url)
      (($# >= 2)) || die 'missing value for --base-url'
      base_url=$2
      shift 2
      ;;
    --samples)
      (($# >= 2)) || die 'missing value for --samples'
      samples=$2
      shift 2
      ;;
    --interval)
      (($# >= 2)) || die 'missing value for --interval'
      interval_seconds=$2
      shift 2
      ;;
    --proc-root)
      (($# >= 2)) || die 'missing value for --proc-root'
      proc_root=$2
      shift 2
      ;;
    --format)
      (($# >= 2)) || die 'missing value for --format'
      format=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      die 'unknown argument'
      ;;
  esac
done

validate_host "$expected_host"
validate_service "$service"
validate_url "$base_url"
base_url=${base_url%/}
validate_integer samples "$samples" 1 5
validate_integer interval "$interval_seconds" 0 60
case "$format" in kv|json) ;; *) die 'format must be kv or json' ;; esac
proc_root=$(validate_proc_root "$proc_root")

observed_host=$(current_host)
set_report report_version 1
set_report collected_at_utc "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
set_report expected_host "$expected_host"
set_report observed_host "$observed_host"
set_report host_match "$([[ $observed_host == "$expected_host" ]] && printf true || printf false)"
set_report service_unit "$service"
set_report health_base_url "$base_url"
set_report samples "$samples"
set_report interval_seconds "$interval_seconds"
set_report mutations_performed none
set_report host_profile_root_bytes "$EXPECTED_ROOT_BYTES"
set_report host_profile_root_human "$(human_bytes "$EXPECTED_ROOT_BYTES")"
set_report host_profile_memory_bytes "$EXPECTED_MEMORY_BYTES"
set_report host_profile_memory_human "$(human_bytes "$EXPECTED_MEMORY_BYTES")"

if [[ $observed_host != "$expected_host" ]]; then
  emit_report
  exit 3
fi

sample_swap
initial_swap_total_bytes=$swap_total_bytes
initial_swap_used_bytes=$swap_used_bytes
initial_swap_in_pages=$swap_in_pages
initial_swap_out_pages=$swap_out_pages
for ((sample_index = 1; sample_index < samples; sample_index += 1)); do
  if ((interval_seconds > 0)); then
    sleep "$interval_seconds"
  fi
  sample_swap
done
final_swap_total_bytes=$swap_total_bytes
final_swap_used_bytes=$swap_used_bytes
final_swap_in_pages=$swap_in_pages
final_swap_out_pages=$swap_out_pages

mem_total_bytes=$(read_kib_value "$(proc_file meminfo)" MemTotal)
mem_available_bytes=$(read_kib_value "$(proc_file meminfo)" MemAvailable)
((mem_available_bytes <= mem_total_bytes)) || die 'MemAvailable exceeds MemTotal'
mem_available_percent=$((mem_available_bytes * 100 / mem_total_bytes))
swap_used_percent=0
if ((final_swap_total_bytes > 0)); then
  swap_used_percent=$((final_swap_used_bytes * 100 / final_swap_total_bytes))
fi
swap_used_delta_bytes=$((final_swap_used_bytes - initial_swap_used_bytes))
swap_in_pages_delta=$((final_swap_in_pages - initial_swap_in_pages))
swap_out_pages_delta=$((final_swap_out_pages - initial_swap_out_pages))

set_report memory_total_bytes "$mem_total_bytes"
set_report memory_total_human "$(human_bytes "$mem_total_bytes")"
set_report mem_available_bytes "$mem_available_bytes"
set_report mem_available_human "$(human_bytes "$mem_available_bytes")"
set_report mem_available_percent "$mem_available_percent"
if ((mem_available_percent >= 30)); then
  set_pressure_state mem_pressure green
elif ((mem_available_percent >= 20)); then
  set_pressure_state mem_pressure warning
else
  set_pressure_state mem_pressure stop
fi
set_report memory_profile_match "$([[ $mem_total_bytes == "$EXPECTED_MEMORY_BYTES" ]] && printf true || printf false)"
if ((mem_total_bytes != EXPECTED_MEMORY_BYTES)); then
  raise_state warning
fi

set_report swap_total_bytes "$final_swap_total_bytes"
set_report swap_total_human "$(human_bytes "$final_swap_total_bytes")"
set_report swap_total_initial_bytes "$initial_swap_total_bytes"
set_report swap_used_bytes "$final_swap_used_bytes"
set_report swap_used_human "$(human_bytes "$final_swap_used_bytes")"
set_report swap_used_percent "$swap_used_percent"
set_report swap_used_delta_bytes "$swap_used_delta_bytes"
if ((swap_used_delta_bytes < 0)); then
  swap_delta_abs=$((-swap_used_delta_bytes))
else
  swap_delta_abs=$swap_used_delta_bytes
fi
set_report swap_used_delta_human "$(human_bytes "$swap_delta_abs")"
set_report swap_in_pages_delta "$swap_in_pages_delta"
set_report swap_out_pages_delta "$swap_out_pages_delta"
if ((swap_in_pages_delta > 0 || swap_out_pages_delta > 0)); then
  set_report swap_churn observed
  raise_state warning
else
  set_report swap_churn none
fi
if ((swap_used_percent < 10)); then
  set_pressure_state swap_pressure green
elif ((swap_used_percent <= 25)); then
  set_pressure_state swap_pressure warning
else
  set_pressure_state swap_pressure stop
fi
if ((swap_used_delta_bytes > 0)); then
  set_report swap_change increased
elif ((swap_used_delta_bytes < 0)); then
  set_report swap_change decreased
else
  set_report swap_change unchanged
fi

read_root_filesystem
set_report root_total_bytes "$root_total_bytes"
set_report root_total_human "$(human_bytes "$root_total_bytes")"
set_report root_used_bytes "$root_used_bytes"
set_report root_used_human "$(human_bytes "$root_used_bytes")"
set_report root_available_bytes "$root_available_bytes"
set_report root_available_human "$(human_bytes "$root_available_bytes")"
set_report root_used_percent "$root_used_percent"
set_report root_profile_match "$([[ $root_total_bytes == "$EXPECTED_ROOT_BYTES" ]] && printf true || printf false)"
if ((root_total_bytes != EXPECTED_ROOT_BYTES)); then
  raise_state warning
fi
if ((root_used_percent < 70)); then
  set_pressure_state root_pressure green
elif ((root_used_percent < 80)); then
  set_pressure_state root_pressure warning
else
  set_pressure_state root_pressure stop
fi
if ((root_available_bytes >= MIN_ROOT_FREE_BYTES)); then
  set_report root_headroom green
else
  set_pressure_state root_headroom stop
fi

read_service
if [[ ${service_values[ActiveState]} == active && ${service_values[SubState]} == running ]]; then
  set_report service_state green
else
  set_pressure_state service_state stop
fi
if [[ ${service_values[NRestarts]} =~ ^[0-9]+$ ]]; then
  if ((service_values[NRestarts] > 0)); then
    raise_state warning
  fi
else
  die 'systemd returned an invalid NRestarts value'
fi
if [[ ${service_values[MemoryCurrent]} =~ ^[0-9]+$ ]]; then
  if [[ ${service_values[MemoryMax]} =~ ^[0-9]+$ ]] &&
     ((service_values[MemoryMax] > 0 && service_values[MemoryCurrent] >= service_values[MemoryMax])); then
    raise_state stop
  elif [[ ${service_values[MemoryHigh]} =~ ^[0-9]+$ ]] &&
       ((service_values[MemoryHigh] > 0 && service_values[MemoryCurrent] >= service_values[MemoryHigh])); then
    raise_state warning
  fi
fi

probe_health status /api/status
probe_health livez /api/livez
set_report overall_state "$overall_state"
emit_report

case "$overall_state" in
  green) exit 0 ;;
  warning) exit 1 ;;
  stop) exit 4 ;;
  *) die 'invalid final report state' ;;
esac
