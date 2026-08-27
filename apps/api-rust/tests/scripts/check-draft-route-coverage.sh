#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../../../.." && pwd -P)
router_root=${DRAFT_ROUTER_ROOT:-"$repo_root/apps/api-rust/src"}
baseline=${DRAFT_BASELINE_PATH:-"$repo_root/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv"}
gate=${DRAFT_GATE_PATH:-"$repo_root/apps/api-rust/tests/fixtures/routes/migration-gate.tsv"}
plan=${DRAFT_PLAN_PATH:-"$repo_root/apps/api-rust/tests/fixtures/routes/migration-plan.tsv"}
expected_baseline_count=${DRAFT_EXPECT_BASELINE_COUNT:-352}
report_missing=${DRAFT_REPORT_MISSING:-0}
require_complete=${DRAFT_REQUIRE_COMPLETE:-0}
outside_allowlist=${DRAFT_OUTSIDE_BASELINE_ALLOWLIST-"$repo_root/apps/api-rust/tests/fixtures/routes/draft-route-completion-allowlist.tsv"}

[[ -d $router_root ]] || { echo "missing Rust router source root: $router_root" >&2; exit 1; }
[[ -f $baseline ]] || { echo "missing frozen legacy route baseline: $baseline" >&2; exit 1; }
[[ -f $gate ]] || { echo "missing migration gate: $gate" >&2; exit 1; }
[[ -f $plan ]] || { echo "missing migration plan: $plan" >&2; exit 1; }
[[ $expected_baseline_count =~ ^[0-9]+$ ]] || {
  echo "DRAFT_EXPECT_BASELINE_COUNT must be a non-negative integer" >&2
  exit 1
}
[[ $report_missing == 0 || $report_missing == 1 ]] || {
  echo "DRAFT_REPORT_MISSING must be 0 or 1" >&2
  exit 1
}
[[ $require_complete == 0 || $require_complete == 1 ]] || {
  echo "DRAFT_REQUIRE_COMPLETE must be 0 or 1" >&2
  exit 1
}
[[ -z $outside_allowlist || -f $outside_allowlist ]] || {
  echo "DRAFT_OUTSIDE_BASELINE_ALLOWLIST must name an existing TSV file" >&2
  exit 1
}
command -v rg >/dev/null || { echo "ripgrep is required" >&2; exit 1; }
command -v perl >/dev/null || { echo "perl is required" >&2; exit 1; }

mapfile -t router_files < <(rg --files -g '*.rs' "$router_root" | LC_ALL=C sort)
[[ ${#router_files[@]} -gt 0 ]] || {
  echo "no Rust source files found below $router_root" >&2
  exit 1
}

perl - "$repo_root" "$baseline" "$gate" "$plan" "$outside_allowlist" "$expected_baseline_count" "$report_missing" "$require_complete" "${router_files[@]}" <<'PERL'
use strict;
use warnings;

my ($repo_root, $baseline_path, $gate_path, $plan_path, $outside_allowlist_path, $expected_baseline_count, $report_missing, $require_complete, @source_files) = @ARGV;
my @methods = qw(get post put delete patch head options trace connect);
my $method_pattern = join '|', @methods;
my $hard_placeholder_pattern = qr{
    \btodo!\s*\(
  | \bunimplemented!\s*\(
  | \bpanic!\s*\(
  | \bplaceholder\b
}ix;
my $not_implemented_pattern = qr{
    StatusCode\s*::\s*NOT_IMPLEMENTED
  | \bnot[_-]?implemented\b
}ix;
my $placeholder_pattern = qr{
    $hard_placeholder_pattern
  |
    $not_implemented_pattern
}ix;

sub fail {
    my ($message) = @_;
    print STDERR "$message\n";
    return 1;
}

sub read_file {
    my ($path) = @_;
    open my $handle, '<', $path or die "cannot read $path: $!\n";
    local $/;
    return <$handle>;
}

# Remove comments without changing byte offsets. Strings remain intact so the
# route path can be extracted from the sanitized source.
sub without_comments {
    my ($source) = @_;
    my $output = '';
    my $length = length $source;
    my ($index, $block_depth, $in_line, $in_string, $in_char, $escaped) = (0, 0, 0, 0, 0, 0);
    while ($index < $length) {
        my $char = substr($source, $index, 1);
        my $pair = substr($source, $index, 2);
        if ($in_line) {
            if ($char eq "\n") {
                $in_line = 0;
                $output .= "\n";
            } else {
                $output .= ' ';
            }
            $index++;
            next;
        }
        if ($block_depth) {
            if ($pair eq '/*') {
                $output .= '  ';
                $block_depth++;
                $index += 2;
            } elsif ($pair eq '*/') {
                $output .= '  ';
                $block_depth--;
                $index += 2;
            } else {
                $output .= $char eq "\n" ? "\n" : ' ';
                $index++;
            }
            next;
        }
        if ($in_string || $in_char) {
            $output .= $char;
            if ($escaped) {
                $escaped = 0;
            } elsif ($char eq '\\') {
                $escaped = 1;
            } elsif ($in_string && $char eq '"') {
                $in_string = 0;
            } elsif ($in_char && $char eq "'") {
                $in_char = 0;
            }
            $index++;
            next;
        }
        if ($pair eq '//') {
            $output .= '  ';
            $in_line = 1;
            $index += 2;
        } elsif ($pair eq '/*') {
            $output .= '  ';
            $block_depth = 1;
            $index += 2;
        } else {
            $output .= $char;
            $in_string = 1 if $char eq '"';
            if ($char eq "'") {
                my $rest = substr($source, $index, 8);
                $in_char = 1 if $rest =~ /^'(?:\\.|[^'\\])'/;
            }
            $index++;
        }
    }
    return ($output, $block_depth || $in_string || $in_char ? 0 : 1);
}

sub matching_delimiter {
    my ($text, $opening_index, $open, $close) = @_;
    my $depth = 0;
    my ($in_string, $in_char, $escaped) = (0, 0, 0);
    for (my $index = $opening_index; $index < length($text); $index++) {
        my $char = substr($text, $index, 1);
        if ($in_string || $in_char) {
            if ($escaped) {
                $escaped = 0;
            } elsif ($char eq '\\') {
                $escaped = 1;
            } elsif ($in_string && $char eq '"') {
                $in_string = 0;
            } elsif ($in_char && $char eq "'") {
                $in_char = 0;
            }
            next;
        }
        if ($char eq '"') {
            $in_string = 1;
            next;
        }
        if ($char eq "'") {
            # Rust lifetimes are not character literals. Treat a quote followed
            # by an identifier as a lifetime unless a closing quote is nearby.
            my $rest = substr($text, $index, 8);
            $in_char = 1 if $rest =~ /^'(?:\\.|[^'\\])'/;
            next;
        }
        $depth++ if $char eq $open;
        if ($char eq $close) {
            $depth--;
            return $index if $depth == 0;
        }
    }
    return;
}

sub top_level_comma {
    my ($text, $start, $end) = @_;
    my ($paren, $bracket, $brace) = (0, 0, 0);
    my ($in_string, $escaped) = (0, 0);
    for (my $index = $start; $index < $end; $index++) {
        my $char = substr($text, $index, 1);
        if ($in_string) {
            if ($escaped) {
                $escaped = 0;
            } elsif ($char eq '\\') {
                $escaped = 1;
            } elsif ($char eq '"') {
                $in_string = 0;
            }
            next;
        }
        if ($char eq '"') { $in_string = 1; next; }
        if ($char eq '(') { $paren++; next; }
        if ($char eq ')') { $paren--; next; }
        if ($char eq '[') { $bracket++; next; }
        if ($char eq ']') { $bracket--; next; }
        if ($char eq '{') { $brace++; next; }
        if ($char eq '}') { $brace--; next; }
        return $index if $char eq ',' && !$paren && !$bracket && !$brace;
    }
    return;
}

sub normalize_path {
    my ($path) = @_;
    return if $path !~ m{^/};
    $path =~ s/\{\*([A-Za-z_][A-Za-z0-9_]*)\}/*$1/g;
    $path =~ s/\{([A-Za-z_][A-Za-z0-9_]*)\}/:$1/g;
    return if $path =~ /[{}]/;
    return $path;
}

sub relative_source_path {
    my ($path) = @_;
    $path =~ s{^\Q$repo_root/\E}{};
    return $path;
}

# A Gemini route has both one-segment (`model:action`) and tail forms in Axum,
# whereas Gin recorded one wildcard inventory path.  Count either POST form as
# that one frozen wildcard route. The GET/DELETE tail declarations are explicit
# guards for Gin's one-segment `:model` boundary, not legacy API endpoints, so
# they must never earn static route credit.
sub normalize_candidate_path {
    my ($method, $path) = @_;
    return '/v1/models/:model'
        if $method eq 'DELETE' && $path eq '/v1/models/*path';
    return
        if ($method eq 'GET' || $method eq 'DELETE')
        && $path eq '/v1/models/:model/*tail';
    return '/v1/models/*path'
        if $method eq 'POST'
        && ($path eq '/v1/models/:model' || $path eq '/v1/models/:model/*tail');
    return '/v1beta/models/*path'
        if $method eq 'POST'
        && ($path eq '/v1beta/models/:model' || $path eq '/v1beta/models/:model/*tail');
    return $path;
}

sub is_models_post_alias {
    my ($method, $path) = @_;
    return 0 if $method ne 'POST';
    return $path eq '/v1/models/:model'
        || $path eq '/v1/models/:model/*tail'
        || $path eq '/v1beta/models/:model'
        || $path eq '/v1beta/models/:model/*tail';
}

# The shared `/v1/models/:model` method router is composed in three places:
# the focused compatibility candidate, the normal GET catalogue, and the
# relay's POST/DELETE method router.  These declarations intentionally describe
# one Axum method surface rather than three independently mounted endpoints.
sub is_models_shared_alias {
    return 1 if $_[1] eq '/v1/models/:model'
        && ($_[0] eq 'GET' || $_[0] eq 'DELETE');
    return is_models_post_alias(@_);
}

sub method_calls {
    my ($expression) = @_;
    my @calls;
    my $index = 0;
    while ($index < length($expression)) {
        my $remaining = substr($expression, $index);
        if ($remaining =~ /\A(?:\.\s*)?(?:axum\s*::\s*routing\s*::\s*)?($method_pattern)\s*\(/i) {
            my $method = lc $1;
            my $opening = $index + length($&)- 1;
            my $closing = matching_delimiter($expression, $opening, '(', ')');
            return ([], "unterminated $method method constructor") if !defined $closing;
            my $handler = substr($expression, $opening + 1, $closing - $opening - 1);
            push @calls, [uc($method), $handler];
            $index = $closing + 1;
            next;
        }
        $index++;
    }
    return (\@calls, undef);
}

sub function_matches_pattern {
    my ($raw, $clean, $handler, $pattern, $name_pattern) = @_;
    return 0 if $handler !~ /^\s*(?:[A-Za-z_][A-Za-z0-9_]*::)*([A-Za-z_][A-Za-z0-9_]*)\s*$/;
    my $name = $1;
    return 1 if $name =~ $name_pattern;
    if ($clean =~ /(?:^|[^A-Za-z0-9_])(?:async\s+)?fn\s+\Q$name\E\b/g) {
        my $signature_end = pos($clean);
        my $opening = index($clean, '{', $signature_end);
        return 0 if $opening < 0;
        my $closing = matching_delimiter($clean, $opening, '{', '}');
        return 0 if !defined $closing;
        my $body = substr($raw, $opening, $closing - $opening + 1);
        return $body =~ $pattern ? 1 : 0;
    }
    return 0;
}

my (%baseline, %baseline_handler, %planned_module, %gate_mount, %candidates, %placeholders, %hard_placeholders, %not_implemented, %outside_allowlist);
my $failed = 0;

open my $baseline_handle, '<', $baseline_path or die "cannot read $baseline_path: $!\n";
my $baseline_line = 0;
while (my $line = <$baseline_handle>) {
    $baseline_line++;
    chomp $line;
    $line =~ s/\r$//;
    my @fields = split /\t/, $line, -1;
    if (@fields != 3) {
        $failed |= fail("baseline line $baseline_line: expected 3 tab-separated fields, got " . scalar @fields);
        next;
    }
    my ($method, $path, $legacy_handler) = @fields;
    my $normalized = normalize_path($path);
    if ($method !~ /^(?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE|CONNECT)$/ || !defined $normalized) {
        $failed |= fail("baseline line $baseline_line: invalid method/path $method $path");
        next;
    }
    my $key = "$method\t$normalized";
    $failed |= fail("baseline line $baseline_line: duplicate normalized route $method $normalized") if exists $baseline{$key};
    $baseline{$key} = 1;
    $baseline_handler{$key} = $legacy_handler;
}
close $baseline_handle;
if (scalar(keys %baseline) != $expected_baseline_count) {
    $failed |= fail("expected $expected_baseline_count frozen routes, got " . scalar(keys %baseline));
}

open my $plan_handle, '<', $plan_path or die "cannot read $plan_path: $!\n";
my $plan_header = <$plan_handle>;
if (!defined $plan_header) {
    $failed |= fail('migration plan is empty');
} else {
    chomp $plan_header;
    $plan_header =~ s/\r$//;
    my @header_fields = split /\t/, $plan_header, -1;
    my %column;
    @column{@header_fields} = (0 .. $#header_fields);
    for my $required (qw(method path planned_rust_module)) {
        $failed |= fail("migration plan is missing $required column") if !exists $column{$required};
    }
    my $plan_line = 1;
    while (my $line = <$plan_handle>) {
        $plan_line++;
        chomp $line;
        $line =~ s/\r$//;
        my @fields = split /\t/, $line, -1;
        if (@fields != @header_fields) {
            $failed |= fail("plan line $plan_line: expected " . scalar(@header_fields) . " fields, got " . scalar(@fields));
            next;
        }
        next if !exists $column{method} || !exists $column{path} || !exists $column{planned_rust_module};
        my ($method, $path, $module) = @fields[@column{qw(method path planned_rust_module)}];
        my $normalized = normalize_path($path);
        if (!defined $normalized || $module eq '') {
            $failed |= fail("plan line $plan_line: invalid path or empty planned module for $method $path");
            next;
        }
        my $key = "$method\t$normalized";
        if (exists $planned_module{$key}) {
            $failed |= fail("plan line $plan_line: duplicate normalized route $method $normalized");
            next;
        }
        $planned_module{$key} = $module;
    }
}
close $plan_handle;
for my $key (sort keys %baseline) {
    $failed |= fail("migration plan is missing frozen route " . ($key =~ s/\t/ /r))
        if !exists $planned_module{$key};
}
for my $key (sort keys %planned_module) {
    $failed |= fail("migration plan has non-frozen route " . ($key =~ s/\t/ /r))
        if !exists $baseline{$key};
}

open my $gate_handle, '<', $gate_path or die "cannot read $gate_path: $!\n";
my $header = <$gate_handle>;
if (!defined $header) {
    $failed |= fail('migration gate is empty');
} else {
    chomp $header;
    $header =~ s/\r$//;
    my @header_fields = split /\t/, $header, -1;
    my %column;
    @column{@header_fields} = (0 .. $#header_fields);
    for my $required (qw(method path mount_state)) {
        $failed |= fail("migration gate is missing $required column") if !exists $column{$required};
    }
    my (%seen_gate, $gate_line);
    $gate_line = 1;
    while (my $line = <$gate_handle>) {
        $gate_line++;
        chomp $line;
        $line =~ s/\r$//;
        my @fields = split /\t/, $line, -1;
        if (@fields != @header_fields) {
            $failed |= fail("gate line $gate_line: expected " . scalar(@header_fields) . " fields, got " . scalar(@fields));
            next;
        }
        next if !exists $column{method} || !exists $column{path} || !exists $column{mount_state};
        my ($method, $path, $mount) = @fields[@column{qw(method path mount_state)}];
        my $normalized = normalize_path($path);
        if (!defined $normalized || $mount !~ /^(?:mounted|unmounted)$/) {
            $failed |= fail("gate line $gate_line: invalid path or mount state for $method $path");
            next;
        }
        my $key = "$method\t$normalized";
        $failed |= fail("gate line $gate_line: duplicate normalized route $method $normalized") if $seen_gate{$key}++;
        $gate_mount{$key} = $mount;
    }
}
close $gate_handle;

if ($outside_allowlist_path ne '') {
    open my $allowlist_handle, '<', $outside_allowlist_path
        or die "cannot read $outside_allowlist_path: $!\n";
    my $allowlist_header = <$allowlist_handle>;
    if (!defined $allowlist_header) {
        $failed |= fail('outside-baseline allowlist is empty');
    } else {
        chomp $allowlist_header;
        $allowlist_header =~ s/\r$//;
        my @allowlist_header_fields = split /\t/, $allowlist_header, -1;
        my @required_header = qw(method path category source_path rationale);
        if (join("\t", @allowlist_header_fields) ne join("\t", @required_header)) {
            $failed |= fail('outside-baseline allowlist header must be method, path, category, source_path, rationale');
        }
        my $allowlist_line = 1;
        while (my $line = <$allowlist_handle>) {
            $allowlist_line++;
            chomp $line;
            $line =~ s/\r$//;
            my @fields = split /\t/, $line, -1;
            if (@fields != 5) {
                $failed |= fail("outside-baseline allowlist line $allowlist_line: expected 5 tab-separated fields, got " . scalar @fields);
                next;
            }
            my ($method, $path, $category, $source_path, $rationale) = @fields;
            my $normalized = normalize_path($path);
            if ($method !~ /^(?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE|CONNECT)$/ || !defined $normalized
                || $category !~ /^(?:health|internal|test|fixture|compatibility)$/
                || $source_path !~ m{^apps/api-rust/[A-Za-z0-9_./-]+\.rs$}
                || $source_path =~ m{(?:^|/)\.\.?/}
                || !-f "$repo_root/$source_path"
                || $rationale eq '') {
                $failed |= fail("outside-baseline allowlist line $allowlist_line: invalid method, path, category, source_path, or rationale");
                next;
            }
            my $key = "$method\t$normalized";
            if (exists $baseline{$key}) {
                $failed |= fail("outside-baseline allowlist line $allowlist_line: frozen route must not be allowlisted: " . ($key =~ s/\t/ /r));
                next;
            }
            if (exists $outside_allowlist{$key}) {
                $failed |= fail("outside-baseline allowlist line $allowlist_line: duplicate normalized route " . ($key =~ s/\t/ /r));
                next;
            }
            $outside_allowlist{$key} = {
                category => $category,
                source_path => $source_path,
                rationale => $rationale,
                line => $allowlist_line,
                seen => 0,
            };
        }
    }
    close $allowlist_handle;
}

for my $file (@source_files) {
    my $raw = read_file($file);
    my ($clean, $lexically_complete) = without_comments($raw);
    if (!$lexically_complete) {
        $failed |= fail("$file: unterminated comment or string");
        next;
    }
    pos($clean) = 0;
    while ($clean =~ /\.route\s*\(/g) {
        my $route_start = $-[0];
        my $opening = $+[0] - 1;
        my $closing = matching_delimiter($clean, $opening, '(', ')');
        my $line = 1 + (substr($clean, 0, $route_start) =~ tr/\n/\n/);
        if (!defined $closing) {
            $failed |= fail("$file:$line: unterminated .route(...) declaration");
            last;
        }
        my $comma = top_level_comma($clean, $opening + 1, $closing);
        if (!defined $comma) {
            $failed |= fail("$file:$line: .route(...) has no top-level path/method separator");
            pos($clean) = $closing + 1;
            next;
        }
        my $path_expression = substr($clean, $opening + 1, $comma - $opening - 1);
        my $expression = substr($clean, $comma + 1, $closing - $comma - 1);
        my ($calls, $method_error) = method_calls($expression);
        my $static_path_candidate =
            $path_expression =~ /^\s*"(?:\\.|[^"\\])*"\s*$/s
            || $path_expression =~ /^\s*[A-Za-z_][A-Za-z0-9_]*_PATH\s*$/s;
        if (!$static_path_candidate && !$method_error && !@$calls) {
            # Registry/catalog APIs also expose `.route(source, target)`.
            # They are capability lookups, not Axum route declarations.
            pos($clean) = $closing + 1;
            next;
        }
        my ($raw_path, $route_alias) = (undef, 0);
        if ($path_expression =~ /^\s*"((?:\\.|[^"\\])*)"\s*$/s) {
            $raw_path = $1;
        } elsif ($path_expression =~ /^\s*([A-Za-z_][A-Za-z0-9_]*)\s*$/s) {
            my $constant_name = $1;
            if ($constant_name !~ /_PATH\z/) {
                $failed |= fail("$file:$line: route path identifier must use a *_PATH constant");
            } else {
                my $constant_pattern = qr{
                    \bconst\s+\Q$constant_name\E\s*:\s*&str\s*=\s*"((?:\\.|[^"\\])*)"\s*;
                }x;
                if ($raw =~ $constant_pattern) {
                    $raw_path = $1;
                    $route_alias = 1;
                } else {
                    $failed |= fail("$file:$line: route path constant $constant_name must be a local static string");
                }
            }
        } else {
            $failed |= fail("$file:$line: route path must be a static string literal or a *_PATH constant");
        }
        if (!defined $raw_path) {
            pos($clean) = $closing + 1;
            next;
        }
        my $path = normalize_path($raw_path);
        if (!defined $path) {
            $failed |= fail("$file:$line: unsupported or malformed route path $raw_path");
            pos($clean) = $closing + 1;
            next;
        }
        if ($method_error || !@$calls) {
            $failed |= fail("$file:$line: cannot statically parse route methods" . ($method_error ? ": $method_error" : ''));
            pos($clean) = $closing + 1;
            next;
        }
        my %declaration_methods;
        for my $call (@$calls) {
            my ($method, $handler) = @$call;
            # A *_PATH route is an explicitly named non-owning split mount.
            # Its owning route must still be emitted through a literal path in
            # this source tree; skipping aliases keeps read-only/state-specific
            # mounts from being mistaken for duplicate production ownership.
            next if $route_alias;
            my $candidate_path = normalize_candidate_path($method, $path);
            next if !defined $candidate_path;
            if ($declaration_methods{$method}++) {
                $failed |= fail("$file:$line: ambiguous repeated $method method for $candidate_path");
                next;
            }
            my $key = "$method\t$candidate_path";
            if (exists $candidates{$key}) {
                next if is_models_shared_alias($method, $path);
                $failed |= fail("$file:$line: duplicate normalized route $method $candidate_path (first at $candidates{$key})");
                next;
            }
            $candidates{$key} = relative_source_path($file) . ":$line";
            my $route_source = substr($raw, $route_start, $closing - $route_start + 1);
            $hard_placeholders{$key} = 1
                if $handler =~ $hard_placeholder_pattern
                || $route_source =~ $hard_placeholder_pattern
                || function_matches_pattern(
                    $raw,
                    $clean,
                    $handler,
                    $hard_placeholder_pattern,
                    # `todo` is also a domain noun (`get_todos`). Real `todo!`
                    # macros are still detected in the handler expression/body.
                    qr/(?:placeholder|panic)/i,
                );
            $not_implemented{$key} = 1
                if $handler =~ $not_implemented_pattern
                || $route_source =~ $not_implemented_pattern
                || function_matches_pattern(
                    $raw,
                    $clean,
                    $handler,
                    $not_implemented_pattern,
                    qr/not[_-]?implemented/i,
                );
            $placeholders{$key} = 1 if $hard_placeholders{$key} || $not_implemented{$key};
        }
        pos($clean) = $closing + 1;
    }
}

exit 1 if $failed;

my (@outside, @approved_outside, @placeholder_routes, @legacy_stub_routes, @missing_routes);
my ($frozen_matches, $mounted) = (0, 0);
for my $key (sort keys %candidates) {
    if ($baseline{$key}) {
        $frozen_matches++;
        $mounted++ if ($gate_mount{$key} // '') eq 'mounted';
    } else {
        my $allowlisted = $outside_allowlist{$key};
        my ($source_path) = $candidates{$key} =~ /^(.*):\d+$/;
        if (!$allowlisted) {
            push @outside, "$key\t$candidates{$key}";
        } else {
            $allowlisted->{seen} = 1;
            if ($allowlisted->{source_path} ne $source_path) {
                push @outside, "$key\t$candidates{$key}\tallowlist source mismatch: expected $allowlisted->{source_path}";
            } else {
                push @approved_outside, "$key\t$candidates{$key}\t$allowlisted->{category}\t$allowlisted->{rationale}";
            }
        }
    }
    if ($placeholders{$key}) {
        my $is_frozen_legacy_stub = !$hard_placeholders{$key}
            && $not_implemented{$key}
            && ($baseline_handler{$key} // '') =~ /(?:^|\.)RelayNotImplemented$/;
        if ($is_frozen_legacy_stub) {
            push @legacy_stub_routes, "$key\t$candidates{$key}";
        } else {
            push @placeholder_routes, "$key\t$candidates{$key}";
        }
    }
}
for my $key (sort keys %baseline) {
    push @missing_routes, "$key\t$baseline_handler{$key}" if !exists $candidates{$key};
}
for my $key (sort keys %outside_allowlist) {
    my $entry = $outside_allowlist{$key};
    $failed |= fail("outside-baseline allowlist line $entry->{line}: route is not emitted by the static scanner: " . ($key =~ s/\t/ /r))
        if !$entry->{seen};
}
exit 1 if $failed;

for my $entry (@outside) {
    my ($method, $path, $source, $reason) = split /\t/, $entry, 4;
    print "draft route outside frozen baseline: $method $path ($source)" . (defined $reason ? " [$reason]" : '') . "\n";
}
for my $entry (@approved_outside) {
    my ($method, $path, $source, $category, $rationale) = split /\t/, $entry, 5;
    print "draft route approved outside frozen baseline: $method $path category=$category source=$source rationale=$rationale\n";
}
for my $entry (@placeholder_routes) {
    my ($method, $path, $source) = split /\t/, $entry, 3;
    print "draft route placeholder: $method $path ($source)\n";
}
for my $entry (@legacy_stub_routes) {
    my ($method, $path, $source) = split /\t/, $entry, 3;
    print "draft route frozen legacy stub: $method $path ($source)\n";
}
if ($report_missing) {
    my %missing_by_module;
    for my $entry (@missing_routes) {
        my ($method, $path, $handler) = split /\t/, $entry, 3;
        my $key = "$method\t$path";
        my $module = $planned_module{$key};
        $missing_by_module{$module}++;
        print "draft route missing: $method $path module=$module legacy=$handler\n";
    }
    for my $module (sort keys %missing_by_module) {
        print "draft route missing group: module=$module count=$missing_by_module{$module}\n";
    }
}
printf "draft route coverage: candidate-method-paths=%d frozen-matches=%d frozen-total=%d missing=%d mounted=%d placeholders=%d legacy-stubs=%d outside-baseline=%d approved-outside-baseline=%d\n",
    scalar(keys %candidates), $frozen_matches, scalar(keys %baseline),
    scalar(@missing_routes), $mounted, scalar(@placeholder_routes),
    scalar(@legacy_stub_routes), scalar(@outside), scalar(@approved_outside);
print "draft coverage is static candidate evidence only; it is not differential verification, migration credit, or production ownership\n";
if ($require_complete) {
    my $complete = !@missing_routes
        && !@placeholder_routes
        && !@legacy_stub_routes
        && $mounted == scalar(keys %baseline);
    if (!$complete) {
        printf STDERR "draft completion gate failed: missing=%d placeholders=%d legacy-stubs=%d mounted=%d required-mounted=%d\n",
            scalar(@missing_routes), scalar(@placeholder_routes), scalar(@legacy_stub_routes),
            $mounted, scalar(keys %baseline);
        exit 1;
    }
    printf "draft completion gate passed: missing=0 placeholders=0 legacy-stubs=0 mounted=%d\n",
        $mounted;
}
PERL
