#!/usr/bin/env bash
set -euo pipefail

# Complete classification coverage; never a parity claim.
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../../../.." && pwd -P)
routes_dir="$repo_root/apps/api-rust/tests/fixtures/routes"
manifest_arg=''
if [[ $# -gt 0 ]]; then
  [[ $# == 2 && $1 == --manifest ]] || {
    printf 'usage: %s [--manifest path-to-go-route-manifest.tsv]\n' "$0" >&2
    exit 2
  }
  manifest_arg=$2
fi
go_manifest=${manifest_arg:-${COMPLETE_GO_MANIFEST:-}}
frozen=${COMPLETE_FROZEN_LEDGER:-"$routes_dir/legacy-go-routes.tsv"}
implemented=${COMPLETE_RUST_IMPLEMENTED_LEDGER:-"$routes_dir/rust-implemented-routes.tsv"}
legacy_stubs=${COMPLETE_LEGACY_STUB_LEDGER:-"$routes_dir/legacy-equivalent-stubs.tsv"}
blockers=${COMPLETE_RUNTIME_BLOCKERS_LEDGER:-"$routes_dir/test-instance-runtime-blockers.tsv"}
normal_mounts=${COMPLETE_NORMAL_MOUNTS_LEDGER:-"$routes_dir/rust-normal-mounted-routes.tsv"}
compatibility_shells=${COMPLETE_FAIL_CLOSED_SHELLS_LEDGER:-"$routes_dir/rust-mounted-fail-closed-shells.tsv"}
route_gate=${COMPLETE_ROUTE_GATE:-"$routes_dir/route-gate.tsv"}
mcp_paths=${COMPLETE_MCP_PATHS:-"$repo_root/apps/api-go/router/open_source_bounty_mcp_router.go"}
results_dir=${COMPLETE_DIFFERENTIAL_RESULTS_DIR:-}
strict_classification=${COMPLETE_REQUIRE_EXPLICIT_CLASSIFICATION:-0}

fail() { printf 'complete route coverage: %s\n' "$*" >&2; exit 1; }
[[ $strict_classification == 0 || $strict_classification == 1 ]] || fail 'COMPLETE_REQUIRE_EXPLICIT_CLASSIFICATION must be 0 or 1'
for file in "$frozen" "$implemented" "$legacy_stubs" "$blockers" "$normal_mounts" "$compatibility_shells" "$route_gate"; do
  [[ -f $file ]] || fail "missing ledger: $file"
done
[[ -z $results_dir || -d $results_dir ]] || fail "COMPLETE_DIFFERENTIAL_RESULTS_DIR is not a directory: $results_dir"
command -v perl >/dev/null || fail 'perl is required'

work=$(mktemp -d "${TMPDIR:-/tmp}/lmm-complete-route-coverage.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
manifest="$work/go-route-manifest.tsv"
if [[ -n $go_manifest ]]; then
  [[ -f $go_manifest ]] || fail "COMPLETE_GO_MANIFEST is not a file: $go_manifest"
  cp -- "$go_manifest" "$manifest"
else
  (cd "$repo_root/apps/api-go" && GIN_MODE=release go run ./cmd/route-manifest >"$manifest") || fail 'Go route-manifest command failed'
fi
[[ -s $manifest ]] || fail 'Go route manifest is empty'

mcp_inventory="$work/mcp.tsv"
: >"$mcp_inventory"
if [[ -n ${COMPLETE_MCP_PATHS:-} ]]; then
  [[ -f $mcp_paths ]] || fail "COMPLETE_MCP_PATHS is not a file: $mcp_paths"
  while IFS= read -r relative; do
    [[ -z $relative || $relative == / ]] || fail "unexpected MCP relative path: $relative"
    path=/mcp; [[ $relative == / ]] && path=/mcp/
    for method in GET POST PUT PATCH HEAD OPTIONS DELETE CONNECT TRACE; do
      printf '%s\t%s\t%s\n' "$method" "$path" 'gin.RouterGroup.Any' >>"$mcp_inventory"
    done
  done <"$mcp_paths"
else
  while IFS= read -r relative; do
    case "$relative" in '') path=/mcp ;; /) path=/mcp/ ;; *) fail "unexpected MCP Any relative path: $relative" ;; esac
    for method in GET POST PUT PATCH HEAD OPTIONS DELETE CONNECT TRACE; do
      printf '%s\t%s\t%s\n' "$method" "$path" 'gin.RouterGroup.Any' >>"$mcp_inventory"
    done
  done < <(sed -nE 's/.*mcpRoute\.Any\("([^"]*)".*/\1/p' "$mcp_paths")
fi
awk -F '\t' '
  NR == FNR { seen[$1 FS $2] = 1; print; next }
  !seen[$1 FS $2]++ { print }
' "$manifest" "$mcp_inventory" >"$work/go-full.tsv"

perl - "$work/go-full.tsv" "$frozen" "$implemented" "$legacy_stubs" "$blockers" "$normal_mounts" "$compatibility_shells" "$route_gate" "$results_dir" "$strict_classification" <<'PERL'
use strict;
use warnings;
use JSON::PP qw(decode_json encode_json);
sub true { return JSON::PP::true; }
sub false { return JSON::PP::false; }

my ($inventory_path, $frozen_path, $implemented_path, $stubs_path, $blockers_path, $normal_path, $shells_path, $gate_path, $results_dir, $strict) = @ARGV;
sub fail { die "complete route coverage: $_[0]\n"; }
sub key { return join("\x1f", @_); }
sub read_tsv {
  my ($path, $name, $min, $comments) = @_;
  open my $fh, '<', $path or fail("cannot read $name: $!");
  my (@rows, $line_no);
  while (my $line = <$fh>) {
    $line_no++; chomp $line;
    next if $line eq '' || ($comments && $line =~ /^#/);
    my @fields = split /\t/, $line, -1;
    next if $line_no == 1 && $fields[0] =~ /^(?:method|# method)$/i;
    fail("malformed $name row at $line_no") if @fields < $min || $fields[0] eq '' || $fields[1] eq '';
    push @rows, \@fields;
  }
  close $fh;
  return @rows;
}
sub route_map {
  my ($path, $name, $min, $comments, $deduplicate_routes) = @_;
  my %map;
  for my $row (read_tsv($path, $name, $min, $comments)) {
    my $route = key($row->[0], $row->[1]);
    if (exists $map{$route}) {
      next if $deduplicate_routes;
      fail("duplicate $name route: $row->[0] $row->[1]");
    }
    $map{$route} = $row;
  }
  return %map;
}

my %frozen = route_map($frozen_path, 'frozen ledger', 3, 0);
my %implemented = route_map($implemented_path, 'Rust implemented ledger', 3, 1);
my %stubs = route_map($stubs_path, 'legacy stub ledger', 3, 0);
my %blockers = route_map($blockers_path, 'runtime blocker ledger', 3, 0, 1);
my %normal = route_map($normal_path, 'normal mount ledger', 3, 1);
my %shells = route_map($shells_path, 'mounted fail-closed shell ledger', 6, 1);
my %live = route_map($inventory_path, 'Go inventory', 3, 0);

open my $gate, '<', $gate_path or fail("cannot read route gate: $!");
my $gate_header = <$gate>;
fail('route gate is empty') unless defined $gate_header;
chomp $gate_header;
$gate_header =~ s/\r$//;
my @gate_columns = split /\t/, $gate_header, -1;
my %gate_column;
@gate_column{@gate_columns} = (0 .. $#gate_columns);
for my $required (qw(method path source_state mount_state gate_state evidence)) {
  fail("route gate is missing $required column") unless exists $gate_column{$required};
}
my (%retired, $gate_line);
$gate_line = 1;
while (my $line = <$gate>) {
  $gate_line++;
  chomp $line;
  $line =~ s/\r$//;
  my @fields = split /\t/, $line, -1;
  fail("malformed route gate row at $gate_line") unless @fields == @gate_columns;
  my ($method, $path, $source, $mount, $state, $evidence) =
    @fields[@gate_column{qw(method path source_state mount_state gate_state evidence)}];
  next unless $evidence =~ /(?:^|;)retired=true(?:;|$)/;
  fail("retired route gate row has invalid state at $gate_line: $method $path")
    unless $method =~ /^(?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE|CONNECT)$/
      && $path =~ m{^/}
      && $source eq 'absent'
      && $mount eq 'unmounted'
      && $state eq 'legacy-go';
  my $route = key($method, $path);
  fail("duplicate retired route gate route: $method $path") if exists $retired{$route};
  $retired{$route} = [$method, $path];
}
close $gate;
for my $route (sort keys %retired) {
  my ($method, $path) = @{$retired{$route}};
  fail("retired route is not frozen: $method $path") unless exists $frozen{$route};
  fail("retired route was reintroduced in current Go manifest: $method $path") if exists $live{$route};
  fail("retired route remains in Rust implemented ledger: $method $path") if exists $implemented{$route};
  fail("retired route remains in Rust normal mount ledger: $method $path") if exists $normal{$route};
  fail("retired route remains in legacy stub ledger: $method $path") if exists $stubs{$route};
}
# A frozen legacy endpoint may be mounted on the normal listener when the
# mount is an explicit, audited compatibility boundary.  Keep the stub ledger
# entry so coverage continues to classify the Go contract as legacy-501, while
# allowing the Rust ledger to record that the auth-gated response is reachable.
my %mounted_frozen_compatibility = map { key(split /\t/, $_) => 1 } (
  "GET\t/v1/files",
  "POST\t/v1/files",
  "DELETE\t/v1/files/:id",
  "GET\t/v1/files/:id",
  "GET\t/v1/files/:id/content",
  "GET\t/v1/fine-tunes",
  "POST\t/v1/fine-tunes",
  "GET\t/v1/fine-tunes/:id",
  "POST\t/v1/fine-tunes/:id/cancel",
  "GET\t/v1/fine-tunes/:id/events",
  "POST\t/v1/images/variations",
  "DELETE\t/v1/models/:model",
);
for my $route (keys %stubs) {
  fail("legacy stub is not frozen: $route") unless exists $frozen{$route};
  fail("route is both legacy stub and provider-blocked: $route") if exists $blockers{$route};
}
for my $route (keys %blockers) { fail("runtime blocker is not frozen: $route") unless exists $frozen{$route}; }
for my $route (keys %shells) {
  my ($method, $path, $handler, $mount, $adapter, $promotion) = @{$shells{$route}};
  fail("mounted fail-closed shell is absent from current Go inventory: $method $path") unless exists $live{$route};
  fail("mounted fail-closed shell has invalid Rust handler evidence: $method $path")
    unless $handler =~ /^lmm_api_rs::/;
  fail("mounted fail-closed shell has invalid ordinary-listener evidence: $method $path")
    unless $mount =~ m{^apps/api-rust/src/main\.rs:};
  fail("mounted fail-closed shell lacks an explicit blocker adapter: $method $path")
    unless $adapter =~ /(?:^|\+)(?:Disabled|FailClosed|Unconfigured)[A-Za-z0-9_]*(?:\+|$)/;
  fail("mounted fail-closed shell lacks the real-adapter capability promotion gate: $method $path")
    unless $promotion eq 'requires-real-adapter-and-capability-test';
  fail("mounted fail-closed shell is incorrectly counted as implemented: $method $path") if exists $implemented{$route};
  fail("mounted fail-closed shell is incorrectly counted as a normal mount: $method $path") if exists $normal{$route};
  fail("mounted fail-closed shell is also a legacy stub: $method $path") if exists $stubs{$route};
}
for my $route (keys %implemented) {
  my ($method, $path, $handler) = @{$implemented{$route}};
  fail("known blocker adapter cannot qualify as implemented: $method $path")
    if $handler =~ /(?:^|::)(?:Disabled|FailClosed|Unconfigured)[A-Za-z0-9_]*/;
  fail("route is both implemented and legacy stub: $route")
    if exists $stubs{$route}
      && !(exists $mounted_frozen_compatibility{$route}
        && exists $normal{$route}
        && ($implemented{$route}->[2] // '') =~ /(?:relay_misc_frozen|relay_anthropic_gemini)/);
  # A normal listener may have a concrete implementation while the isolated
  # test instance deliberately wires a fail-closed provider adapter for the
  # same route.  The blocker is then test-instance-only, not a contradiction
  # in the production candidate inventory.  Keep rejecting the ambiguous case
  # where the route is implemented but has no normal mount to distinguish the
  # two surfaces.
  fail("route is both implemented and provider-blocked: $route")
    if exists $blockers{$route} && !exists $normal{$route};
}
for my $route (keys %normal) { fail("normal mount is not implemented: $route") unless exists $implemented{$route}; }

my %differential;
if ($results_dir ne '') {
  opendir my $dir, $results_dir or fail("cannot read differential results: $!");
  my @files = sort grep { /\.jsonl?$/ && -f "$results_dir/$_" } readdir $dir;
  closedir $dir;
  for my $file (@files) {
    open my $fh, '<', "$results_dir/$file" or fail("cannot read $file: $!");
    my @documents;
    if ($file =~ /\.jsonl$/) {
      @documents = <$fh>;
    } else {
      local $/;
      @documents = (<$fh>);
    }
    close $fh;
    for my $document (@documents) {
      next if $document =~ /^\s*$/;
      my $record = eval { decode_json($document) };
      fail("invalid JSON in $file: $@") if $@ || ref($record) ne 'HASH';
      my $route = ref($record->{route}) eq 'HASH' ? $record->{route} : $record;
      next unless defined $route->{method} && defined $route->{path};
      my $identity = key($route->{method}, $route->{path});
      fail("duplicate differential result for $route->{method} $route->{path}") if exists $differential{$identity};
      $differential{$identity} = $record;
    }
  }
}

open my $inventory, '<', $inventory_path or fail("cannot read Go inventory: $!");
my (%seen, %classes, %results);
my $total = 0;
my $verified = 0;
my $transport_boundary_verified = 0;
while (my $line = <$inventory>) {
  chomp $line; next if $line eq '';
  my @fields = split /\t/, $line, -1;
  fail("malformed Go inventory at line $.") unless @fields == 3 && !grep { $_ eq '' } @fields;
  my ($method, $path, $handler) = @fields;
  my $route = key($method, $path);
  fail("duplicate Go route identity: $method $path") if $seen{$route}++;
  my ($class, $reason);
  if (exists $stubs{$route}) { $class = 'legacy-501'; $reason = $stubs{$route}->[6] // 'legacy explicit 501 stub'; }
  elsif (exists $shells{$route}) {
    $class = 'mounted-fail-closed-shell';
    $reason = 'ordinary-listener compatibility shell; production adapter is ' . $shells{$route}->[4] . '; real adapter and capability test required for implementation credit';
  }
  elsif (exists $implemented{$route}) {
    $class = 'differential-candidate';
    $reason = 'Rust implementation ledger entry; scenario differential evidence required';
    $reason .= '; test-instance adapter is fail-closed' if exists $blockers{$route};
  }
  elsif (exists $blockers{$route}) { $class = 'provider-blocked'; $reason = $blockers{$route}->[3] // 'test-instance adapter is fail-closed'; }
  else {
    fail("unclassified Go route: $method $path") if $strict;
    $class = 'static-only';
    $reason = exists $frozen{$route} ? 'frozen Go route without a Rust implementation ledger entry' : 'current Go route outside frozen route ledger';
  }
  my $rust_normal = exists $shells{$route} ? 'fail-closed-compatibility-shell' : exists $normal{$route} ? 'mounted' : 'unmounted';
  my $rust_test = ($class eq 'provider-blocked' || $class eq 'mounted-fail-closed-shell') ? 'fail-closed' : $class eq 'legacy-501' ? 'legacy-501' :
    $class eq 'differential-candidate' ? ($rust_normal eq 'mounted' ? 'also-normal-mounted' : 'candidate-only') :
    exists $frozen{$route} ? 'static-source-candidate-unverified' : 'absent';
  my $diff = $differential{$route};
  my $is_verified = $diff && $diff->{differential_verified} ? 1 : 0;
  my $is_transport_boundary_verified = $diff && $diff->{transport_boundary_verified} ? 1 : 0;
  my $result = $class eq 'differential-candidate' && $is_verified ? 'scenario-differential-verified' :
    $class eq 'differential-candidate' ? 'differential-pending' : 'no-parity-claim';
  $verified++ if $is_verified && $class eq 'differential-candidate';
  $transport_boundary_verified++ if $is_transport_boundary_verified;
  $classes{$class}++; $results{$result}++; $total++;
  print encode_json({
    record_type => 'route', method => $method, path => $path, go_handler => $handler, class => $class,
    rust_normal => $rust_normal, rust_test_instance => $rust_test, result => $result,
    evidence => { frozen_go => exists $frozen{$route} ? true : false, rust_implemented => exists $implemented{$route} ? $implemented{$route}->[2] : undef, normal_mount => exists $normal{$route} ? $normal{$route}->[2] : undef, mounted_fail_closed_shell => exists $shells{$route} ? $shells{$route}->[3] : undef, production_blocker_adapter => exists $shells{$route} ? $shells{$route}->[4] : undef, provider_blocked_test_instance => exists $blockers{$route} ? true : false, reason => $reason },
    differential_verified => $is_verified ? true : false,
    transport_boundary_verified => $is_transport_boundary_verified ? true : false,
    differential_scope => $diff && exists $diff->{differential_scope} ? $diff->{differential_scope} : undef,
    differences => $diff ? $diff->{differences} : undef,
    mismatch_names => $diff && exists $diff->{mismatch_names} ? $diff->{mismatch_names} : [],
  }), "\n";
}
close $inventory;
for my $route (keys %differential) { fail("differential result has no current Go route: $route") unless $seen{$route}; }
print encode_json({
  record_type => 'summary', total => $total, class_counts => \%classes, result_counts => \%results,
  coverage_complete => $total > 0 ? true : false, differential_candidates => ($classes{'differential-candidate'} // 0),
  differential_verified => $verified,
  transport_boundary_verified => $transport_boundary_verified,
  differential_results_complete => (($classes{'differential-candidate'} // 0) == $verified) ? true : false,
  parity_claimed => false,
  note => 'coverage_complete means every current Go route was classified; it does not assert Rust/Go behavioral parity.',
}), "\n";
PERL
