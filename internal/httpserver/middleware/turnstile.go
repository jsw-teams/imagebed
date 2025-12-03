package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jsw-teams/imagebed/internal/turnstile"
)

// Turnstile 中间件：
// - 通过 isEnabled() 动态判断当前是否启用 Turnstile
// - 通过 getVerifier() 获取最新的 Verifier（支持运行时更新配置）
// - 从多种位置尝试读取 token：
//   1) POST form:  cf-turnstile-response
//   2) POST form:  turnstile_token
//   3) Header:     X-Turnstile-Token
//   4) Query:      cf-turnstile-response（主要用于调试）
func Turnstile(
	getVerifier func() *turnstile.Verifier,
	isEnabled func() bool,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 未启用时直接放行
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

		// 优先从表单读取（multipart/form-data）
		token := c.PostForm("cf-turnstile-response")
		if token == "" {
			token = c.PostForm("turnstile_token")
		}

		// 其次从 Header 读取，适用于 JSON 登录等场景
		if token == "" {
			token = c.GetHeader("X-Turnstile-Token")
		}

		// 最后从 query 读取，主要用于调试或某些特殊客户端
		if token == "" {
			token = c.Query("cf-turnstile-response")
		}

		if token == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "missing_turnstile_token",
			})
			return
		}

		ok, err := v.Verify(c.Request.Context(), token, c.ClientIP())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error":  "turnstile_verification_failed",
				"detail": err.Error(),
			})
			return
		}
		if !ok {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "turnstile_verification_failed",
			})
			return
		}

		c.Next()
	}
}