package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RequireInstalled(isInstalled func() bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isInstalled() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error":     "setup_required",
				"setup_url": "/setup/",
			})
			return
		}
		c.Next()
	}
}
