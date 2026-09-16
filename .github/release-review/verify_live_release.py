"""Read-only comparison of the public site with a verified Web release archive."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import PurePosixPath
import sys
import tarfile
import time
import urllib.parse
import urllib.request


def load_public_probe(path):
    spec = importlib.util.spec_from_file_location('public_release_probe', path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def archive_file(archive, name):
    member = archive.getmember(name)
    if not member.isfile() or member.size > 8 * 1024 * 1024:
        raise ValueError('release member is not a bounded regular file: ' + name)
    source = archive.extractfile(member)
    if source is None:
        raise ValueError('release member has no content: ' + name)
    with source:
        data = source.read(8 * 1024 * 1024 + 1)
    if len(data) != member.size:
        raise ValueError('release member size mismatch: ' + name)
    return data


def verify_identity(probe, archive, fetch, expected_revision, backend_version, web_version):
    names = archive.getnames()
    if len(names) != len(set(names)):
        raise ValueError('duplicate archive member names')
    revision = archive_file(archive, 'REVISION').decode('ascii').strip()
    if revision != expected_revision:
        raise ValueError('release revision mismatch')
    expected_index = archive_file(archive, 'dist/index.html')
    parser = probe.Assets()
    parser.feed(expected_index.decode('utf-8'))
    expected_urls = set()
    for raw in parser.urls:
        url = urllib.parse.urljoin(probe.ORIGIN + '/', raw)
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme == 'https' and parsed.netloc == urllib.parse.urlsplit(probe.ORIGIN).netloc:
            expected_urls.add(urllib.parse.urlunsplit(parsed._replace(fragment='')))
    if not expected_urls:
        raise ValueError('release index has no same-origin JS/CSS entries')
    cache = {}
    def cached_fetch(url):
        if url not in cache:
            cache[url] = fetch(url)
        return cache[url]
    result = probe.verify(cached_fetch, backend_version)
    for path in ('/', '/login', '/console'):
        response = cached_fetch(probe.ORIGIN + path)
        if response.body != expected_index:
            raise ValueError('public HTML does not match signed release: ' + path)
    identities = []
    for url in sorted(expected_urls):
        path = urllib.parse.urlsplit(url).path
        if not path.startswith('/') or '..' in PurePosixPath(path).parts:
            raise ValueError('unsafe release entry path')
        expected = archive_file(archive, 'dist' + path)
        actual = cached_fetch(url).body
        if actual != expected:
            raise ValueError('public asset does not match signed release: ' + path)
        identities.append({'path': path, 'sha256': hashlib.sha256(actual).hexdigest()})
    result.update({'web_version':web_version,'revision':revision,'index_sha256':hashlib.sha256(expected_index).hexdigest(),'verified_entries':identities})
    return result


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive',required=True)
    parser.add_argument('--probe',required=True)
    parser.add_argument('--revision',required=True)
    parser.add_argument('--backend-version',required=True)
    parser.add_argument('--web-version',required=True)
    args=parser.parse_args()
    probe=load_public_probe(args.probe)
    opener=urllib.request.build_opener(probe.SameOriginRedirect())
    deadline=time.monotonic()+120
    def fetch(url):
        remaining=deadline-time.monotonic()
        if remaining<=0: raise TimeoutError('public acceptance deadline exceeded')
        request=urllib.request.Request(url,headers={'User-Agent':'LMM-Signed-Release-Acceptance/1.0','Cache-Control':'no-cache','Accept-Encoding':'identity'})
        with opener.open(request,timeout=min(15,remaining)) as response:
            body=response.read(probe.MAX_BODY+1)
            if len(body)>probe.MAX_BODY: raise ValueError('public response exceeds limit')
            return probe.Response(response.status,response.headers.get('Content-Type',''),body)
    with tarfile.open(args.archive,'r:gz') as archive:
        result=verify_identity(probe,archive,fetch,args.revision,args.backend_version,args.web_version)
    print(json.dumps(result,sort_keys=True))

if __name__=='__main__':
    main()
