package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jsw-teams/imagebed/internal/turnstile"
)

// 动态读取 verifier / enabled 状态，支持运行过程中更新配置
func Turnstile(
	getVerifier func() *turnstile.Verifier,
	isEnabled func() bool,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isEnabled() {
			c.Next()
			return
		}

		v := getVerifier()
		if v == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "turnstile_not_configured",
			})
			return
		}

		token := c.PostForm("cf-turnstile-response")
		if token == "" {
			token = c.GetHeader("X-Turnstile-Token")
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "missing_turnstile_token",
			})
			return
		}

		ok, err := v.Verify(c.Request.Context(), token, c.ClientIP())
		if err != nil || !ok {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "turnstile_verification_failed",
			})
			return
		}

		c.Next()
	}
}
