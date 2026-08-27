#!/usr/bin/env perl
use strict;
use warnings;

my ($source_path, $wanted_method, $wanted_path) = @ARGV;
die "usage: $0 SOURCE METHOD ROUTE_PATH\n" if !defined $wanted_path || @ARGV != 3;
die "invalid route method: $wanted_method\n"
    if $wanted_method !~ /\A(?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE|CONNECT)\z/;
die "route path must start with /: $wanted_path\n" if $wanted_path !~ m{\A/};

open my $source_handle, '<', $source_path or die "cannot read $source_path: $!\n";
local $/;
my $raw = <$source_handle>;
close $source_handle;

sub without_comments {
    my ($source) = @_;
    my $output = '';
    my ($index, $block_depth, $in_line, $in_string, $in_char, $escaped) = (0, 0, 0, 0, 0, 0);
    while ($index < length $source) {
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
                $in_char = 1 if $rest =~ /\A'(?:\\.|[^'\\])'/;
            }
            $index++;
        }
    }
    die "unterminated comment or literal in $source_path\n"
        if $block_depth || $in_string || $in_char;
    return $output;
}

sub matching_delimiter {
    my ($text, $opening_index, $open, $close) = @_;
    my $depth = 0;
    my ($in_string, $in_char, $escaped) = (0, 0, 0);
    for (my $index = $opening_index; $index < length $text; $index++) {
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
            my $rest = substr($text, $index, 8);
            $in_char = 1 if $rest =~ /\A'(?:\\.|[^'\\])'/;
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
    my ($paren, $bracket, $brace, $in_string, $escaped) = (0, 0, 0, 0, 0);
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

sub method_calls {
    my ($expression) = @_;
    my @calls;
    my $index = 0;
    while ($index < length $expression) {
        my $remaining = substr($expression, $index);
        if ($remaining =~ /\A(?:\.\s*)?(?:axum\s*::\s*routing\s*::\s*)?(get|post|put|delete|patch|head|options|trace|connect)\s*\(/i) {
            my $method = uc $1;
            my $opening = $index + length($&) - 1;
            my $closing = matching_delimiter($expression, $opening, '(', ')');
            die "unterminated $method method constructor for $wanted_method $wanted_path\n"
                if !defined $closing;
            push @calls, [$method, substr($expression, $opening + 1, $closing - $opening - 1)];
            $index = $closing + 1;
            next;
        }
        $index++;
    }
    return @calls;
}

my $clean = without_comments($raw);
my @handlers;
pos($clean) = 0;
while ($clean =~ /\.\s*route\s*\(/g) {
    my $opening = pos($clean) - 1;
    my $closing = matching_delimiter($clean, $opening, '(', ')');
    die "unterminated route declaration in $source_path\n" if !defined $closing;
    my $comma = top_level_comma($clean, $opening + 1, $closing);
    die "route declaration lacks a handler in $source_path\n" if !defined $comma;
    my $path_expression = substr($clean, $opening + 1, $comma - $opening - 1);
    if ($path_expression =~ /\A\s*"([^"\\]*)"\s*\z/ && $1 eq $wanted_path) {
        my $method_expression = substr($clean, $comma + 1, $closing - $comma - 1);
        for my $call (method_calls($method_expression)) {
            push @handlers, $call->[1] if $call->[0] eq $wanted_method;
        }
    }
    pos($clean) = $closing + 1;
}

die "expected exactly one $wanted_method $wanted_path handler in $source_path, found " . scalar(@handlers) . "\n"
    if @handlers != 1;

my $stub_pattern = qr{
    StatusCode\s*::\s*NOT_IMPLEMENTED
  | not[_ -]?implemented
  | (?<![0-9])501(?![0-9])
}ix;
my $handler = $handlers[0];
my $is_stub = $handler =~ $stub_pattern ? 1 : 0;

if (!$is_stub && $handler =~ /\A\s*(?:[A-Za-z_][A-Za-z0-9_]*::)*([A-Za-z_][A-Za-z0-9_]*)\s*\z/) {
    my $name = $1;
    if ($clean =~ /(?:\A|[^A-Za-z0-9_])(?:async\s+)?fn\s+\Q$name\E\b/g) {
        my $signature_end = pos($clean);
        my $opening = index($clean, '{', $signature_end);
        die "handler $name has no body in $source_path\n" if $opening < 0;
        my $closing = matching_delimiter($clean, $opening, '{', '}');
        die "handler $name has an unterminated body in $source_path\n" if !defined $closing;
        my $body = substr($clean, $opening, $closing - $opening + 1);
        $is_stub = 1 if $body =~ $stub_pattern;
    }
}

print $is_stub ? "stub\n" : "complete\n";
