#!/usr/bin/env python3
"""Isolated gRPC protocol experiment. This is NOT the Rust core or its ledger."""
from __future__ import annotations
import concurrent.futures as futures
import hashlib
import hmac
import json
import os
from pathlib import Path
import signal
import socket
import sqlite3
import ssl
import statistics
import subprocess
import sys
import tempfile
import threading
import time
from datetime import datetime, timedelta, timezone

import grpc
import psutil
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID

CONTROL = '/lmm.core.v1.CoreControl/'
PROBE = '/dp24.lab.Probe/'  # Deliberately NOT a production money protocol.
MAX_MESSAGE = 65536
SERVICE_TOKEN = 'dp24_' + 'test_only_' * 4
SESSION = 'lmms_' + 'S' * 40
API_KEY = 'lmmk_' + 'K' * 40
IDENTITY = 'spiffe://lmm.test/extensions/e1'
METHODS = [CONTROL+x for x in ('Capabilities','Authorize','ListTeams')] + [PROBE+x for x in ('Charge','Receipt','Sleep','Stream')]


def dump(path: Path, value):
    tmp = path.with_suffix('.tmp')
    tmp.write_text(json.dumps(value, indent=2, sort_keys=True)+'\n')
    os.replace(tmp, path)


def run(*args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=10, **kwargs)


def digest(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def varint(value: int) -> bytes:
    data = bytearray()
    while value > 127:
        data.append((value & 127) | 128)
        value >>= 7
    data.append(value)
    return bytes(data)


def field(number: int, value: int) -> bytes:
    return varint(number << 3) + varint(value)


def account() -> bytes:
    data = field(1, 1) + field(2, 101)
    return varint((5 << 3) | 2) + varint(len(data)) + data


def pki(root: Path):
    core = root/'core'
    clients = root/'clients'
    core.mkdir(mode=0o700)
    clients.mkdir(mode=0o700)
    now = datetime.now(timezone.utc)
    ca_key = ec.generate_private_key(ec.SECP256R1())
    ca_name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, 'DP24 disposable test CA')])
    ca = (x509.CertificateBuilder().subject_name(ca_name).issuer_name(ca_name)
          .public_key(ca_key.public_key()).serial_number(x509.random_serial_number())
          .not_valid_before(now-timedelta(minutes=1)).not_valid_after(now+timedelta(hours=1))
          .add_extension(x509.BasicConstraints(ca=True, path_length=0), critical=True)
          .add_extension(x509.KeyUsage(digital_signature=False, content_commitment=False, key_encipherment=False, data_encipherment=False, key_agreement=False, key_cert_sign=True, crl_sign=True, encipher_only=False, decipher_only=False), critical=True)
          .add_extension(x509.SubjectKeyIdentifier.from_public_key(ca_key.public_key()), critical=False)
          .sign(ca_key, hashes.SHA256()))
    (root/'ca.pem').write_bytes(ca.public_bytes(serialization.Encoding.PEM))
    pins = {}
    for name in ('a','b','old','new','wrong','expired','short'):
        server = name in ('a','b')
        folder = core if server else clients
        key = ec.generate_private_key(ec.SECP256R1())
        uri = f'spiffe://lmm.test/core/{name}' if server else (IDENTITY if name != 'wrong' else 'spiffe://lmm.test/extensions/not-e1')
        until = now+timedelta(minutes=20)
        start = now-timedelta(minutes=1)
        if name == 'expired':
            until = now-timedelta(seconds=1)
        if name == 'short':
            until = now+timedelta(seconds=6)
        sans = [x509.UniformResourceIdentifier(uri)]
        if server:
            sans.append(x509.DNSName(f'core-{name}.dp24.test'))
        cert = (x509.CertificateBuilder().subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, name)]))
                .issuer_name(ca_name).public_key(key.public_key()).serial_number(x509.random_serial_number())
                .not_valid_before(start).not_valid_after(until)
                .add_extension(x509.SubjectAlternativeName(sans), critical=False)
                .add_extension(x509.AuthorityKeyIdentifier.from_issuer_public_key(ca_key.public_key()), critical=False)
                .add_extension(x509.SubjectKeyIdentifier.from_public_key(key.public_key()), critical=False)
                .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH if server else ExtendedKeyUsageOID.CLIENT_AUTH]), critical=False)
                .sign(ca_key, hashes.SHA256()))
        (folder/f'{name}.pem').write_bytes(cert.public_bytes(serialization.Encoding.PEM))
        (folder/f'{name}.key').write_bytes(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
        os.chmod(folder/f'{name}.key', 0o600)
        pins[name] = digest(cert.public_bytes(serialization.Encoding.DER))
    # CA private key is never persisted. Each run uses a new, disposable CA.
    dump(root/'pins.json', pins)
    with sqlite3.connect(core/'fixture.sqlite') as db:
        db.executescript('''
        PRAGMA journal_mode=WAL;
        CREATE TABLE credentials (digest TEXT PRIMARY KEY, kind INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0);
        CREATE TABLE balance (id INTEGER PRIMARY KEY, amount INTEGER NOT NULL CHECK(amount>=0));
        INSERT INTO balance VALUES(101,10000);
        CREATE TABLE operations (operation_id TEXT PRIMARY KEY, user_id INTEGER NOT NULL, amount INTEGER NOT NULL, status TEXT NOT NULL);
        ''')
        db.executemany('INSERT INTO credentials(digest,kind) VALUES(?,?)', [(digest(SESSION.encode()),1),(digest(API_KEY.encode()),2)])
    return pins


def policy(root, pins, version, peers=('old','new','short'), token=SERVICE_TOKEN, ttl=60, old_until=None):
    now = time.time()
    dump(root/'core/policy.json', {
        'version': version, 'issued_at': now-0.05, 'expires_at': now+ttl-0.05,
        'service_uri': IDENTITY, 'methods': METHODS,
        'tokens': [{'digest':digest(token.encode()),'until':now+ttl}],
        'peers': {pins[x]:(old_until if x=='old' and old_until else now+ttl) for x in peers},
    })


class Fixture:
    def __init__(self, root, node):
        self.root, self.node = root, node
        self.lock = threading.Lock()
        self.active = 0
        self.peak = 0
        self.calls = 0
        self.epoch = 0
        self.cert_cache = {}

    def authorize_service(self, ctx, method, local=False):
        try:
            with (self.root/'core/policy.json').open('rb') as source:
                raw = source.read(65537)
            if len(raw)>65536:
                raise ValueError('oversized policy')
            p = json.loads(raw)
            now = time.time()
            with self.lock:
                if p['version'] < self.epoch:
                    raise ValueError('rollback')
                self.epoch = max(self.epoch,p['version'])
            if not (0 < p['version'] and p['issued_at'] <= now < p['expires_at'] and p['expires_at']-p['issued_at']<=60):
                raise ValueError('stale policy')
        except Exception:
            ctx.abort(grpc.StatusCode.UNAUTHENTICATED,'service policy unavailable or stale')
        values = {}
        for k,v in ctx.invocation_metadata():
            if k in ('authorization','x-lmm-protocol','x-lmm-user-credential'):
                if k in values:
                    ctx.abort(grpc.StatusCode.UNAUTHENTICATED,'duplicate metadata')
                values[k]=v
        token = values.get('authorization','')
        if not token.startswith('Bearer ') or not any(
                hmac.compare_digest(t['digest'],digest(token[7:].encode())) and time.time()<t['until'] for t in p['tokens']):
            ctx.abort(grpc.StatusCode.UNAUTHENTICATED,'invalid service token')
        if values.get('x-lmm-protocol')!='1':
            ctx.abort(grpc.StatusCode.FAILED_PRECONDITION,'unsupported protocol major')
        if method not in p['methods']:
            ctx.abort(grpc.StatusCode.PERMISSION_DENIED,'method not allowed')
        if not local:
            certs = ctx.auth_context().get('x509_pem_cert',[])
            if len(certs)!=1:
                ctx.abort(grpc.StatusCode.UNAUTHENTICATED,'missing verified client certificate')
            pem = certs[0]
            # Certificate parsing may be cached. Its authorization is not cached.
            with self.lock:
                cert = self.cert_cache.get(pem)
                if cert is None:
                    cert = x509.load_pem_x509_certificate(pem)
                    if len(self.cert_cache)>=8:
                        self.cert_cache.clear()
                    self.cert_cache[pem] = cert
            fp = digest(cert.public_bytes(serialization.Encoding.DER))
            try:
                identities=cert.extensions.get_extension_for_class(x509.SubjectAlternativeName).value.get_values_for_type(x509.UniformResourceIdentifier)
            except x509.ExtensionNotFound:
                identities=[]
            now_dt=datetime.now(timezone.utc)
            if identities != [p['service_uri']] or not (cert.not_valid_before_utc<=now_dt<cert.not_valid_after_utc) or not time.time()<p['peers'].get(fp,0):
                ctx.abort(grpc.StatusCode.PERMISSION_DENIED,'service identity expired or revoked')
        return values

    def user(self, ctx, values):
        credential = values.get('x-lmm-user-credential','')
        if (self.root/f'core/offline-{self.node}').exists():
            ctx.abort(grpc.StatusCode.UNAVAILABLE,'fixture authority unavailable')
        try:
            with sqlite3.connect(self.root/'core/fixture.sqlite',timeout=.2) as db:
                row=db.execute('SELECT kind,revoked FROM credentials WHERE digest=?',(digest(credential.encode()),)).fetchone()
        except sqlite3.Error:
            ctx.abort(grpc.StatusCode.UNAVAILABLE,'fixture authority unavailable')
        if not row or row[1]:
            ctx.abort(grpc.StatusCode.UNAUTHENTICATED,'invalid user credential')
        return row[0]

    def unary(self, name, local=False):
        method=(CONTROL if name in ('Capabilities','Authorize','ListTeams') else PROBE)+name
        def call(data,ctx):
            values=self.authorize_service(ctx,method,local)
            with self.lock:
                self.active+=1;self.calls+=1;self.peak=max(self.peak,self.active)
            try:
                if name=='Capabilities':
                    return field(1,1)+field(2,1)
                if name=='Authorize':
                    kind=self.user(ctx,values)
                    return field(1,11 if kind==1 else 12)+field(2,kind)+field(3,101)+field(4,1)+account()
                if name=='ListTeams':
                    self.user(ctx,values)
                    return b''
                self.user(ctx,values)
                req=json.loads(data or b'{}')
                if name=='Sleep':
                    until=time.monotonic()+min(float(req.get('seconds',.2)),2)
                    while time.monotonic()<until:
                        if not ctx.is_active():
                            ctx.abort(grpc.StatusCode.CANCELLED,'cancelled')
                        time.sleep(.005)
                    return b'{}'
                operation=req.get('operation_id','')
                if not isinstance(operation,str) or not 1<=len(operation)<=64:
                    ctx.abort(grpc.StatusCode.INVALID_ARGUMENT,'invalid operation identifier')
                with sqlite3.connect(self.root/'core/fixture.sqlite',timeout=.2) as db:
                    db.execute('BEGIN IMMEDIATE')
                    old=db.execute('SELECT amount,status FROM operations WHERE operation_id=? AND user_id=101',(operation,)).fetchone()
                    if name=='Receipt':
                        result={'operation_id':operation,'status':old[1] if old else 'not_found'}
                    else:
                        amount=req.get('amount')
                        if type(amount) is not int or not 0<amount<=1000:
                            ctx.abort(grpc.StatusCode.INVALID_ARGUMENT,'invalid amount')
                        if old and old[0]!=amount:
                            ctx.abort(grpc.StatusCode.ALREADY_EXISTS,'operation payload changed')
                        if not old:
                            changed=db.execute('UPDATE balance SET amount=amount-? WHERE id=101 AND amount>=?',(amount,amount)).rowcount
                            if changed!=1:
                                ctx.abort(grpc.StatusCode.FAILED_PRECONDITION,'not enough fixture balance')
                            db.execute('INSERT INTO operations VALUES(?,101,?,?)',(operation,amount,'committed'))
                        result={'operation_id':operation,'status':'committed','replayed':bool(old)}
                    db.commit()
                if name=='Charge' and req.get('drop_reply'):
                    ctx.abort(grpc.StatusCode.UNAVAILABLE,'injected lost reply AFTER fixture commit')
                return json.dumps(result).encode()
            finally:
                with self.lock:
                    self.active-=1
                    dump(self.root/f'core/metrics-{self.node}.json',{'calls':self.calls,'peak':self.peak,'active':self.active})
        return call

    def stream(self,data,ctx):
        self.authorize_service(ctx,PROBE+'Stream')
        self.user(ctx,dict(ctx.invocation_metadata()))
        for n in range(30):
            if not ctx.is_active():
                return
            yield json.dumps({'node':self.node,'sequence':n}).encode()
            time.sleep(.03)


def serve(root: Path,node: str,ip: str,port: int):
    deadline=time.monotonic()+10
    while not (root/f'ready-{node}').exists():
        if time.monotonic()>deadline:
            raise RuntimeError('network setup timed out')
        time.sleep(.02)
    safety = json.loads((root/'isolation.json').read_text())
    if (os.readlink('/proc/self/ns/net') in (safety['outer'], safety['controller'])
            or 'default' in run('ip', 'route', 'show', 'table', 'all').stdout
            or node not in ('a', 'b')
            or ip != ('10.203.24.2' if node == 'a' else '10.203.25.2')
            or port not in (51051, 51053)):
        raise RuntimeError('refusing to bind outside the disposable test network')
    fixture=Fixture(root,node)
    options=[('grpc.max_receive_message_length',MAX_MESSAGE),('grpc.max_send_message_length',MAX_MESSAGE),
             ('grpc.max_concurrent_streams',8),('grpc.max_metadata_size',8192),('grpc.so_reuseport',0)]
    server=grpc.server(futures.ThreadPoolExecutor(max_workers=8),maximum_concurrent_rpcs=8,options=options)
    server.add_generic_rpc_handlers((grpc.method_handlers_generic_handler('lmm.core.v1.CoreControl',{
        n:grpc.unary_unary_rpc_method_handler(fixture.unary(n)) for n in ('Capabilities','Authorize','ListTeams')}),
        grpc.method_handlers_generic_handler('dp24.lab.Probe',{
            **{n:grpc.unary_unary_rpc_method_handler(fixture.unary(n)) for n in ('Charge','Receipt','Sleep')},
            'Stream':grpc.unary_stream_rpc_method_handler(fixture.stream)})))
    credentials=grpc.ssl_server_credentials([((root/f'core/{node}.key').read_bytes(),(root/f'core/{node}.pem').read_bytes())],root_certificates=(root/'ca.pem').read_bytes(),require_client_auth=True)
    assert server.add_secure_port(f'{ip}:{port}',credentials)==port
    server.start()
    local=None
    if node=='a':
        local=grpc.server(futures.ThreadPoolExecutor(max_workers=8),maximum_concurrent_rpcs=8,options=options)
        local.add_generic_rpc_handlers((grpc.method_handlers_generic_handler('lmm.core.v1.CoreControl',{
            n:grpc.unary_unary_rpc_method_handler(fixture.unary(n,True)) for n in ('Capabilities','Authorize','ListTeams')}),))
        local.add_insecure_port('unix:'+str(root/'core/pair.sock'))
        local.start()
    dump(root/f'core/started-{node}.json',{'pid':os.getpid(),'namespace':os.readlink('/proc/self/ns/net'),'port':port})
    stop=threading.Event()
    signal.signal(signal.SIGTERM,lambda *_:stop.set())
    stop.wait()
    server.stop(0).wait(2)
    if local:local.stop(0).wait(2)


class Lab:
    def __init__(self,root,out,pins):
        self.root,self.out,self.pins=root,out,pins
        self.nodes={};self.channels=[];self.tests=[];self.version=1
        self.started=time.monotonic()
        self.meta=[('authorization','Bearer '+SERVICE_TOKEN),('x-lmm-protocol','1')]
        self.user_meta=self.meta+[('x-lmm-user-credential',SESSION)]

    def start(self,node,port=51051):
        suffix=24 if node=='a' else 25
        ip=f'10.203.{suffix}.2';iface='dx'+node;peer='dc'+node
        (self.root/f'ready-{node}').unlink(missing_ok=True)
        (self.root/f'core/started-{node}.json').unlink(missing_ok=True)
        log=open(self.out/f'server-{node}.log','a')
        p=subprocess.Popen(['unshare','--net','--mount',sys.executable,__file__,'serve',str(self.root),node,ip,str(port)],stdout=log,stderr=log)
        # unshare starts asynchronously; wait for a distinct namespace before moving interfaces.
        for _ in range(100):
            if p.poll() is not None:raise RuntimeError('fixture process exited')
            if os.readlink(f'/proc/{p.pid}/ns/net')!=os.readlink('/proc/self/ns/net'):break
            time.sleep(.01)
        run('ip','link','add',iface,'type','veth','peer','name',peer)
        run('ip','link','set',peer,'netns',str(p.pid))
        run('ip','addr','add',f'10.203.{suffix}.1/30','dev',iface)
        run('ip','link','set',iface,'up')
        for args in [('ip','link','set','lo','up'),('ip','addr','add',ip+'/30','dev',peer),('ip','link','set',peer,'up')]:
            run('nsenter','-t',str(p.pid),'-n',*args)
        (self.root/f'ready-{node}').touch()
        self.nodes[node]={'process':p,'ip':ip,'port':port,'interface':iface,'log':log}
        for _ in range(200):
            if (self.root/f'core/started-{node}.json').exists():break
            if p.poll() is not None:raise RuntimeError('fixture failed to start')
            time.sleep(.02)
        else:raise RuntimeError('fixture startup timed out')

    def stop(self,node):
        info=self.nodes[node];p=info['process']
        p.terminate()
        try:p.wait(3)
        except subprocess.TimeoutExpired:p.kill();p.wait()
        info['log'].close()
        # No other process holds the core netns; its veth normally disappears.
        subprocess.run(['ip','link','delete',info['interface']],capture_output=True)

    def channel(self,node='a',cert='old',name=None,plain=False,uds=False):
        if uds:
            channel=grpc.insecure_channel('unix:'+str(self.root/'core/pair.sock'),options=[('grpc.enable_retries',0)])
        else:
            n=self.nodes[node];target=f"{n['ip']}:{n['port']}"
            if plain:channel=grpc.insecure_channel(target,options=[('grpc.enable_retries',0)])
            else:
                creds=grpc.ssl_channel_credentials(root_certificates=(self.root/'ca.pem').read_bytes(),
                    private_key=(self.root/f'clients/{cert}.key').read_bytes() if cert else None,
                    certificate_chain=(self.root/f'clients/{cert}.pem').read_bytes() if cert else None)
                # Only a test dial-address override. TLS still verifies this exact DNS SAN.
                channel=grpc.secure_channel(target,creds,options=[('grpc.ssl_target_name_override',name or f'core-{node}.dp24.test'),('grpc.enable_retries',0),('grpc.max_receive_message_length',MAX_MESSAGE),('grpc.max_send_message_length',MAX_MESSAGE+1024)])
        self.channels.append(channel)
        return channel

    def call(self,ch,method=CONTROL+'Capabilities',data=b'',meta=None,timeout=1.2):
        return ch.unary_unary(method)(data,metadata=self.meta if meta is None else meta,timeout=timeout,wait_for_ready=False)

    def check(self,name,fn):
        t=time.monotonic()
        try:
            detail=fn()
            self.tests.append({'name':name,'status':'pass','seconds':round(time.monotonic()-t,4),'detail':detail})
        except subprocess.CalledProcessError as e:
            state='blocked' if 'Specified qdisc kind is unknown' in e.stderr else 'fail'
            self.tests.append({'name':name,'status':state,'seconds':round(time.monotonic()-t,4),'error':e.stderr.strip(),'command':e.cmd})
        except Exception as e:
            self.tests.append({'name':name,'status':'fail','seconds':round(time.monotonic()-t,4),'error':type(e).__name__+': '+str(e)})
        dump(self.out/'results.json',{'scope':'local isolated Python gRPC fixture, NOT Rust/PostgreSQL','tests':self.tests})

    def denied(self,ch,method=CONTROL+'Capabilities',data=b'',meta=None,allowed=None):
        try:self.call(ch,method,data,meta)
        except grpc.RpcError as e:
            if allowed and e.code().name not in allowed:raise AssertionError(e.code().name)
            return e.code().name
        raise AssertionError('unexpected success')

    def rotate(self,**kw):
        self.version+=1
        policy(self.root,self.pins,self.version,**kw)

    def ensure_ready(self,ch):
        grpc.channel_ready_future(ch).result(timeout=3)
        self.call(ch)

    def measure(self,ch,count=100):
        self.ensure_ready(ch)
        vals=[]
        for _ in range(count):
            t=time.perf_counter_ns();self.call(ch);vals.append((time.perf_counter_ns()-t)/1e6)
        vals.sort()
        return {'calls':count,'p50_ms':round(statistics.median(vals),4),'p95_ms':round(vals[int(.95*(count-1))],4),'max_ms':round(max(vals),4)}

    def exercise(self):
        a=self.channel('a');b=self.channel('b');self.ensure_ready(a);self.ensure_ready(b)
        short_channel=self.channel(cert='short');self.ensure_ready(short_channel)
        self.check('three_isolated_network_namespaces',lambda:self.topology())
        self.check('mTLS_and_connection_reuse',lambda:{'a':self.call(a).hex(),'b':self.call(b).hex(),'repeat':self.call(a).hex()})
        self.check('plaintext_rejected',lambda:self.denied(self.channel(plain=True)))
        self.check('missing_client_certificate_rejected',lambda:self.denied(self.channel(cert=None)))
        self.check('wrong_server_name_rejected',lambda:self.denied(self.channel(name='not-core.dp24.test')))
        self.check('expired_client_certificate_rejected',lambda:self.denied(self.channel(cert='expired')))
        self.check('valid_CA_wrong_service_identity_rejected',lambda:self.denied(self.channel(cert='wrong'),allowed=['PERMISSION_DENIED']))
        self.check('service_identity_is_not_user_authority',lambda:self.denied(a,CONTROL+'Authorize',allowed=['UNAUTHENTICATED']))
        self.check('duplicate_authorization_rejected',lambda:self.denied(a,meta=self.meta+[('authorization','Bearer '+SERVICE_TOKEN)],allowed=['UNAUTHENTICATED']))
        self.check('wrong_protocol_major_rejected',lambda:self.denied(a,meta=[('authorization','Bearer '+SERVICE_TOKEN),('x-lmm-protocol','2')],allowed=['FAILED_PRECONDITION']))
        self.check('unknown_method_is_not_enabled',lambda:self.denied(a,'/lmm.core.v1.CorePayments/CreditTopup',allowed=['UNIMPLEMENTED']))
        def credentials():
            result={}
            for key,secret in [('session',SESSION),('api_key',API_KEY)]:
                meta=self.meta+[('x-lmm-user-credential',secret)]
                ra=self.call(a,CONTROL+'Authorize',meta=meta)
                rb=self.call(b,CONTROL+'Authorize',meta=meta)
                assert ra==rb
                result[key]={'same_response':True,'wire_hex':ra.hex()}
            return result
        self.check('same_session_and_key_across_nodes_fixture',credentials)
        def revocation():
            with sqlite3.connect(self.root/'core/fixture.sqlite') as db:
                db.execute('UPDATE credentials SET revoked=1 WHERE digest=?',(digest(API_KEY.encode()),))
            meta=self.meta+[('x-lmm-user-credential',API_KEY)]
            result={n:self.denied(ch,CONTROL+'Authorize',meta=meta,allowed=['UNAUTHENTICATED']) for n,ch in [('a',a),('b',b)]}
            with sqlite3.connect(self.root/'core/fixture.sqlite') as db:db.execute('UPDATE credentials SET revoked=0')
            return result
        self.check('user_key_revocation_across_reused_channels_fixture',revocation)
        def authority_outage():
            self.call(a,CONTROL+'Authorize',meta=self.user_meta)
            flag=self.root/'core/offline-a';flag.touch()
            try:
                denied=self.denied(a,CONTROL+'Authorize',meta=self.user_meta,allowed=['UNAVAILABLE'])
                success=self.call(b,CONTROL+'Authorize',meta=self.user_meta)
                return {'a':denied,'b_success':bool(success)}
            finally:flag.unlink()
        self.check('disconnected_authority_does_not_use_cached_grants_fixture',authority_outage)
        def rotation():
            new=self.channel(cert='new');self.ensure_ready(new)
            self.rotate(old_until=time.time()+.35)
            self.call(a);self.call(new)
            time.sleep(.4)
            expired=self.denied(a,allowed=['PERMISSION_DENIED'])
            self.call(new)
            # Remove an already active certificate without waiting for its lease.
            self.rotate(peers=('old',));revoked=self.denied(new,allowed=['PERMISSION_DENIED'])
            self.rotate()
            return {'old_after_overlap':expired,'new_after_explicit_revoke':revoked,'existing_channels_checked':True}
        self.check('bounded_certificate_overlap_and_explicit_revoke',rotation)
        def token_rotation():
            now=time.time();self.version+=1
            p=json.loads((self.root/'core/policy.json').read_text());p['version']=self.version
            new_token='dp24_rotated_'+'N'*40
            p['tokens']=[{'digest':digest(SERVICE_TOKEN.encode()),'until':now+.3},{'digest':digest(new_token.encode()),'until':now+30}]
            dump(self.root/'core/policy.json',p)
            new_meta=[('authorization','Bearer '+new_token),('x-lmm-protocol','1')]
            self.call(a);self.call(a,meta=new_meta);time.sleep(.35)
            old=self.denied(a,allowed=['UNAUTHENTICATED']);self.call(a,meta=new_meta)
            self.rotate()
            return {'old_token_after_overlap':old,'new_token':'accepted'}
        self.check('bounded_service_token_rotation',token_rotation)
        def certificate_expiry():
            cert=x509.load_pem_x509_certificate((self.root/'clients/short.pem').read_bytes())
            wait=max(0,cert.not_valid_after_utc.timestamp()-time.time()+.08)
            assert wait<=8
            time.sleep(wait)
            old=self.denied(short_channel,allowed=['PERMISSION_DENIED'])
            fresh=self.denied(self.channel(cert='short'),allowed=['UNAVAILABLE'])
            return {'established_connection':old,'new_handshake':fresh,'wait_seconds':round(wait,3)}
        self.check('certificate_expiry_on_existing_TLS_connection',certificate_expiry)
        def method_scope():
            p=json.loads((self.root/'core/policy.json').read_text());self.version+=1;p['version']=self.version
            p['methods'].remove(CONTROL+'Authorize');dump(self.root/'core/policy.json',p)
            try:
                denied=self.denied(a,CONTROL+'Authorize',meta=self.user_meta,allowed=['PERMISSION_DENIED'])
                self.call(a)
                return {'Authorize':denied,'Capabilities':'OK'}
            finally:self.rotate()
        self.check('exact_service_method_allowlist',method_scope)

        def policy_expiry():
            self.rotate(ttl=.2);self.call(a);time.sleep(.25)
            result=self.denied(a,allowed=['UNAUTHENTICATED']);self.rotate();self.call(a)
            return result
        self.check('expired_policy_lease_fails_closed',policy_expiry)
        def bad_policy():
            saved=json.loads((self.root/'core/policy.json').read_text())
            (self.root/'core/policy.json').write_text('{bad')
            invalid=self.denied(a,allowed=['UNAUTHENTICATED'])
            saved['version']=1;dump(self.root/'core/policy.json',saved)
            rollback=self.denied(a,allowed=['UNAUTHENTICATED'])
            self.rotate();self.call(a)
            return {'malformed':invalid,'rollback':rollback}
        self.check('malformed_and_rollback_policy_denied',bad_policy)
        def money():
            op='dp24-fixed-operation-1'
            request=json.dumps({'operation_id':op,'amount':17,'drop_reply':True}).encode()
            lost=self.denied(a,PROBE+'Charge',request,self.user_meta,['UNAVAILABLE'])
            receipt=json.loads(self.call(b,PROBE+'Receipt',json.dumps({'operation_id':op}).encode(),self.user_meta))
            assert receipt['status']=='committed'
            replay=json.loads(self.call(b,PROBE+'Charge',json.dumps({'operation_id':op,'amount':17}).encode(),self.user_meta))
            assert replay['replayed']
            changed=self.denied(b,PROBE+'Charge',json.dumps({'operation_id':op,'amount':18}).encode(),self.user_meta,['ALREADY_EXISTS'])
            with sqlite3.connect(self.root/'core/fixture.sqlite') as db:
                count=db.execute('SELECT count(*) FROM operations').fetchone()[0]
                balance=db.execute('SELECT amount FROM balance').fetchone()[0]
            assert count==1 and balance==9983
            return {'lost_reply':lost,'operation_id':op,'receipt_on_b':receipt,'payload_change':changed,'committed_rows':count,'remaining_fixture_units':balance,'REAL_LEDGER_TEST':False}
        self.check('lost_reply_same_identifier_receipt_and_dedup_FIXTURE_ONLY',money)
        self.check('message_limit',lambda:self.denied(a,CONTROL+'Authorize',b'x'*(MAX_MESSAGE+1),self.user_meta,['RESOURCE_EXHAUSTED']))
        def concurrency():
            channels=[self.channel() for _ in range(4)]
            for ch in channels:self.ensure_ready(ch)
            gate=threading.Barrier(16)
            def one(i):
                gate.wait(timeout=3)
                try:self.call(channels[i%4],PROBE+'Sleep',b'{"seconds":0.3}',self.user_meta);return 'OK'
                except grpc.RpcError as e:return e.code().name
            with futures.ThreadPoolExecutor(max_workers=16) as pool:codes=list(pool.map(one,range(16)))
            assert 'RESOURCE_EXHAUSTED' in codes
            metrics=json.loads((self.root/'core/metrics-a.json').read_text())
            assert metrics['peak']<=8 and metrics['active']==0
            for ch in channels:ch.close()
            return {'outcomes':{c:codes.count(c) for c in set(codes)},'handler_peak':metrics['peak'],'active_after':metrics['active']}
        self.check('eight_global_calls_and_resource_recovery',concurrency)
        def deadline():
            t=time.monotonic()
            try:self.call(a,PROBE+'Sleep',b'{"seconds":2}',self.user_meta,timeout=.12)
            except grpc.RpcError as e:assert e.code()==grpc.StatusCode.DEADLINE_EXCEEDED
            else:raise AssertionError('missing deadline')
            duration=time.monotonic()-t;time.sleep(.08)
            assert json.loads((self.root/'core/metrics-a.json').read_text())['active']==0
            return {'elapsed_ms':round(duration*1000,2),'handler_released':True}
        self.check('deadline_and_handler_release',deadline)
        def network():
            run('tc','qdisc','add','dev','dxa','root','netem','delay','40ms','loss','10%')
            codes=[];t=time.monotonic()
            try:
                for _ in range(8):
                    try:self.call(a,timeout=.45);codes.append('OK')
                    except grpc.RpcError as e:codes.append(e.code().name)
                stats=run('tc','-s','qdisc','show','dev','dxa').stdout
            finally:run('tc','qdisc','del','dev','dxa','root')
            dump(self.out/'netem.json',{'stats':stats,'codes':codes})
            self.call(b)
            return {'injection':'A egress: 40ms delay, 10% random packet loss','calls':len(codes),'outcomes':{c:codes.count(c) for c in set(codes)},'seconds':round(time.monotonic()-t,3),'b_healthy':True}
        self.check('real_netem_delay_and_packet_loss',network)
        def partition_stream():
            stream=a.unary_stream(PROBE+'Stream')(b'',metadata=self.user_meta,timeout=.5)
            first=json.loads(next(stream))
            assert first['node']=='a'
            run('ip','link','set','dxa','down')
            try:
                blocked=self.denied(a,allowed=['DEADLINE_EXCEEDED','UNAVAILABLE'])
                self.call(b)
                records=[first];stream_error=None
                try:
                    for raw in stream:records.append(json.loads(raw))
                except grpc.RpcError as e:stream_error=e.code().name
                assert stream_error and all(x['node']=='a' for x in records)
                assert len({x['sequence'] for x in records})==len(records)
                stats=run('ip','-details','link','show','dxa').stdout
            finally:run('ip','link','set','dxa','up')
            t=time.monotonic();self.call(a)
            return {'a':blocked,'b_new_request':'OK','stream_error':stream_error,'stream_nodes':['a'],'chunks_seen':len(records),'existing_stream_migrated':False,'after_fault_removed_ms':round((time.monotonic()-t)*1000,3),'interface_state':stats}
        self.check('partial_link_down_and_no_stream_migration',partition_stream)
        def restart():
            self.stop('a')
            t=time.monotonic();attempts=[]
            for ch,node in ((a,'a'),(b,'b')):
                attempts.append(node)
                try:
                    result=self.call(ch,CONTROL+'Authorize',meta=self.user_meta,timeout=.35)
                    break
                except grpc.RpcError as e:
                    if e.code() not in (grpc.StatusCode.UNAVAILABLE,grpc.StatusCode.DEADLINE_EXCEEDED):raise
            else:raise AssertionError('both nodes failed')
            assert attempts==['a','b']
            switch_ms=(time.monotonic()-t)*1000
            self.start('a');new_a=self.channel('a');self.ensure_ready(new_a)
            assert self.call(new_a,CONTROL+'Authorize',meta=self.user_meta)==result
            return {'bounded_query_attempts':attempts,'switch_ms':round(switch_ms,3),'session_survives_fixture_restart':True}
        self.check('node_down_bounded_query_failover_and_restart',restart)
        def address_change():
            self.stop('b');self.start('b',51053)
            new_b=self.channel('b');self.ensure_ready(new_b)
            old=self.denied(b,allowed=['UNAVAILABLE','DEADLINE_EXCEEDED'])
            self.call(new_b,CONTROL+'Authorize',meta=self.user_meta)
            return {'stable_tls_name':'core-b.dp24.test','old_port':51051,'new_port':51053,'old_connection':old,'new_endpoint':'accepted'}
        self.check('address_change_with_stable_verified_identity',address_change)
        self.rotate()
        a=self.channel('a');self.ensure_ready(a)
        self.check('latency_UDS_grpc_fixture',lambda:self.measure(self.channel(uds=True),200))
        self.check('latency_mTLS_grpc_fixture',lambda:self.measure(a,200))
        def handshake():
            context=ssl.create_default_context(cafile=str(self.root/'ca.pem'))
            context.minimum_version=ssl.TLSVersion.TLSv1_3
            context.load_cert_chain(self.root/'clients/old.pem',self.root/'clients/old.key')
            context.set_alpn_protocols(['h2'])
            values=[]
            for _ in range(30):
                t=time.perf_counter_ns()
                with socket.create_connection((self.nodes['a']['ip'],self.nodes['a']['port']),timeout=1) as raw:
                    with context.wrap_socket(raw,server_hostname='core-a.dp24.test') as tls:
                        assert tls.selected_alpn_protocol()=='h2'
                        values.append((time.perf_counter_ns()-t)/1e6)
            values.sort()
            return {'count':30,'p50_ms':round(statistics.median(values),4),'p95_ms':round(values[27],4),'includes_tcp_connect':True,'runtime':'CPython/OpenSSL, not Rust'}
        self.check('TLS13_handshake_cost_fixture',handshake)
        def go_probe():
            binary=os.environ.get('DP24_POLICY_TEST')
            assert binary, 'compiled Go fixture probe missing'
            env={'PATH':os.environ['PATH'],'TERM':'dumb','DP24_FIXTURE_ROOT':str(self.root),'DP24_FIXTURE_ADDRESS':self.nodes['a']['ip']+':51051'}
            command=['unshare','--mount','bash','-c','set -euo pipefail; mount --make-rprivate /; mount -t tmpfs -o size=1m,mode=0700 tmpfs "$1/core"; exec "$2" -test.run "^TestGoGRPCFixture$" -test.v','_',str(self.root),binary]
            before = json.loads((self.root/'core/metrics-a.json').read_text())['calls']
            result=subprocess.run(command,check=True,capture_output=True,text=True,timeout=15,env=env)
            time.sleep(.05)
            after = json.loads((self.root/'core/metrics-a.json').read_text())['calls']
            assert after - before == 2, 'revoked third RPC reached the service handler'
            (self.out/'go-fixture.log').write_text(result.stdout+result.stderr)
            return {'runtime':'Go standard-library HTTP/2 client → Python gRPC fixture','core_database_and_server_keys':'masked in a separate mount namespace','accepted_handler_calls':after-before,'revoked_third_rpc_reached_handler':False,'result':result.stdout}
        self.check('Go_mTLS_grpc_wire_and_core_credential_isolation',go_probe)
        self.check('resource_snapshot',lambda:self.resources())

    def topology(self):
        data={'client_namespace':os.readlink('/proc/self/ns/net'),'client_routes':run('ip','route').stdout,'nodes':{}}
        namespaces={data['client_namespace']}
        for node,n in self.nodes.items():
            ns=os.readlink(f"/proc/{n['process'].pid}/ns/net");namespaces.add(ns)
            routes=run('nsenter','-t',str(n['process'].pid),'-n','ip','route').stdout
            assert 'default' not in routes
            data['nodes'][node]={'netns':ns,'routes':routes,'ip':n['ip']}
        assert len(namespaces)==3 and 'default' not in data['client_routes']
        dump(self.out/'topology.json',data)
        return data

    def resources(self):
        result={'client':{},'nodes':{},'cpu_1c_constraint':False,'memory_1GiB_constraint':False,'server_connection_limit_verified':False}
        for name,p in [('client',psutil.Process())]+[(n,psutil.Process(x['process'].pid)) for n,x in self.nodes.items()]:
            mem=p.memory_info();cpu=p.cpu_times()
            val={'rss_MiB':round(mem.rss/1048576,3),'vms_MiB':round(mem.vms/1048576,3),'threads':p.num_threads(),'fds':p.num_fds(),'cpu_seconds':round(cpu.user+cpu.system,3)}
            if name=='client':result[name]=val
            else:result['nodes'][name]=val
        return result

    def cleanup(self):
        for ch in self.channels:ch.close()
        for node,n in self.nodes.items():
            if n['process'].poll() is None:self.stop(node)


def orchestrate(out):
    # A fresh namespace with only loopback is required before any network write.
    # run.sh supplies the original namespace; direct host invocation is refused.
    outer = os.environ.get('DP24_OUTER_NETNS')
    current = os.readlink('/proc/self/ns/net')
    interfaces = json.loads(run('ip', '-j', 'link').stdout)
    if (not outer or outer == current or not interfaces
            or any(i['ifname'] != 'lo' for i in interfaces)
            or 'default' in run('ip', 'route', 'show', 'table', 'all').stdout):
        raise SystemExit('refusing to run outside a fresh isolated network; use run.sh')
    out.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='dp24-') as temporary:
        root=Path(temporary)
        dump(root/'isolation.json', {'outer': outer, 'controller': current})
        pins=pki(root);policy(root,pins,1)
        lab=Lab(root,out,pins)
        try:
            lab.start('a');lab.start('b')
            lab.exercise()
            result={'scope':'local isolated protocol experiment; Python gRPC server, SQLite fixture; NOT Rust/Go production integration',
                    'started_unix':time.time()-(time.monotonic()-lab.started),'duration_seconds':round(time.monotonic()-lab.started,3),
                    'grpc_version':grpc.__version__,'python':sys.version.split()[0],
                    'passed':sum(t['status']=='pass' for t in lab.tests),'failed':sum(t['status']=='fail' for t in lab.tests),'blocked':sum(t['status']=='blocked' for t in lab.tests),'tests':lab.tests,
                    'not_verified':['Rust compilation/runtime','production Go gRPC integration','real PostgreSQL credentials and ledger','real multi-server deployment','1c1g capacity','server hard connection cap','CA trust-root live rotation','automatic service discovery/controller','bounded old channel drain across manifest generations']}
            dump(out/'results.json',result)
            print(json.dumps({'passed':result['passed'],'failed':result['failed'],'blocked':result['blocked'],'seconds':result['duration_seconds']}))
            if result['failed']:raise SystemExit(1)
            if result['blocked']:raise SystemExit(2)
        finally:lab.cleanup()


if __name__=='__main__':
    if sys.argv[1]=='serve':serve(Path(sys.argv[2]),sys.argv[3],sys.argv[4],int(sys.argv[5]))
    elif sys.argv[1]=='orchestrate':orchestrate(Path(sys.argv[2]))
    else:raise SystemExit('unknown mode')
