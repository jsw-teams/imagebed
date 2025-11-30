package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders 统一设置一些安全相关的 HTTP 响应头。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 基础安全头
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// 这里不再开启老旧的 X-XSS-Protection，现代浏览器基本都废弃了
		// c.Header("X-XSS-Protection", "0")

		// Content-Security-Policy
		//
		// 需求：
		// 1. 允许本站静态资源：default-src 'self'
		// 2. 允许页面里的 <style> 和 style="" 内联样式（我们大量使用）
		// 3. 允许页面里的内联 <script>（/setup、/upload、/admin 都是内联脚本）
		// 4. 允许 Cloudflare Insights 这个域名的外部脚本
		//
		// 注意：因为有 'unsafe-inline'，XSS 防护主要还得靠后端输入校验，
		//       但对于你这个私人小图床 + 内网后台来说，这个 tradeoff 是合理的。
		c.Header("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://static.cloudflareinsights.com; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"connect-src 'self'; "+
				"frame-ancestors 'self';")

		c.Next()
	}
}

