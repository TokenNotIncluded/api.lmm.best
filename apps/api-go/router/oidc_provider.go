package router

import (
 "net/http"
 "github.com/LIghtJUNction/api.lmm.best/model"
 "github.com/LIghtJUNction/api.lmm.best/service"
 "github.com/gin-gonic/gin"
)

func mountSubprojectOIDC(router *gin.Engine)error {
 provider,err:=service.ConfigureSubprojectOIDC(model.DB);if err!=nil{return err}
 handler:=http.Handler(http.NotFoundHandler())
 attestation:=http.Handler(http.NotFoundHandler())
 browser:=http.Handler(http.NotFoundHandler())
 if provider!=nil{handler=provider.Handler();attestation=provider.AttestationHandler();browser=provider.BrowserEntryHandler()}
 for _,path:=range []string{
  "/oidc/.well-known/openid-configuration",
  "/.well-known/oauth-authorization-server/oidc",
  "/api/oidc/jwks","/api/oidc/token","/api/oidc/userinfo","/api/oidc/introspect","/api/oidc/revoke",
  "/api/user/auth/oidc/consent",
 } {router.Any(path,gin.WrapH(handler))}
 router.Any("/api/user/auth/oidc/authorize",gin.WrapH(browser))
 router.Any("/api/user/auth/oidc/grants",gin.WrapH(browser))
 router.Any("/api/oidc/attest",gin.WrapH(attestation))
 return nil
}
