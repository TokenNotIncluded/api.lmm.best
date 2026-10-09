#!/usr/bin/env python3
"""Render a local static frontend overlay for a captured whole-origin proxy.

This command never contacts a server or changes the captured input. The caller
must verify the frontend source/artifact and review the output before deployment.
"""
import argparse
import hashlib
import os
from pathlib import Path
import re
import stat

BACKEND_ROOTS = {'api', 'assets', 'mcp', 'v1', 'v1beta', 'typesafe', 'pg', 'mj', 'suno', 'kling', 'jimeng'}


def is_backend_path(path):
    parts = path.strip('/').split('/')
    return (parts[0] in BACKEND_ROOTS or (len(parts) >= 2 and parts[1] == 'mj')
            or path.rstrip('/') in {'/dashboard/billing/subscription', '/dashboard/billing/usage'}
            or (len(parts) > 1 and parts[0] == 'scripts'))


def regular_bytes(path):
    path = Path(path)
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_mode & 0o022:
        raise ValueError('input must be a protected single-link regular file')
    return path.read_bytes()


def route_patterns(route_tree):
    header = 'export interface FileRoutesByFullPath {\n'
    if route_tree.count(header) != 1:
        raise ValueError('expected one generated frontend route interface')
    section = route_tree.split(header, 1)[1].split('\n}', 1)[0]
    paths = re.findall(r"^  '([^']+)':", section, re.M)
    if not paths or '/' not in paths or len(paths) != len(set(paths)):
        raise ValueError('invalid generated route paths')
    for path in paths:
        if not re.fullmatch(r'/(?:[A-Za-z0-9_$-]+/?)*', path):
            raise ValueError('unsupported generated route path')
        if is_backend_path(path) or (path != '/' and path.split('/')[1].startswith('$')):
            raise ValueError('generated frontend route overlaps a backend namespace')
    literal = lambda value: re.escape(value).replace(r'\-', '-')
    simple = sorted({path.strip('/') for path in paths if '$' not in path and path != '/'})
    dynamic = sorted({path.strip('/') for path in paths if '$' in path and '/claim/' not in path})
    return (
        '^/(?:' + '|'.join(literal(path) for path in simple) + ')/?$',
        '^/(?:' + '|'.join('/'.join('[^/]+' if part.startswith('$') else literal(part)
                                  for part in path.split('/')) for path in dynamic) + ')/?$',
    )


def render(original, route_tree, public_names):
    fallback = b'location / {\n    error_page 418 = @lmm_api_backend;\n    return 418;\n}\n'
    if original.count(b'location @lmm_api_backend {') != 1 or original.count(fallback) != 1:
        raise ValueError('expected the captured named backend and exact forwarding fallback')
    if re.search(rb'(?m)^\s*(?:root|alias)\s|location[^\n]*/(?:static|index\.html)', original):
        raise ValueError('captured proxy already has a frontend source; review that configuration directly')
    simple, dynamic = route_patterns(route_tree)
    guard = '    error_page 418 = @lmm_api_backend;\n    if ($request_method !~ "^(GET|HEAD)$") { return 418; }\n'
    entry = guard + '    try_files /index.html =404;\n    add_header Cache-Control "no-cache, must-revalidate" always;\n'
    overlay = '''# Frontend files use the signed publisher; backend requests retain the captured hop.
include /etc/nginx/lmm-api-mime.types;
root /srv/lmm-api-frontend/current;

location ^~ /static/ {
''' + guard + '''    alias /srv/lmm-api-frontend/assets/;
    add_header Cache-Control "public, max-age=31536000, immutable" always;
}
'''
    # Go's dynamic /:mode/mj namespace can collide with one-segment UI params.
    overlay += '''location ~ ^/[^/]+/mj(?:/|$) {
    error_page 418 = @lmm_api_backend;
    return 418;
}
'''
    overlay += 'location = /store/manage {\n    error_page 418 = @lmm_api_backend;\n    if ($request_method !~ "^(GET|HEAD)$") { return 418; }\n    access_log off;\n    try_files /index.html =404;\n    add_header Cache-Control "no-store" always;\n    add_header Referrer-Policy "no-referrer" always;\n    add_header Content-Security-Policy $lmm_extore_callback_csp always;\n}\n'
    for selector in ('= /index.html', '= /', '~ "' + simple + '"', '~ "' + dynamic + '"'):
        overlay += 'location ' + selector + ' {\n' + entry + '}\n'
    overlay += 'location ~ "^/store/claim/[A-Za-z0-9_-]{43}/?$" {\n' + guard + '''    access_log off;
    try_files /index.html =404;
    add_header Cache-Control "no-store" always;
    add_header Referrer-Policy "no-referrer" always;
    add_header X-Content-Type-Options nosniff always;
}
location ^~ /legal/ {
''' + guard + '''    try_files $uri =404;
    add_header Cache-Control "no-cache, must-revalidate" always;
}
'''
    for name in sorted(set(public_names) - {'index.html'}):
        if not re.fullmatch(r'[A-Za-z0-9._-]+', name) or name in ('.', '..'):
            raise ValueError('unsupported public file name')
        if is_backend_path('/' + name) or name == 'scripts':
            raise ValueError('public file overlaps a backend namespace')
        content_type = ('    types { }\n    default_type text/plain;\n    charset utf-8;\n'
                        '    add_header X-Content-Type-Options nosniff always;\n'
                        if name == 'AGENTS.md' else '')
        overlay += 'location = /' + name + ' {\n' + guard + content_type + '    try_files $uri =404;\n    add_header Cache-Control "no-cache, must-revalidate" always;\n}\n'
    return original.replace(fallback, overlay.encode() + b'\n' + fallback, 1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--locations', required=True, type=Path)
    parser.add_argument('--route-tree', required=True, type=Path)
    parser.add_argument('--public-tree', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    if args.public_tree.is_symlink() or not args.public_tree.is_dir():
        raise ValueError('public tree must be a real verified frontend directory')
    public_names = []
    for path in args.public_tree.iterdir():
        info = path.lstat()
        if stat.S_ISLNK(info.st_mode) or not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)):
            raise ValueError('unsupported public tree entry')
        if stat.S_ISREG(info.st_mode):
            public_names.append(path.name)
    candidate = render(regular_bytes(args.locations), regular_bytes(args.route_tree).decode(), public_names)
    descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'wb') as output:
        output.write(candidate)
        output.flush()
        os.fsync(output.fileno())
    print('rendered sha256=' + hashlib.sha256(candidate).hexdigest() + ' bytes=' + str(len(candidate)))


if __name__ == '__main__':
    main()
