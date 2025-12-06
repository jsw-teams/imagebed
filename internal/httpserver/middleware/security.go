package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders 统一设置一些安全相关的 HTTP 响应头。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()

		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-XSS-Protection", "0")

		h.Set("Content-Security-Policy",
			"default-src 'self';"+
				// 自己的脚本 + 内联脚本 + Cloudflare Insights + Turnstile
				"script-src 'self' 'unsafe-inline' https://static.cloudflareinsights.com https://challenges.cloudflare.com;"+
				// 自己的样式 + 内联样式
				"style-src 'self' 'unsafe-inline';"+
				// 允许本站图片、data: 图片以及所有 https 图片（方便以后在控制台里嵌预览）
				"img-src 'self' data: https:;"+
				// Turnstile 使用 iframe 加载
				"frame-src https://challenges.cloudflare.com;"+
				// AJAX / fetch 仅连回本站
				"connect-src 'self';"+
				// 只允许本站作为 frame 容器
				"frame-ancestors 'self';"+
				// 只允许本站作为表单提交目标 & base URL
				"form-action 'self';"+
				"base-uri 'self';",
		)

		c.Next()
	}
}