package middleware

import (
	"github.com/gin-gonic/gin"
)

// SecurityHeaders 为所有响应加上一组安全头，避免 XSS / clickjacking 等
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// 对纯 API 项目影响不大，但能防很多 XSS 场景
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		c.Next()
	}
}
