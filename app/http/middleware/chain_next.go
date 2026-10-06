package middleware

import (
	"github.com/gin-gonic/gin"
	contractshttp "github.com/goravel/framework/contracts/http"
)

// continueChain runs the rest of the gin handlers without building the
// Goravel request. Building that request JSON-decodes an application/json body.
func continueChain(ctx contractshttp.Context) {
	if ginCtx := ginInstance(ctx); ginCtx != nil {
		ginCtx.Next()
		return
	}
	ctx.Request().Next()
}

func ginInstance(ctx contractshttp.Context) *gin.Context {
	withInstance, ok := ctx.(interface{ Instance() *gin.Context })
	if !ok || withInstance == nil {
		return nil
	}
	return withInstance.Instance()
}
