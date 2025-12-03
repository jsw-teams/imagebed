package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders 统一设置一些安全相关的 HTTP 响应头。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 基础安全头
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// 现代浏览器基本已废弃 X-XSS-Protection，这里显式关闭旧行为
		c.Header("X-XSS-Protection", "0")

		// Content-Security-Policy
		//
		// 1. 默认只允许本站资源：default-src 'self'
		// 2. 允许内联样式 / <style>：style-src 'self' 'unsafe-inline'
		// 3. 允许内联脚本（当前页面脚本大多是内联）：script-src 'self' 'unsafe-inline' ...
		// 4. 允许 Cloudflare Insights 脚本：static.cloudflareinsights.com
		// 5. 允许 Cloudflare Turnstile：
		//    - script-src: 加载 https://challenges.cloudflare.com/turnstile/v0/api.js
		//    - frame-src: 允许 Turnstile 的 iframe
		//    - connect-src: 允许验证请求
		//    - img-src: 允许 Turnstile 相关图片
		c.Header("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://static.cloudflareinsights.com https://challenges.cloudflare.com; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: https://challenges.cloudflare.com; "+
				"connect-src 'self' https://challenges.cloudflare.com; "+
				"frame-src 'self' https://challenges.cloudflare.com; "+
				"frame-ancestors 'self';")

		c.Next()
	}
}

