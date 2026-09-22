// Package oidcprovider implements the LMM first-party subproject identity
// boundary. It has no passwords, account creation or alternate login methods.
// Browser identity and current-session validation are injected by api.lmm.best.
package oidcprovider

import (
 "context"
 "crypto"
 "crypto/rand"
 "crypto/rsa"
 "crypto/sha256"
 "crypto/subtle"
 "crypto/x509"
 "encoding/base64"
 "encoding/json"
 "errors"
 "fmt"
 "math/big"
 "net"
 "net/http"
 "net/url"
 "strings"
 "sync"
 "time"
)

var ErrMissing = errors.New("identity record missing")
var ErrDenied = errors.New("identity unavailable or revoked")

// Store.Take MUST consume a record atomically across every server instance.
// Only token hashes, never raw bearer credentials, are used as storage keys.
type Store interface {
 Set(context.Context,string,[]byte,int64,string) error
 Get(context.Context,string)([]byte,error)
 Take(context.Context,string)([]byte,error)
 Delete(context.Context,string) error
 Families(context.Context,string)([][]byte,error)
}
type Identity struct {
 Subject string `json:"subject"`
 Name string `json:"name"`
 SessionID string `json:"session_id"`
 SessionVersion int64 `json:"session_version"`
 AuthVersion int64 `json:"auth_version"`
}
type Client struct {
 ID string `json:"client_id"`
 Name string `json:"name"`
 RedirectURIs []string `json:"redirect_uris"`
 Resources []string `json:"resources"`
 Scopes []string `json:"scopes"`
 Controller string `json:"controller"`
 Loopback bool `json:"loopback"`
}
type Resource struct {
 ID string `json:"id"`
 URI string `json:"uri"`
 Secret string `json:"-"`
 SecretEnv string `json:"secret_env"`
}
type Config struct {
 Issuer string
 Clients []Client
 Resources []Resource
 Key *rsa.PrivateKey
 Store Store
 BrowserIdentity func(context.Context,*http.Request)(Identity,error)
 ValidateIdentity func(context.Context,Identity)error
 TrustedProxies []*net.IPNet
}
type bucket struct {start time.Time; count int}
type Provider struct {
 config Config
 origin string
 kid string
 clients map[string]Client
 resources map[string]Resource
 mu sync.Mutex
 rates map[string]bucket
}
var knownScopes=[]string{"openid","profile","coweft:read","coweft:write","coweft:propose","coweft:vote"}
func contains(values []string,value string)bool {for _,v:=range values {if v==value{return true}};return false}
func digest(s string)string {h:=sha256.Sum256([]byte(s));return base64.RawURLEncoding.EncodeToString(h[:])}
func same(a,b string)bool {x:=sha256.Sum256([]byte(a));y:=sha256.Sum256([]byte(b));return subtle.ConstantTimeCompare(x[:],y[:])==1}
func random() (string,error) {var b [32]byte;if _,e:=rand.Read(b[:]);e!=nil{return "",e};return base64.RawURLEncoding.EncodeToString(b[:]),nil}
func New(c Config)(*Provider,error) {
 u,e:=url.Parse(c.Issuer);if e!=nil||u.Scheme!="https"||u.Host==""||u.User!=nil||u.Path!="/oidc"||u.RawQuery!=""||u.Fragment!="" {return nil,fmt.Errorf("OIDC issuer must be a fixed HTTPS origin followed by /oidc")}
 if c.Key==nil||c.Key.N.BitLen()<2048||c.Store==nil||c.BrowserIdentity==nil||c.ValidateIdentity==nil {return nil,fmt.Errorf("OIDC requires an RSA key, persistent storage and trusted identity callbacks")}
 if e:=c.Key.Validate();e!=nil{return nil,e}
 der,e:=x509.MarshalPKIXPublicKey(&c.Key.PublicKey);if e!=nil{return nil,e};kid:=digest(string(der))[:22]
 p:=&Provider{config:c,origin:u.Scheme+"://"+u.Host,kid:kid,clients:map[string]Client{},resources:map[string]Resource{},rates:map[string]bucket{}}
 for _,r:=range c.Resources {
  uri,e:=url.Parse(r.URI);if r.ID==""||len(r.Secret)<32||e!=nil||uri.Scheme!="https"||uri.Host==""||uri.User!=nil||uri.RawQuery!=""||uri.Fragment!="" {return nil,fmt.Errorf("invalid OIDC resource registration")}
  if _,exists:=p.resources[r.ID];exists{return nil,fmt.Errorf("duplicate resource")};p.resources[r.ID]=r
 }
 if len(p.resources)==0{return nil,fmt.Errorf("at least one resource must be explicitly registered")}
 for _,client:=range c.Clients {
  if client.ID==""||client.Name==""||len(client.RedirectURIs)==0||len(client.Resources)==0||(client.Controller!="human"&&client.Controller!="agent") {return nil,fmt.Errorf("invalid client registration")}
  if _,ok:=p.clients[client.ID];ok{return nil,fmt.Errorf("duplicate client")}
  for _,redirect:=range client.RedirectURIs {if !validRedirectTemplate(redirect,client.Loopback){return nil,fmt.Errorf("invalid redirect for %s",client.ID)}}
  for _,s:=range client.Scopes {if !contains(knownScopes,s){return nil,fmt.Errorf("unknown scope %s",s)}}
  for _,uri:=range client.Resources {found:=false;for _,r:=range c.Resources {if r.URI==uri {found=true}};if !found{return nil,fmt.Errorf("unregistered resource")}}
  p.clients[client.ID]=client
 }
 return p,nil
}
func validRedirectTemplate(raw string,loopback bool)bool {
 u,e:=url.Parse(raw);if e!=nil||u.User!=nil||u.Fragment!=""||u.RawQuery!=""||u.Host==""||u.Path=="" {return false}
 if loopback{return u.Scheme=="http"&&u.Hostname()=="127.0.0.1"&&u.Port()==""}
 return u.Scheme=="https"
}
func redirectMatches(c Client,raw string)bool {
 for _,registered:=range c.RedirectURIs {
  if !c.Loopback&&raw==registered{return true}
  if c.Loopback {u,e:=url.Parse(raw);t,_:=url.Parse(registered);if e==nil&&u.Scheme=="http"&&u.Hostname()=="127.0.0.1"&&u.Port()!=""&&u.User==nil&&u.RawQuery==""&&u.Fragment==""&&u.Path==t.Path {return true}}
 };return false
}
func(p *Provider) put(ctx context.Context,key string,v any,expires int64,owner string)error {b,e:=json.Marshal(v);if e!=nil{return e};return p.config.Store.Set(ctx,key,b,expires,owner)}
func(p *Provider) get(ctx context.Context,key string,v any,take bool)error {var b []byte;var e error;if take{b,e=p.config.Store.Take(ctx,key)}else{b,e=p.config.Store.Get(ctx,key)};if e!=nil{return e};return json.Unmarshal(b,v)}
func jsonResponse(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_ = json.NewEncoder(w).Encode(v)}
func fail(w http.ResponseWriter,status int,code string){jsonResponse(w,status,map[string]string{"error":code})}
func(p *Provider) transport(r *http.Request)bool {
 if r.TLS!=nil{return true};host,_,e:=net.SplitHostPort(r.RemoteAddr);if e!=nil{return false};ip:=net.ParseIP(host)
 for _,network:=range p.config.TrustedProxies {if network.Contains(ip)&&len(r.Header.Values("X-Forwarded-Proto"))==1&&r.Header.Get("X-Forwarded-Proto")=="https"{return true}};return false
}
func(p *Provider) allowed(r *http.Request)bool {
 host,_,_:=net.SplitHostPort(r.RemoteAddr);key:=host+":"+r.URL.Path;now:=time.Now()
 p.mu.Lock();defer p.mu.Unlock();b,exists:=p.rates[key]
 if !exists||now.Sub(b.start)>=time.Minute {if len(p.rates)>=4096{for k,v:=range p.rates{if now.Sub(v.start)>=time.Minute{delete(p.rates,k)}}};if !exists&&len(p.rates)>=4096{return false};b=bucket{start:now}}
 b.count++;p.rates[key]=b;return b.count<=120
}
func(p *Provider) Handler()http.Handler {return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
 w.Header().Set("Cache-Control","no-store");w.Header().Set("Pragma","no-cache");w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("Referrer-Policy","strict-origin");w.Header().Set("X-Frame-Options","DENY")
 if !p.transport(r){fail(w,400,"https_required");return};if !p.allowed(r){w.Header().Set("Retry-After","60");fail(w,429,"rate_limited");return}
 ctx,cancel:=context.WithTimeout(r.Context(),15*time.Second);defer cancel();r=r.WithContext(ctx)
 switch r.Method+" "+r.URL.Path {
 case "GET /oidc/.well-known/openid-configuration","GET /.well-known/oauth-authorization-server/oidc":p.discovery(w)
 case "GET /api/oidc/jwks":p.jwks(w)
 case "GET /api/user/auth/oidc/authorize":p.authorize(w,r)
 case "POST /api/user/auth/oidc/consent":p.consent(w,r)
 case "POST /api/oidc/token":p.token(w,r)
 case "GET /api/oidc/userinfo","POST /api/oidc/userinfo":p.userinfo(w,r)
 case "POST /api/oidc/introspect":p.introspect(w,r)
 case "POST /api/oidc/revoke":p.revoke(w,r)
 case "GET /api/user/auth/oidc/grants":p.grants(w,r)
 case "POST /api/user/auth/oidc/grants":p.manage(w,r)
 default:http.NotFound(w,r)
 }
})}
func(p *Provider) discovery(w http.ResponseWriter){jsonResponse(w,200,map[string]any{
 "issuer":p.config.Issuer,"authorization_endpoint":p.origin+"/api/user/auth/oidc/authorize","token_endpoint":p.origin+"/api/oidc/token","userinfo_endpoint":p.origin+"/api/oidc/userinfo","jwks_uri":p.origin+"/api/oidc/jwks","introspection_endpoint":p.origin+"/api/oidc/introspect","revocation_endpoint":p.origin+"/api/oidc/revoke",
 "response_types_supported":[]string{"code"},"grant_types_supported":[]string{"authorization_code","refresh_token"},"subject_types_supported":[]string{"public"},"id_token_signing_alg_values_supported":[]string{"RS256"},"token_endpoint_auth_methods_supported":[]string{"none"},"revocation_endpoint_auth_methods_supported":[]string{"none"},"introspection_endpoint_auth_methods_supported":[]string{"client_secret_basic"},"code_challenge_methods_supported":[]string{"S256"},"scopes_supported":knownScopes,"claims_supported":[]string{"iss","sub","aud","exp","iat","nonce","name","preferred_username","at_hash"},"authorization_response_iss_parameter_supported":true,
 })}
func(p *Provider) jwks(w http.ResponseWriter){jsonResponse(w,200,map[string]any{"keys":[]any{map[string]any{"kty":"RSA","use":"sig","alg":"RS256","kid":p.kid,"n":base64.RawURLEncoding.EncodeToString(p.config.Key.N.Bytes()),"e":base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.config.Key.E)).Bytes())}}})}
func(p *Provider) sign(claims any)(string,error){h,_:=json.Marshal(map[string]string{"alg":"RS256","typ":"JWT","kid":p.kid});b,e:=json.Marshal(claims);if e!=nil{return "",e};raw:=base64.RawURLEncoding.EncodeToString(h)+"."+base64.RawURLEncoding.EncodeToString(b);sum:=sha256.Sum256([]byte(raw));sig,e:=rsa.SignPKCS1v15(rand.Reader,p.config.Key,crypto.SHA256,sum[:]);if e!=nil{return "",e};return raw+"."+base64.RawURLEncoding.EncodeToString(sig),nil}
func parseForm(w http.ResponseWriter,r *http.Request)(url.Values,error){
 if !strings.HasPrefix(r.Header.Get("Content-Type"),"application/x-www-form-urlencoded"){return nil,ErrDenied};r.Body=http.MaxBytesReader(w,r.Body,16*1024);if e:=r.ParseForm();e!=nil{return nil,e};if r.URL.RawQuery!=""{return nil,ErrDenied};for _,v:=range r.PostForm{if len(v)!=1{return nil,ErrDenied}};return r.PostForm,nil
}
func browserCookie(r *http.Request,name string)(string,bool){value:="";count:=0;for _,c:=range r.Cookies(){if c.Name==name{count++;value=c.Value}};return value,count==1&&len(value)==43}
func setCookie(w http.ResponseWriter,name,value string,age int){http.SetCookie(w,&http.Cookie{Name:name,Value:value,Path:"/",HttpOnly:true,Secure:true,SameSite:http.SameSiteStrictMode,MaxAge:age})}
