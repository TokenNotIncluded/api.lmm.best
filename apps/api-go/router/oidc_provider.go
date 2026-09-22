package router

import (
 "net/http"
 "github.com/LIghtJUNction/api.lmm.best/model"
 "github.com/LIghtJUNction/api.lmm.best/service"
 "github.com/gin-gonic/gin"
)

func mountSubprojectOIDC(router *gin.Engine)error {
 provider,err:=service.ConfigureSubprojectOIDC(model.DB);if err!=nil{return err}
 handler:=http.Handler(http.NotFoundHandler());if provider!=nil{handler=provider.Handler()}
 for _,path:=range []string{
  "/oidc/.well-known/openid-configuration",
  "/.well-known/oauth-authorization-server/oidc",
  "/api/oidc/jwks","/api/oidc/token","/api/oidc/userinfo","/api/oidc/introspect","/api/oidc/revoke",
  "/api/user/auth/oidc/authorize","/api/user/auth/oidc/consent","/api/user/auth/oidc/grants",
 } {router.Any(path,gin.WrapH(handler))}
 return nil
}
