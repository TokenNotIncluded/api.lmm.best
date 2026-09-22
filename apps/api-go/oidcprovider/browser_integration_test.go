package oidcprovider

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "net/url"
 "os"
 "regexp"
 "strings"
 "testing"
)
func TestBrowserConsentPKCEAndSingleUse(t *testing.T){
 p,_:=setup(t)
 authorize:=httptest.NewRequest("GET","https://api.lmm.best/api/user/auth/oidc/authorize?"+query().Encode(),nil)
 page:=httptest.NewRecorder();p.BrowserEntryHandler().ServeHTTP(page,authorize)
 if page.Code!=200{t.Fatal(page.Code,page.Body.String())}
 fields:=map[string]string{}
 for _,match:=range regexp.MustCompile(`name="(transaction|csrf)" value="([^"]+)"`).FindAllStringSubmatch(page.Body.String(),-1){fields[match[1]]=match[2]}
 if fields["transaction"]==""||fields["csrf"]==""{t.Fatal("missing consent fields")}
 form:=url.Values{"transaction":{fields["transaction"]},"csrf":{fields["csrf"]},"decision":{"allow"}}
 request:=httptest.NewRequest("POST","https://api.lmm.best/api/user/auth/oidc/consent",strings.NewReader(form.Encode()))
 request.Header.Set("Content-Type","application/x-www-form-urlencoded");request.Header.Set("Origin","https://api.lmm.best")
 for _,cookie:=range page.Result().Cookies(){request.AddCookie(cookie)}
 response:=httptest.NewRecorder();p.Handler().ServeHTTP(response,request)
 if response.Code!=303{t.Fatal(response.Code,response.Body.String())}
 target,err:=url.Parse(response.Header().Get("Location"));if err!=nil{t.Fatal(err)}
 code:=target.Query().Get("code")
 if code==""||target.Query().Get("state")!=query().Get("state")||target.Query().Get("iss")!=p.config.Issuer{t.Fatal("invalid authorization response",target)}
 values:=url.Values{"grant_type":{"authorization_code"},"client_id":{"coweft-web"},"redirect_uri":{"https://forum.example/auth/callback"},"resource":{"https://forum.example/mcp"},"code":{code},"code_verifier":{strings.Repeat("v",43)}}
 response=post(p,"/api/oidc/token",values,false);if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
 var tokens map[string]any;json.Unmarshal(response.Body.Bytes(),&tokens)
 parts:=strings.Split(tokens["id_token"].(string),".");raw,_:=base64.RawURLEncoding.DecodeString(parts[1]);var claims map[string]any;json.Unmarshal(raw,&claims)
 if claims["nonce"]!=query().Get("nonce")||claims["aud"]!="coweft-web"||claims["sub"]!="lmm:7"{t.Fatal(claims)}
 if post(p,"/api/oidc/token",values,false).Code!=400{t.Fatal("authorization code replay accepted")}
}
func TestLoginBridgePreservesValidatedFlowWithoutFrontendRedirectAssumption(t *testing.T){
 p,_:=setup(t);p.config.BrowserIdentity=func(context.Context,*http.Request)(Identity,error){return Identity{},ErrDenied}
 request:=httptest.NewRequest("GET","https://api.lmm.best/api/user/auth/oidc/authorize?"+query().Encode(),nil)
 response:=httptest.NewRecorder();p.BrowserEntryHandler().ServeHTTP(response,request)
 if response.Code!=200||!strings.Contains(response.Body.String(),"已登录，继续授权")||!strings.Contains(response.Body.String(),`href="/login"`){t.Fatal(response.Body.String())}
 bad:=query();bad.Set("redirect_uri","https://attacker.example")
 response=httptest.NewRecorder();p.BrowserEntryHandler().ServeHTTP(response,httptest.NewRequest("GET","https://api.lmm.best/api/user/auth/oidc/authorize?"+bad.Encode(),nil))
 if response.Code!=400{t.Fatal("unregistered redirect rendered")}
}
// Only a PUBLIC key/receipt leaves this test; no private key or bearer token.
func TestExportInteroperabilityFixture(t *testing.T){
 path:=os.Getenv("COWEFT_INTEROP_FIXTURE");if path==""{t.Skip("fixture export not requested")}
 p,_:=setup(t);tokens:=tokenFor(t,p)
 snapshot:=`{"version":1,"source":"https://forum.example","id":"a59cdd36-9229-4d17-a2e5-41c810e122b1","title":"Cross-language publication","body":"Public evidence from the Go provider.","kind":"discussion","revision":1}`
 response:=httptest.NewRecorder();p.AttestationHandler().ServeHTTP(response,receiptRequest(tokens["access_token"].(string),digest(snapshot)))
 if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
 var result map[string]string;json.Unmarshal(response.Body.Bytes(),&result)
 public:=httptest.NewRecorder();p.jwks(public);var jwks any;json.Unmarshal(public.Body.Bytes(),&jwks)
 fixture,err:=json.Marshal(map[string]any{"jwks":jwks,"envelope":map[string]string{"payload":base64.RawURLEncoding.EncodeToString([]byte(snapshot)),"receipt":result["receipt"]}})
 if err!=nil{t.Fatal(err)};if err=os.WriteFile(path,fixture,0600);err!=nil{t.Fatal(err)}
}
