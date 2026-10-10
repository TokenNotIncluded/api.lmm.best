#!/usr/bin/env python3
"""Build a dependency-free source slice; never change project go.mod or fetch tools."""
import argparse, hashlib, json, os, pathlib, shutil, subprocess

BASE = '72667564c0431754d4856dc2e0db55f360bd2745'
REL = pathlib.Path('apps/lmm-extensions/internal/modules/toolmarket')
BLOBS = {'mcp.go':'b15f8540b7372052de0f8de96db23e9892cece85',
         'schema.go':'90d97e642f0097eddf373a45749381616d96697d',
         'network.go':'509bd77eb65fc3bbcec4803b191fcdfeab4f67c5'}

def blob(data):
    return hashlib.sha1(b'blob '+str(len(data)).encode()+b'\0'+data).hexdigest()

def main():
    p=argparse.ArgumentParser()
    p.add_argument('--baseline-source', type=pathlib.Path, required=True)
    p.add_argument('--candidate-source', type=pathlib.Path, required=True)
    p.add_argument('--work', type=pathlib.Path, required=True)
    a=p.parse_args(); here=pathlib.Path(__file__).resolve().parent
    a.work.mkdir(parents=True, exist_ok=True)
    env=dict(os.environ, GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off', GOWORK='off', GO111MODULE='on', CGO_ENABLED='0', GOMAXPROCS='2')
    provenance={'base': BASE, 'scope':'source-slice; NOT full host / ledger / model', 'go_version':subprocess.check_output(['go','version'],env=env,text=True).strip(), 'blobs':BLOBS}
    for name, expected in BLOBS.items():
        if blob((a.baseline_source/REL/name).read_bytes()) != expected:
            raise ValueError(name + ' does not match pinned baseline')
    extractor=a.work/'extract'
    subprocess.run(['go','build','-trimpath','-o',str(extractor),str(here/'extract.go')],env=env,check=True,timeout=90)
    for variant, source in [('baseline', a.baseline_source), ('candidate', a.candidate_source)]:
        dest=a.work/variant; dest.mkdir(exist_ok=True)
        def extract(name, names):
            f=(source/REL/name) if name=='mcp.go' else a.baseline_source/REL/name
            return subprocess.check_output([str(extractor), str(f), names],text=True)
        funcs='rpcEnvelope,mcpSession,rpc,rpcResult'+(',checkedRPCResult' if variant=='candidate' else '')
        text='package toolmarket\nimport ("bufio";"bytes";"context";"encoding/json";"fmt";"io";"mime";"net/http";"strings")\n'+extract('mcp.go',funcs)
        (dest/'mcp.go').write_text(text)
        (dest/'schema.go').write_text('package toolmarket\nimport ("bytes";"encoding/json";"io")\n'+extract('schema.go','uniqueJSON'))
        shutil.copyfile(a.baseline_source/REL/'network.go', dest/'network.go')
        # Exact error texts and constants from types.go at BASE; no Funds or
        # Authority implementations are installed by this test fixture.
        (dest/'scope.go').write_text('''package toolmarket
import "errors"
const maxBody = 1 << 20
const ProtocolVersion = "2025-11-25"
var ErrInvalid=errors.New("invalid_request")
var ErrDenied=errors.New("forbidden")
var ErrMissing=errors.New("not_found")
var ErrUpstream=errors.New("upstream_error")
var ErrAuth=errors.New("authorization_required")
var ErrLimit=errors.New("rate_limited")
''')
        (dest/'go.mod').write_text('module dp26-source-slice\n\ngo 1.23.0\n')
        shutil.copyfile(a.candidate_source/REL/'mcp_runtime_test.go',dest/'rpc_runtime_test.go')
        shutil.copyfile(here/'load_test.go',dest/'load_test.go')
        subprocess.run(['gofmt','-w',str(dest)],check=True,timeout=10)
        subprocess.run(['go','test','-c','-trimpath','-o',str(a.work/(variant+'.test')),'.'],cwd=dest,env=env,check=True,timeout=120)
        provenance[variant]={'mcp_git_blob':blob((source/REL/'mcp.go').read_bytes()),'binary_sha256':hashlib.sha256((a.work/(variant+'.test')).read_bytes()).hexdigest()}
    (a.work/'provenance.json').write_text(json.dumps(provenance,indent=2)+'\n')
    print(json.dumps(provenance,indent=2))
if __name__=='__main__': main()
