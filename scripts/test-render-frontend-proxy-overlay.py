import hashlib,http.server,json,pathlib,socket,subprocess,tempfile,threading,time,urllib.error,urllib.request
import importlib.util
ROOT=pathlib.Path(__file__).resolve().parent.parent
spec=importlib.util.spec_from_file_location('frontend_overlay',ROOT/'scripts/render-frontend-proxy-overlay.py')
renderer=importlib.util.module_from_spec(spec);spec.loader.exec_module(renderer)
route_tree="export interface FileRoutesByFullPath {\n  '/': typeof Root\n  '/dashboard/$section': typeof Dashboard\n  '/privacy-policy': typeof Privacy\n  '/models/$section': typeof Models\n  '/store/claim/$token': typeof Claim\n}\n"
captured=b'location @lmm_api_backend {\n proxy_pass http://127.0.0.1:3000;\n proxy_http_version 1.1;\n proxy_set_header Host $host;\n proxy_set_header Upgrade $websocket_upgrade;\n proxy_set_header Connection $connection_upgrade;\n proxy_next_upstream off;\n}\nlocation / {\n    error_page 418 = @lmm_api_backend;\n    return 418;\n}\n'
original=renderer.render(captured,route_tree,['index.html','robots.txt'])
for bad_path in ('/api/key','/dashboard/billing/usage','/x/mj/task','/scripts/name'):
 bad_tree=route_tree.replace("  '/': typeof Root","  '/': typeof Root\n  '"+bad_path+"': typeof Unexpected")
 try:renderer.render(captured,bad_tree,['index.html'])
 except ValueError:pass
 else:raise AssertionError('backend overlap accepted')

class Backend(http.server.BaseHTTPRequestHandler):
 def do(self):
  data=self.rfile.read(int(self.headers.get('Content-Length','0')))
  if self.path.startswith('/api/status'):code=200;body=b'{"data":{"version":"0.2.98"}}'
  elif self.path.startswith('/billing-redirect'):code=308;body=b''
  elif self.path.startswith('/api/missing'):code=404;body=b'{"error":"missing"}'
  else:code=200;body=json.dumps({'method':self.command,'path':self.path,'body':data.decode()}).encode()
  self.send_response(code)
  if code==308:self.send_header('Location','/billing-final?keep=1')
  self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers()
  if self.command!='HEAD':self.wfile.write(body)
 do_GET=do_POST=do_HEAD=do_DELETE=do_OPTIONS=do_PATCH=do
 def log_message(self,*a):pass
backend=http.server.ThreadingHTTPServer(('127.0.0.1',0),Backend);threading.Thread(target=backend.serve_forever,daemon=True).start()
try:
 with tempfile.TemporaryDirectory(prefix='static137-nginx-review-') as directory:
  p=pathlib.Path(directory);p.chmod(0o755);front=p/'frontend';assets=p/'assets';front.mkdir();assets.mkdir();(assets/'js').mkdir();(front/'index.html').write_text('INDEX137-FIXTURE');(assets/'js'/'index.0c0e2906e0.js').write_text('console.log("137")');(front/'robots.txt').write_text('robots137')
  s=socket.socket();s.bind(('127.0.0.1',0));port=s.getsockname()[1];s.close()
  conf=original.decode().replace('include /etc/nginx/snippets/lmm-api-maintenance-display-server.conf;','').replace('include /etc/nginx/lmm-api-mime.types;','include '+str(ROOT/'packaging/common/lmm-api/edge-policy/nginx/mime.types')+';').replace('/srv/lmm-api-frontend/current',str(front)).replace('/srv/lmm-api-frontend/assets',str(assets)).replace('127.0.0.1:3000','127.0.0.1:'+str(backend.server_port)).replace('/var/log/nginx/access.log',str(p/'access.log'))
  dirs=' '.join(f'{kind}_temp_path {p}/{kind};' for kind in ('client_body','proxy','fastcgi','uwsgi','scgi'))
  top=f'error_log {p}/error.log; pid {p}/nginx.pid; events {{ worker_connections 64; }} http {{ access_log off; '+dirs+f' map $http_upgrade $websocket_upgrade {{ default $http_upgrade; }} map $http_upgrade $connection_upgrade {{ default upgrade; "" close; }} map $request_uri $lmm_access_loggable {{ default 1; }} server {{ listen 127.0.0.1:{port}; '+conf+' } }'
  config=p/'nginx.conf';config.write_text(top);t=subprocess.run(['/usr/bin/nginx','-p',str(p),'-c',str(config),'-t'],capture_output=True);assert t.returncode==0,t.stderr.decode()
  proc=subprocess.Popen(['/usr/bin/nginx','-p',str(p),'-c',str(config),'-g','daemon off;'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  class NoRedirect(urllib.request.HTTPRedirectHandler):
   def redirect_request(self,*args):return None
  opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect())
  def request(path,method='GET',data=None):
   req=urllib.request.Request(f'http://127.0.0.1:{port}'+path,method=method,data=data)
   for _ in range(30):
    try:
     try:r=opener.open(req,timeout=3)
     except urllib.error.HTTPError as e:r=e
     return r.status,r.headers,r.read()
    except urllib.error.URLError:time.sleep(.03)
   raise AssertionError('nginx fixture unavailable')
  try:
   checks=0
   for path in ['/', '/index.html', '/dashboard/overview', '/dashboard/overview/', '/privacy-policy', '/models/abc', '/store/claim/'+'a'*43]:
    status,headers,body=request(path);assert status==200 and body==b'INDEX137-FIXTURE',(path,status,body);checks+=1
   status,headers,body=request('/static/js/index.0c0e2906e0.js');assert status==200 and body==b'console.log("137")' and headers['Content-Type'].split(';')[0] in ('text/javascript','application/javascript'),(status,headers,body);checks+=1
   status,headers,body=request('/static/js/missing.js');assert status==404 and b'INDEX137' not in body;checks+=1
   status,headers,body=request('/dashboard/overview',method='HEAD');assert status==200 and not body;checks+=1
   for path,method in [('/api/test?a=%2F&b=1','POST'),('/dashboard/overview?keep=1','POST'),('/static/js/index.0c0e2906e0.js?keep=1','DELETE'),('/index.html?keep=1','PATCH'),('/robots.txt?keep=1','OPTIONS')]:
    status,headers,body=request(path,method,data=b'BODY-KEEP');assert status==200 and json.loads(body)=={'method':method,'path':path,'body':'BODY-KEEP'},(path,method,status,body);checks+=1
   status,headers,body=request('/models/mj?keep=1');assert status==200 and json.loads(body)['path']=='/models/mj?keep=1';checks+=1
   status,headers,body=request('/api/status');assert status==200 and json.loads(body)['data']['version']=='0.2.98';checks+=1
   status,headers,body=request('/api/missing');assert status==404 and body==b'{"error":"missing"}';checks+=1
   status,headers,body=request('/unknown-route-static137-probe');assert status==200 and json.loads(body)['path']=='/unknown-route-static137-probe';checks+=1
   status,headers,body=request('/billing-redirect?x=1');assert status==308 and headers['Location']=='/billing-final?keep=1';checks+=1
   print(json.dumps({'local_nginx_syntax':'PASS','local_http_checks':checks,'rendered_sha256':hashlib.sha256(original).hexdigest(),'method_query_body_preserved':True,'api_redirect_404_preserved':True,'production_dispatch':False}))
  finally:proc.terminate();proc.wait(timeout=5)
finally:backend.shutdown();backend.server_close()
