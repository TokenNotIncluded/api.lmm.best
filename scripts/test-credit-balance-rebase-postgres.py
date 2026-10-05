#!/usr/bin/env python3
"""Real transaction verification in a new local Unix-socket PostgreSQL fixture only."""
import importlib.util,json,pathlib,subprocess,tempfile,os,shutil
spec=importlib.util.spec_from_file_location('r',pathlib.Path(__file__).with_name('preview-credit-balance-rebase.py')); r=importlib.util.module_from_spec(spec); spec.loader.exec_module(r)
root=pathlib.Path(tempfile.mkdtemp(prefix='credit-rebase-pg-',dir='/home/lightjunction/.cache')); data=root/'data'; sock=root/'socket'; sock.mkdir()
def run(cmd,**kw): return subprocess.run(cmd,text=True,capture_output=True,env={k:v for k,v in os.environ.items() if not k.startswith('PG')},**kw)
def ok(cmd,**kw):
 p=run(cmd,**kw)
 if p.returncode: raise RuntimeError(p.stderr)
 return p.stdout.strip()
ok(['initdb','-D',str(data),'-A','trust','--no-instructions'])
ok(['pg_ctl','-D',str(data),'-l',str(root/'pg.log'),'-o',f'-c listen_addresses= -c unix_socket_directories={sock}','-w','start'])
base=['psql','-X','-q','-A','-t','-v','ON_ERROR_STOP=1','-h',str(sock),'-d','postgres']
try:
 ok(base,input="CREATE TABLE options (key text PRIMARY KEY,value text); INSERT INTO options VALUES ('CreditsPerUSD','3359744'),('PublicCreditsPerUSD','100000'),('LegacyPricingQuotaPerUnit','500000'),('QuotaPerUnit','500000'); CREATE TABLE users (id bigint PRIMARY KEY, quota bigint, aff_quota bigint, used_quota bigint); CREATE TABLE tokens (id bigint PRIMARY KEY, user_id bigint, remain_quota bigint, unlimited_quota bool); INSERT INTO users VALUES (1,500000000,680,123),(2,-86911,0,45);")
 s={'version':1,'applied_migration_ids':[],'users':[{'id':1,'quota':500000000},{'id':2,'quota':-86911}],'tokens':[]}
 s['options']={'CreditsPerUSD':'3359744','PublicCreditsPerUSD':'100000','LegacyPricingQuotaPerUnit':'500000','QuotaPerUnit':'500000'}
 s['price_review']={'status':'verified','evidence':'synthetic fixture','option_corrections':[]}
 kw=dict(divisor_text='6.8',migration_id='fixture-v1',user_ids=[1,2],rounding='half-away-from-zero',restore_fixed_anchors=True)
 p=r.make_plan(s,**kw); sql=r.postgres_sql(p)
 ok(base,input=sql); first=ok(base,input='SELECT id,quota,used_quota FROM users ORDER BY id;'); assert first=='1|73529412|123\n2|-12781|45',first
 assert ok(base,input="SELECT value FROM options WHERE key='CreditsPerUSD';")=='500000'
 ok(base,input=sql); assert ok(base,input='SELECT id,quota,used_quota FROM users ORDER BY id;')==first
 other=r.postgres_sql(r.make_plan(s,**(kw|{'migration_id':'fixture-v2'}))); assert run(base,input=other).returncode!=0
 assert ok(base,input='SELECT count(*) FROM wallet_credit_rebases;')=='1'
 ok(base,input="TRUNCATE wallet_credit_rebases; UPDATE options SET value=CASE WHEN key='CreditsPerUSD' THEN '3359744' WHEN key='PublicCreditsPerUSD' THEN '100000' ELSE '500000' END; UPDATE users SET quota=500000000 WHERE id=1; UPDATE users SET quota=-86910 WHERE id=2;")
 assert run(base,input=sql).returncode!=0
 assert ok(base,input='SELECT quota FROM users WHERE id=1;')=='500000000'
 assert ok(base,input='SELECT count(*) FROM wallet_credit_rebases;')=='0'
 ok(base,input="UPDATE users SET quota=-86911 WHERE id=2; UPDATE options SET value='unexpected-anchor' WHERE key='CreditsPerUSD';")
 assert run(base,input=sql).returncode!=0
 assert ok(base,input='SELECT quota FROM users WHERE id=1;')=='500000000'
 assert ok(base,input='SELECT count(*) FROM wallet_credit_rebases;')=='0'
 print('Isolated PostgreSQL passed: real apply, negative debt, same-plan rerun, different-id double-debit rejection, user/anchor conflicts roll back all wallet changes.')
finally:
 ok(['pg_ctl','-D',str(data),'-m','immediate','-w','stop'])
 shutil.rmtree(root)
