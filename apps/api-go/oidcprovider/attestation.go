package oidcprovider

import (
 "context"
 "crypto"
 "crypto/rand"
 "crypto/rsa"
 "crypto/sha256"
 "encoding/base64"
 "encoding/json"
 "io"
 "net/http"
 "time"
)

// AttestationHandler requires BOTH the registered resource's Basic credential
// and an active user grant in the JSON body. A user token alone cannot fabricate
// an origin-node publication. The result is public evidence, NOT a bearer token.
func(p *Provider) AttestationHandler() http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("Cache-Control","no-store");w.Header().Set("X-Content-Type-Options","nosniff")
  if r.Method!="POST" {w.WriteHeader(http.StatusMethodNotAllowed);return}
  if !p.transport(r){fail(w,400,"https_required");return};if !p.allowed(r){fail(w,429,"rate_limited");return}
  ctx,cancel:=context.WithTimeout(r.Context(),15*time.Second);defer cancel();r=r.WithContext(ctx)
  id,secret,ok:=r.BasicAuth();resource,found:=p.resources[id]
  if len(r.Header.Values("Authorization"))!=1||!ok||!found||!same(secret,resource.Secret){fail(w,401,"invalid_resource_client");return}
  if r.Header.Get("Content-Type")!="application/json"{fail(w,400,"invalid_request");return}
  var request struct {Token string `json:"token"`;Digest string `json:"digest"`;Purpose string `json:"purpose"`}
  decoder:=json.NewDecoder(http.MaxBytesReader(w,r.Body,2048));decoder.DisallowUnknownFields()
  if decoder.Decode(&request)!=nil||decoder.Decode(new(any))!=io.EOF||request.Purpose!="public-thread-v1" {fail(w,400,"invalid_request");return}
  g,_,err:=p.access(ctx,request.Token);if err!=nil{fail(w,401,"invalid_token");return}
  if g.Request.Resource!=resource.URI||!contains(g.Request.Scopes,"coweft:write"){fail(w,403,"insufficient_scope");return}
  digestBytes,err:=base64.RawURLEncoding.DecodeString(request.Digest)
  if err!=nil||len(digestBytes)!=32||base64.RawURLEncoding.EncodeToString(digestBytes)!=request.Digest{fail(w,400,"invalid_digest");return}
  claims:=map[string]any{
   "iss":p.config.Issuer,"aud":"urn:coweft:public-thread-v1","sub":g.Identity.Subject,
   "resource":g.Request.Resource,"client_id":g.Request.ClientID,"controller":g.Controller,
   "digest":request.Digest,"purpose":request.Purpose,"iat":time.Now().Unix(),
  }
  if contains(g.Request.Scopes,"profile"){claims["name"]=g.Identity.Name}
  header,_:=json.Marshal(map[string]string{"alg":"RS256","typ":"coweft-event+jwt","kid":p.kid})
  body,err:=json.Marshal(claims);if err!=nil{fail(w,503,"server_error");return}
  unsigned:=base64.RawURLEncoding.EncodeToString(header)+"."+base64.RawURLEncoding.EncodeToString(body)
  sum:=sha256.Sum256([]byte(unsigned));signature,err:=rsa.SignPKCS1v15(rand.Reader,p.config.Key,crypto.SHA256,sum[:])
  if err!=nil{fail(w,503,"server_error");return}
  jsonResponse(w,200,map[string]string{"receipt":unsigned+"."+base64.RawURLEncoding.EncodeToString(signature),"purpose":"public-thread-v1"})
 })
}
