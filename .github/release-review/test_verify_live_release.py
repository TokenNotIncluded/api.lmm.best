import io,json,os,tarfile,unittest
from verify_live_release import load_public_probe,verify_identity
P=load_public_probe(os.environ.get('LMM_PUBLIC_PROBE', 'scripts/verify-public-production.py'))
SHA='a'*40
HTML=b'<html><script src="/static/app.js"></script><link rel="stylesheet" href="/static/app.css"></html>'
class Acceptance(unittest.TestCase):
 def fixture(self):
  f=io.BytesIO()
  with tarfile.open(fileobj=f,mode='w:gz') as t:
   for name,data in [('REVISION',(SHA+'\n').encode()),('dist/index.html',HTML),('dist/static/app.js',b'void 0;'),('dist/static/app.css',b'body{}')]:
    m=tarfile.TarInfo(name);m.size=len(data);t.addfile(m,io.BytesIO(data))
  f.seek(0);t=tarfile.open(fileobj=f,mode='r:gz')
  response={P.ORIGIN+p:P.Response(200,'text/html',HTML) for p in ('/','/login','/console')}
  response[P.ORIGIN+'/api/status']=P.Response(200,'application/json',json.dumps({'success':True,'data':{'version':'0.2.51'}}).encode())
  response[P.ORIGIN+'/static/app.js']=P.Response(200,'application/javascript',b'void 0;')
  response[P.ORIGIN+'/static/app.css']=P.Response(200,'text/css',b'body{}')
  return t,response
 def check(self,t,res,sha=SHA):
  return verify_identity(P,t,res.__getitem__,sha,'0.2.51','0.1.74')
 def test_matching_release_passes(self):
  t,r=self.fixture()
  with t:self.assertEqual(len(self.check(t,r)['verified_entries']),2)
 def test_wrong_revision_fails(self):
  t,r=self.fixture()
  with t,self.assertRaisesRegex(ValueError,'revision'):self.check(t,r,'b'*40)
 def test_stale_backend_fails(self):
  t,r=self.fixture();r[P.ORIGIN+'/api/status']=P.Response(200,'application/json',b'{"success":true,"data":{"version":"0.2.31"}}')
  with t,self.assertRaisesRegex(ValueError,'version mismatch'):self.check(t,r)
 def test_stale_html_fails(self):
  t,r=self.fixture();r[P.ORIGIN+'/']=P.Response(200,'text/html',HTML+b'old')
  with t,self.assertRaisesRegex(ValueError,'public HTML'):self.check(t,r)
 def test_modified_javascript_fails(self):
  t,r=self.fixture();r[P.ORIGIN+'/static/app.js']=P.Response(200,'application/javascript',b'void 1;')
  with t,self.assertRaisesRegex(ValueError,'public asset'):self.check(t,r)
 def test_spa_fallback_not_javascript(self):
  t,r=self.fixture();r[P.ORIGIN+'/static/app.js']=P.Response(200,'text/html',HTML)
  with t,self.assertRaisesRegex(ValueError,'SPA fallback'):self.check(t,r)
 def test_stale_login_fails(self):
  t,r=self.fixture();r[P.ORIGIN+'/login']=P.Response(200,'text/html',HTML+b'old')
  with t,self.assertRaisesRegex(ValueError,'public HTML'):self.check(t,r)
if __name__=='__main__':unittest.main()
