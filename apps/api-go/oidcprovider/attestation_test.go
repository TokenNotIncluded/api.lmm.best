package oidcprovider

import (
 "context"
 "crypto"
 "crypto/rsa"
 "crypto/sha256"
 "encoding/base64"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "net/url"
 "strings"
 "testing"
)
func receiptRequest(token,contentDigest string)*http.Request {
 body,_:=json.Marshal(map[string]string{"token":token,"digest":contentDigest,"purpose":"public-thread-v1"})
 request:=httptest.NewRequest("POST","https://api.lmm.best/api/oidc/attest",strings.NewReader(string(body)))
 request.Header.Set("Content-Type","application/json");request.SetBasicAuth("coweft",strings.Repeat("s",32));return request
}
func TestPublicReceiptRequiresNodeAndUserAndCannotBecomeACredential(t *testing.T){
 p,_:=setup(t);tokens:=tokenFor(t,p);access:=tokens["access_token"].(string)
 request:=receiptRequest(access,digest("public content"));response:=httptest.NewRecorder();p.AttestationHandler().ServeHTTP(response,request)
 if response.Code!=200{t.Fatal(response.Code,response.Body.String())}
 var result map[string]string;json.Unmarshal(response.Body.Bytes(),&result)
 parts:=strings.Split(result["receipt"],".");if len(parts)!=3{t.Fatal("invalid receipt")}
 headerBytes,_:=base64.RawURLEncoding.DecodeString(parts[0]);var header map[string]string;json.Unmarshal(headerBytes,&header)
 if header["typ"]!="coweft-event+jwt"{t.Fatal("wrong receipt type")}
 signature,_:=base64.RawURLEncoding.DecodeString(parts[2]);sum:=sha256.Sum256([]byte(parts[0]+"."+parts[1]))
 if rsa.VerifyPKCS1v15(&p.config.Key.PublicKey,crypto.SHA256,sum[:],signature)!=nil{t.Fatal("signature invalid")}
 claimBytes,_:=base64.RawURLEncoding.DecodeString(parts[1]);var claims map[string]any;json.Unmarshal(claimBytes,&claims)
 if claims["digest"]!=digest("public content")||claims["sub"]!="lmm:7"||claims["aud"]!="urn:coweft:public-thread-v1"{t.Fatal(claims)}
 if _,_,err:=p.access(context.Background(),result["receipt"]);err==nil{t.Fatal("public receipt accepted as bearer token")}
 request=receiptRequest(access,digest("forged origin"));request.Header.Set("Authorization","Bearer "+access)
 response=httptest.NewRecorder();p.AttestationHandler().ServeHTTP(response,request)
 if response.Code!=401{t.Fatal("user token alone could impersonate resource approval")}
 post(p,"/api/oidc/revoke",url.Values{"token":{access},"client_id":{"coweft-web"}},false)
 response=httptest.NewRecorder();p.AttestationHandler().ServeHTTP(response,receiptRequest(access,digest("public content")))
 if response.Code!=401{t.Fatal("revoked grant could issue receipt")}
}
