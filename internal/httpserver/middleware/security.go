package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders 统一设置一些安全相关的 HTTP 响应头。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Content-Security-Policy",
			"default-src 'self'; "+
				// 自己的脚本 + 内联脚本 + Cloudflare Insights + Turnstile
				"script-src 'self' 'unsafe-inline' https://static.cloudflareinsights.com https://challenges.cloudflare.com; "+
				// 自己的样式 + 内联样式
				"style-src 'self' 'unsafe-inline'; "+
				// 允许本站图片、data: 图片以及 Turnstile 相关图片
				"img-src 'self' data: https://challenges.cloudflare.com; "+
				// Turnstile 使用 iframe 加载
				"frame-src https://challenges.cloudflare.com; "+
				// AJAX / fetch 仅连回本站，另外放行 Turnstile/Insights 如后续需要可补充
				"connect-src 'self'; "+
				// 只允许本站作为 frame 容器
				"frame-ancestors 'self';")

		c.Next()
	}
}