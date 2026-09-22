package oidcprovider

import (
 "html/template"
 "net/http"
 "net/url"
 "strings"
 "time"
)

type authorization struct {
 ClientID string `json:"client_id"`
 Redirect string `json:"redirect_uri"`
 Resource string `json:"resource"`
 Scopes []string `json:"scopes"`
 State string `json:"state"`
 Nonce string `json:"nonce"`
 Challenge string `json:"challenge"`
}
type flow struct {Request authorization;Identity Identity;Binding string;CSRF string}
type grant struct {
 ID string `json:"id"`
 Identity Identity `json:"identity"`
 Request authorization `json:"request"`
 Controller string `json:"controller"`
 CreatedAt int64 `json:"created_at"`
 ExpiresAt int64 `json:"expires_at"`
}
func(p *Provider) parseAuthorization(q url.Values)(authorization,error){
 var a authorization
 for k,v:=range q {if len(v)!=1||!contains([]string{"client_id","redirect_uri","response_type","scope","resource","state","nonce","code_challenge","code_challenge_method"},k){return a,ErrDenied}}
 c,ok:=p.clients[q.Get("client_id")];if !ok||!redirectMatches(c,q.Get("redirect_uri"))||q.Get("response_type")!="code"||q.Get("code_challenge_method")!="S256"||!contains(c.Resources,q.Get("resource")){return a,ErrDenied}
 if len(q.Get("state"))<16||len(q.Get("state"))>512||len(q.Get("code_challenge"))!=43{return a,ErrDenied}
 challenge:=q.Get("code_challenge");for _,r:=range challenge {if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_",r){return a,ErrDenied}}
 scopes:=strings.Fields(q.Get("scope"));if len(scopes)==0{return a,ErrDenied};seen:=map[string]bool{}
 for _,scope:=range scopes {if seen[scope]||!contains(c.Scopes,scope){return a,ErrDenied};seen[scope]=true}
 if contains(scopes,"openid")&&(len(q.Get("nonce"))<16||len(q.Get("nonce"))>512){return a,ErrDenied}
 for _,s:=range scopes {if strings.HasPrefix(s,"coweft:")&&!contains(scopes,"coweft:read"){return a,ErrDenied}}
 return authorization{c.ID,q.Get("redirect_uri"),q.Get("resource"),scopes,q.Get("state"),q.Get("nonce"),challenge},nil
}
func(p *Provider) authorize(w http.ResponseWriter,r *http.Request){
 q,e:=url.ParseQuery(r.URL.RawQuery);if e!=nil{fail(w,400,"invalid_request");return};a,e:=p.parseAuthorization(q);if e!=nil{fail(w,400,"invalid_request");return}
 identity,e:=p.config.BrowserIdentity(r.Context(),r)
 if e!=nil {
  // The existing account login owns authentication. This endpoint never
  // accepts usernames, account IDs, passwords or dashboard tokens from forms.
  http.Redirect(w,r,p.origin+"/login?redirect="+url.QueryEscape(r.URL.RequestURI()),http.StatusSeeOther);return
 }
 if identity.Subject==""||p.config.ValidateIdentity(r.Context(),identity)!=nil{fail(w,401,"login_required");return}
 tx,e:=random();if e!=nil{fail(w,503,"server_error");return};binding,e:=random();if e!=nil{fail(w,503,"server_error");return};csrf,e:=random();if e!=nil{fail(w,503,"server_error");return}
 f:=flow{a,identity,digest(binding),csrf};if e=p.put(r.Context(),"flow:"+digest(tx),f,time.Now().Add(5*time.Minute).Unix(),identity.Subject);e!=nil{fail(w,503,"server_error");return}
 setCookie(w,"__Host-lmm-oidc-flow",binding,300)
 p.page(w,pageData{Title:"授权给 "+p.clients[a.ClientID].Name,Name:identity.Name,Client:p.clients[a.ClientID].Name,Controller:p.clients[a.ClientID].Controller,Resource:a.Resource,Scopes:a.Scopes,Transaction:tx,CSRF:csrf,Action:"/api/user/auth/oidc/consent"})
}
func(p *Provider) consent(w http.ResponseWriter,r *http.Request){
 if r.Header.Get("Origin")!=p.origin{fail(w,403,"csrf_rejected");return};form,e:=parseForm(w,r);if e!=nil{fail(w,400,"invalid_request");return};binding,ok:=browserCookie(r,"__Host-lmm-oidc-flow");if !ok{fail(w,403,"csrf_rejected");return}
 var f flow;if e=p.get(r.Context(),"flow:"+digest(form.Get("transaction")),&f,true);e!=nil{fail(w,400,"expired_authorization");return}
 setCookie(w,"__Host-lmm-oidc-flow","",-1)
 if !same(digest(binding),f.Binding)||!same(form.Get("csrf"),f.CSRF){fail(w,403,"csrf_rejected");return}
 identity,e:=p.config.BrowserIdentity(r.Context(),r);if e!=nil||identity.Subject!=f.Identity.Subject||identity.SessionID!=f.Identity.SessionID||identity.SessionVersion!=f.Identity.SessionVersion||identity.AuthVersion!=f.Identity.AuthVersion||p.config.ValidateIdentity(r.Context(),identity)!=nil{fail(w,401,"identity_changed");return}
 if form.Get("decision")=="deny"{p.redirect(w,r,f.Request,"","access_denied");return};if form.Get("decision")!="allow"{fail(w,400,"invalid_request");return}
 code,e:=random();if e!=nil{fail(w,503,"server_error");return}
 g:=grant{Identity:identity,Request:f.Request,Controller:p.clients[f.Request.ClientID].Controller,CreatedAt:time.Now().Unix(),ExpiresAt:time.Now().Add(30*24*time.Hour).Unix()}
 if e=p.put(r.Context(),"code:"+digest(code),g,time.Now().Add(120*time.Second).Unix(),identity.Subject);e!=nil{fail(w,503,"server_error");return};p.redirect(w,r,f.Request,code,"")
}
func(p *Provider) redirect(w http.ResponseWriter,r *http.Request,a authorization,code,problem string){u,_:=url.Parse(a.Redirect);q:=u.Query();q.Set("state",a.State);q.Set("iss",p.config.Issuer);if code!=""{q.Set("code",code)};if problem!=""{q.Set("error",problem)};u.RawQuery=q.Encode();http.Redirect(w,r,u.String(),http.StatusSeeOther)}
type pageData struct {Title,Name,Client,Controller,Resource,Transaction,CSRF,Action string;Scopes []string;Grants []grant;Manage bool}
var page=template.Must(template.New("consent").Parse(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}} · LMM</title><style>body{font:15px/1.8 system-ui,sans-serif;background:#f6f8f3;color:#263a30;margin:0;padding:40px 18px}main{max-width:620px;margin:5vh auto;background:white;border:1px solid #e2e8dd;border-radius:18px;padding:32px}h1{font-size:25px}small,p{color:#647362}code{overflow-wrap:anywhere}li{margin:6px 0}button{font:inherit;padding:10px 20px;border:1px solid #cad7c7;background:#263a30;color:white;border-radius:9px;cursor:pointer;margin:6px 8px 6px 0}button[name=decision][value=deny]{background:white;color:#263a30}.grant{border-top:1px solid #e1e8de;padding:18px 0}a{color:#4b7b58}</style><main><small>LMM · 子项目身份与授权</small><h1>{{.Title}}</h1><p>当前账号：{{.Name}}</p>{{if .Manage}}<p>撤销后，网页与 AI 的下一次资源访问会重新检查授权。已公开的内容不会被删除。</p>{{range .Grants}}<div class="grant"><strong>{{.Request.ClientID}}</strong> · {{.Controller}}<p><code>{{.Request.Resource}}</code></p><p>{{range .Request.Scopes}}<code>{{.}}</code> {{end}}</p><form method="post" action="/api/user/auth/oidc/grants"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="grant_id" value="{{.ID}}"><button>撤销这份授权</button></form></div>{{else}}<p>没有有效的子项目授权。</p>{{end}}{{else}}<p><strong>{{.Client}}</strong> 将作为 <strong>{{.Controller}}</strong> 操作者访问：</p><p><code>{{.Resource}}</code></p><ul>{{range .Scopes}}<li><code>{{.}}</code></li>{{end}}</ul><p>登录不会自动授予模型消费权限。人和 AI 使用不同凭证，但归属同一个账号。只同意你准备开放的权限。</p><form method="post" action="{{.Action}}"><input type="hidden" name="transaction" value="{{.Transaction}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><button name="decision" value="allow">允许这些权限</button><button name="decision" value="deny">拒绝</button></form>{{end}}</main></html>`))
func(p *Provider) page(w http.ResponseWriter,data pageData){w.Header().Set("Content-Type","text/html; charset=utf-8");w.Header().Set("Content-Security-Policy","default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'");_ = page.Execute(w,data)}
func(p *Provider) grants(w http.ResponseWriter,r *http.Request){
 identity,e:=p.config.BrowserIdentity(r.Context(),r);if e!=nil||p.config.ValidateIdentity(r.Context(),identity)!=nil{http.Redirect(w,r,"/login?redirect=%2Fapi%2Fuser%2Fauth%2Foidc%2Fgrants",303);return}
 records,e:=p.config.Store.Families(r.Context(),identity.Subject);if e!=nil{fail(w,503,"server_error");return};var grants []grant
 for _,record:=range records {var g grant;if jsonUnmarshal(record,&g)==nil&&g.Identity.Subject==identity.Subject&&g.ExpiresAt>time.Now().Unix(){grants=append(grants,g)}}
 csrf,e:=random();if e!=nil{fail(w,503,"server_error");return};if e=p.put(r.Context(),"manage:"+digest(csrf),identity,time.Now().Add(5*time.Minute).Unix(),identity.Subject);e!=nil{fail(w,503,"server_error");return};setCookie(w,"__Host-lmm-oidc-manage",csrf,300)
 p.page(w,pageData{Title:"管理子项目授权",Name:identity.Name,Manage:true,CSRF:csrf,Grants:grants})
}
func(p *Provider) manage(w http.ResponseWriter,r *http.Request){
 if r.Header.Get("Origin")!=p.origin{fail(w,403,"csrf_rejected");return};form,e:=parseForm(w,r);if e!=nil{fail(w,400,"invalid_request");return};cookie,ok:=browserCookie(r,"__Host-lmm-oidc-manage");if !ok||!same(cookie,form.Get("csrf")){fail(w,403,"csrf_rejected");return}
 var original Identity;if e=p.get(r.Context(),"manage:"+digest(cookie),&original,true);e!=nil{fail(w,403,"csrf_rejected");return}
 current,e:=p.config.BrowserIdentity(r.Context(),r);if e!=nil||current.Subject!=original.Subject||current.SessionID!=original.SessionID||p.config.ValidateIdentity(r.Context(),current)!=nil{fail(w,401,"login_required");return}
 var g grant;key:="family:"+form.Get("grant_id");if e=p.get(r.Context(),key,&g,false);e==nil&&g.Identity.Subject==current.Subject {if e=p.config.Store.Delete(r.Context(),key);e!=nil{fail(w,503,"server_error");return}}
 setCookie(w,"__Host-lmm-oidc-manage","",-1);http.Redirect(w,r,"/api/user/auth/oidc/grants",303)
}
