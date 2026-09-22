package oidcprovider

import (
 "context"
 "crypto/sha256"
 "encoding/base64"
 "encoding/json"
 "net/http"
 "strings"
 "time"
)
func jsonUnmarshal(b []byte,v any)error{return json.Unmarshal(b,v)}
type capability struct {FamilyID string `json:"family_id"`;ExpiresAt int64 `json:"expires_at"`}
func validVerifier(v string)bool {if len(v)<43||len(v)>128{return false};for _,r:=range v{if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~",r){return false}};return true}
func(p *Provider) token(w http.ResponseWriter,r *http.Request){
 if len(r.Header.Values("Authorization"))!=0{fail(w,400,"invalid_client");return};f,e:=parseForm(w,r);if e!=nil{fail(w,400,"invalid_request");return}
 client,ok:=p.clients[f.Get("client_id")];if !ok{fail(w,400,"invalid_client");return};var g grant
 switch f.Get("grant_type") {
 case "authorization_code":
  code:=f.Get("code");if len(code)!=43{fail(w,400,"invalid_grant");return}
  if e=p.get(r.Context(),"code:"+digest(code),&g,true);e!=nil{
   var old capability;if p.get(r.Context(),"used-code:"+digest(code),&old,false)==nil {var previous grant;if p.get(r.Context(),"family:"+old.FamilyID,&previous,false)==nil&&previous.Request.ClientID==client.ID{_ = p.config.Store.Delete(r.Context(),"family:"+old.FamilyID)}}
   fail(w,400,"invalid_grant");return
  }
  if g.Request.ClientID!=client.ID||g.Request.Redirect!=f.Get("redirect_uri")||g.Request.Resource!=f.Get("resource")||!validVerifier(f.Get("code_verifier"))||!same(digest(f.Get("code_verifier")),g.Request.Challenge)||p.config.ValidateIdentity(r.Context(),g.Identity)!=nil{fail(w,400,"invalid_grant");return}
  g.ID,e=random();if e!=nil{fail(w,503,"server_error");return};if e=p.put(r.Context(),"family:"+g.ID,g,g.ExpiresAt,g.Identity.Subject);e!=nil{fail(w,503,"server_error");return}
  if e=p.put(r.Context(),"used-code:"+digest(code),capability{g.ID,g.ExpiresAt},g.ExpiresAt,g.Identity.Subject);e!=nil{_ = p.config.Store.Delete(r.Context(),"family:"+g.ID);fail(w,503,"server_error");return}
 case "refresh_token":
  raw:=f.Get("refresh_token");if !strings.HasPrefix(raw,"lmm_r_")||len(raw)!=49{fail(w,400,"invalid_grant");return};var cap capability
  if e=p.get(r.Context(),"refresh:"+digest(raw),&cap,true);e!=nil{
   var used capability;if p.get(r.Context(),"used-refresh:"+digest(raw),&used,false)==nil {var old grant;if p.get(r.Context(),"family:"+used.FamilyID,&old,false)==nil&&old.Request.ClientID==client.ID{_ = p.config.Store.Delete(r.Context(),"family:"+used.FamilyID)}}
   fail(w,400,"invalid_grant");return
  }
  if e=p.get(r.Context(),"family:"+cap.FamilyID,&g,false);e!=nil||g.Request.ClientID!=client.ID||g.Request.Resource!=f.Get("resource")||p.config.ValidateIdentity(r.Context(),g.Identity)!=nil{fail(w,400,"invalid_grant");return}
  // Rotation never recreates a family. A racing replay permanently revokes it.
  if e=p.put(r.Context(),"used-refresh:"+digest(raw),cap,g.ExpiresAt,g.Identity.Subject);e!=nil{fail(w,503,"server_error");return}
  if f.Get("scope")!=""&&f.Get("scope")!=strings.Join(g.Request.Scopes," "){fail(w,400,"invalid_scope");return}
 default:fail(w,400,"unsupported_grant_type");return
 }
 if g.ExpiresAt<=time.Now().Unix(){fail(w,400,"invalid_grant");return}
 response,e:=p.issue(r.Context(),g,f.Get("grant_type")=="authorization_code");if e!=nil{fail(w,503,"server_error");return};jsonResponse(w,200,response)
}
func(p *Provider) issue(ctx context.Context,g grant,idToken bool)(map[string]any,error){
 a,e:=random();if e!=nil{return nil,e};b,e:=random();if e!=nil{return nil,e};access:="lmm_o_"+a;refresh:="lmm_r_"+b;expires:=time.Now().Add(10*time.Minute).Unix();if expires>g.ExpiresAt{expires=g.ExpiresAt}
 idle:=time.Now().Add(7*24*time.Hour).Unix();if idle>g.ExpiresAt{idle=g.ExpiresAt}
 if e=p.put(ctx,"access:"+digest(access),capability{g.ID,expires},expires,g.Identity.Subject);e!=nil{return nil,e}
 if e=p.put(ctx,"refresh:"+digest(refresh),capability{g.ID,idle},idle,g.Identity.Subject);e!=nil{return nil,e}
 out:=map[string]any{"access_token":access,"token_type":"Bearer","expires_in":expires-time.Now().Unix(),"refresh_token":refresh,"scope":strings.Join(g.Request.Scopes," ")}
 if idToken&&contains(g.Request.Scopes,"openid") {
  sum:=sha256.Sum256([]byte(access));claims:=map[string]any{"iss":p.config.Issuer,"sub":g.Identity.Subject,"aud":g.Request.ClientID,"iat":time.Now().Unix(),"exp":expires,"nonce":g.Request.Nonce,"at_hash":base64.RawURLEncoding.EncodeToString(sum[:16])}
  if contains(g.Request.Scopes,"profile"){claims["name"]=g.Identity.Name;claims["preferred_username"]=g.Identity.Name}
  token,e:=p.sign(claims);if e!=nil{return nil,e};out["id_token"]=token
 }
 return out,nil
}
func(p *Provider) access(ctx context.Context,raw string)(grant,capability,error){
 var g grant;var cap capability
 if !strings.HasPrefix(raw,"lmm_o_")||len(raw)!=49{return g,cap,ErrDenied}
 if e:=p.get(ctx,"access:"+digest(raw),&cap,false);e!=nil{return g,cap,e}
 if cap.ExpiresAt<=time.Now().Unix(){return g,cap,ErrDenied}
 if e:=p.get(ctx,"family:"+cap.FamilyID,&g,false);e!=nil{return g,cap,e}
 if g.ExpiresAt<=time.Now().Unix()||p.config.ValidateIdentity(ctx,g.Identity)!=nil{return g,cap,ErrDenied}
 return g,cap,nil
}
func bearer(r *http.Request)(string,bool){values:=r.Header.Values("Authorization");if len(values)!=1||!strings.HasPrefix(values[0],"Bearer "){return "",false};raw:=strings.TrimPrefix(values[0],"Bearer ");return raw,len(raw)==49}
func(p *Provider) userinfo(w http.ResponseWriter,r *http.Request){raw,ok:=bearer(r);if !ok{w.Header().Set("WWW-Authenticate","Bearer");fail(w,401,"invalid_token");return};g,_,e:=p.access(r.Context(),raw);if e!=nil||!contains(g.Request.Scopes,"openid"){fail(w,401,"invalid_token");return};out:=map[string]any{"sub":g.Identity.Subject};if contains(g.Request.Scopes,"profile"){out["name"]=g.Identity.Name;out["preferred_username"]=g.Identity.Name};jsonResponse(w,200,out)}
func(p *Provider) introspect(w http.ResponseWriter,r *http.Request){
 id,secret,ok:=r.BasicAuth();resource,found:=p.resources[id];if !ok||!found||!same(secret,resource.Secret){w.Header().Set("WWW-Authenticate",`Basic realm="LMM resource introspection"`);fail(w,401,"invalid_client");return}
 f,e:=parseForm(w,r);if e!=nil{fail(w,400,"invalid_request");return};if f.Get("resource")!=resource.URI{jsonResponse(w,200,map[string]bool{"active":false});return}
 g,cap,e:=p.access(r.Context(),f.Get("token"));if e!=nil||g.Request.Resource!=resource.URI{jsonResponse(w,200,map[string]bool{"active":false});return}
 out:=map[string]any{"active":true,"iss":p.config.Issuer,"sub":g.Identity.Subject,"aud":g.Request.Resource,"client_id":g.Request.ClientID,"scope":strings.Join(g.Request.Scopes," "),"exp":cap.ExpiresAt,"controller":g.Controller,"grant_id":g.ID,"token_type":"Bearer"}
 if contains(g.Request.Scopes,"profile"){out["name"]=g.Identity.Name};jsonResponse(w,200,out)
}
func(p *Provider) revoke(w http.ResponseWriter,r *http.Request){
 f,e:=parseForm(w,r);if e!=nil||len(r.Header.Values("Authorization"))!=0{fail(w,400,"invalid_request");return};client,ok:=p.clients[f.Get("client_id")];if !ok{fail(w,400,"invalid_client");return}
 raw:=f.Get("token");var cap capability;prefix:="access:";if strings.HasPrefix(raw,"lmm_r_"){prefix="refresh:"}
 if p.get(r.Context(),prefix+digest(raw),&cap,false)==nil {var g grant;if p.get(r.Context(),"family:"+cap.FamilyID,&g,false)==nil&&g.Request.ClientID==client.ID {if e=p.config.Store.Delete(r.Context(),"family:"+g.ID);e!=nil{fail(w,503,"server_error");return}}}
 w.WriteHeader(200)
}
