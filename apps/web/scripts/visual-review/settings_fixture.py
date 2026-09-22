# Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
# Synthetic fixtures. Never connects to production.
import json,time
from pathlib import Path
from urllib.parse import urlparse
now=int(time.time())
USER={'id':1099,'username':'design_admin','display_name':'设计管理员','email':'preview@example.test','role':100,'status':1,'group':'default','quota':25000000,'used_quota':4500000,'request_count':1098,'developer_access_granted':True,'language':'zhCN','permissions':{'docs_access':True},'onboarding':{'activation_complete':True,'credential_complete':True,'first_request_complete':True,'stage':'complete'},'trust_level_info':{'level':2,'automatic_level':2,'override_level':None,'paid_amount':500,'discount_ratio':1,'discount_percent':0,'next_level':None,'inactivity_decay_steps':0,'decay_period_days':90,'overridden':False}}
BUNDLE={'access_token':'local-visual-fixture-not-a-real-token','token_type':'Bearer','access_expires_at':now+86400,'user':USER,'session':{'sid':'local-visual-review','current':True,'login_method':'password','ip':'127.0.0.1','user_agent':'Visual review','created_at':now-3600,'last_active_at':now,'expires_at':now+86400}}
def make_options():
 opts=json.loads(Path(__file__).with_name('assistant-defaults.json').read_text())
 opts.update({'SystemName':'LMM','ServerAddress':'https://api.lmm.best','Logo':'','Footer':'© 2026 LMM','About':'让模型接入更简单。','HomePageContent':'# LMM\n连接模型，开始创造。','legal.user_agreement':'https://example.test/terms','legal.privacy_policy':'https://example.test/privacy','AssistantPersona':'友好、简洁、直接。帮助用户完成模型接入。','AssistantPreConversationPresets':json.dumps([{'id':'getting_started','label':{'default':'从哪里开始？','zh':'从哪里开始？','en':'Where should I start?'},'prompt':{'default':'请帮我完成第一次模型调用。'}},{'id':'model','label':{'default':'帮我选择模型'},'prompt':{'default':'根据我的用途推荐适合的模型。'}},{'id':'billing','label':{'default':'了解计费方式'},'prompt':{'default':'解释平台的计费方式。'}}],ensure_ascii=False), 'GitHubOAuthEnabled':True,'GitHubClientId':'Iv1.preview-local','GitHubClientSecret':'','OAuthRegisterEnabled':True,'SMTPServer':'smtp.example.test','SMTPPort':'587','SMTPAccount':'support@example.test','SMTPFrom':'LMM','SMTPStartTLSEnabled':True})
 return {k: str(v).lower() if isinstance(v,bool) else str(v) for k,v in opts.items()}
class Fixture:
 def __init__(self):self.options=make_options();self.requests=[];self.writes=[];self.fail_options=False
 async def route(self,route):
  req=route.request;path=urlparse(req.url).path;method=req.method;self.requests.append([method,path]);data=[];status=200
  if path=='/api/user/auth/refresh':data=BUNDLE
  elif path=='/api/setup':data={'status':True}
  elif path=='/api/status':data={'system_name':'LMM','logo':'/logo.png','assistant':{'enabled':True},'announcements_enabled':False,'registration_enabled':False,'email_verification':False,'turnstile_check':False,'quota_per_unit':500000,'version':'0.1.6'}
  elif path=='/api/user/self':data=USER
  elif path=='/api/option/':
   if self.fail_options:status=503;data=None
   elif method=='PUT':
    payload=req.post_data_json;self.options[payload['key']]=payload['value'];self.writes.append(payload);data=None
   else:data=[{'key':k,'value':v} for k,v in self.options.items()]
  elif path=='/api/option/bulk':
   payload=req.post_data_json;self.options.update(payload['values']);self.writes.append(payload);data=None
  elif path=='/api/assistant/models':data=['deepseek-v4-flash','claude-sonnet','gpt-5-mini']
  elif path=='/api/group/':data=['default','premium']
  elif path=='/api/release-notes/latest':data=None
  elif path=='/api/notice':data=''
  elif path=='/api/user/self/groups':data={'default':{'desc':'Default','ratio':1}}
  elif path=='/api/user/self/onboarding/todo':data={'items':[],'count':0}
  elif path=='/api/assistant/handoff/latest':data=None
  elif path=='/api/assistant/preferences':data={'enabled':True}
  return await route.fulfill(status=status,content_type='application/json',body=json.dumps({'success':status<400,'message':'' if status<400 else 'Preview: unavailable','data':data},ensure_ascii=False))
async def setup(context, fixture):
 await context.route('**/api/**',fixture.route)
 await context.add_init_script("localStorage.setItem('i18nextLng','zhCN'); document.cookie='vite-ui-theme=light; path=/';")
