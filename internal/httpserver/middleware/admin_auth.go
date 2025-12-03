package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/models"
)

// 管理员登录状态的 Cookie 名称
const adminSessionCookieName = "imagebed_admin_session"

// 为了简单，这里只用一个固定值，后续如果要支持多用户/多端再扩展
const adminSessionValue = "1"

// SecurityHeaders 为所有响应附加一些基础安全头
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-XSS-Protection", "1; mode=block")
		// 可以按需再加 CSP 等
		c.Next()
	}
}

// RequireInstalled 用于保护 /api：未完成安装时返回错误
func RequireInstalled(isInstalled func() bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isInstalled() {
			c.AbortWithStatusJSON(503, gin.H{
				"error": "not_installed",
			})
			return
		}
		c.Next()
	}
}

//
// --------- 管理员登录 / 会话相关中间件 ---------
//

// HandleAdminSessionStatus 返回当前会话是否已登录管理员。
// 前端可在 /admin 页面加载时调用 /api/admin/session。
func HandleAdminSessionStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		val, err := c.Cookie(adminSessionCookieName)
		if err != nil || val != adminSessionValue {
			c.JSON(200, gin.H{"authenticated": false})
			return
		}
		c.JSON(200, gin.H{"authenticated": true})
	}
}

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// HandleAdminLogin 处理 /api/admin/login：验证账号密码并写入 Cookie 会话。
func HandleAdminLogin(getDB func() *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		db := getDB()
		if db == nil {
			c.JSON(503, gin.H{"error": "db_not_ready"})
			return
		}

		var req adminLoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid_request"})
			return
		}

		req.Username = strings.TrimSpace(req.Username)
		req.Password = strings.TrimSpace(req.Password)
		if req.Username == "" || req.Password == "" {
			c.JSON(400, gin.H{"error": "missing_fields"})
			return
		}

		ok, err := models.CheckAdminCredentials(c.Request.Context(), db, req.Username, req.Password)
		if err != nil {
			c.JSON(500, gin.H{
				"error":  "auth_failed",
				"detail": err.Error(),
			})
			return
		}
		if !ok {
			c.JSON(401, gin.H{"error": "invalid_credentials"})
			return
		}

		// 登录成功：写入长期 Cookie（例如 30 天）
		maxAge := int((30 * 24 * time.Hour).Seconds())
		// domain 留空 = 当前域名；secure=false 方便本地测试，生产在 HTTPS + 反代下仍然安全
		c.SetCookie(
			adminSessionCookieName,
			adminSessionValue,
			maxAge,
			"/",
			"",
			false, // secure
			true,  // httpOnly
		)

		c.JSON(200, gin.H{"ok": true})
	}
}

// HandleAdminLogout 清除管理员会话 Cookie。
func HandleAdminLogout() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 设置 MaxAge<0 清 Cookie
		c.SetCookie(
			adminSessionCookieName,
			"",
			-1,
			"/",
			"",
			false,
			true,
		)
		c.JSON(200, gin.H{"ok": true})
	}
}

// AdminAuthRequired 保护后台接口：要求已登录管理员。
func AdminAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		val, err := c.Cookie(adminSessionCookieName)
		if err != nil || val != adminSessionValue {
			c.AbortWithStatusJSON(401, gin.H{"error": "admin_not_authenticated"})
			return
		}
		c.Next()
	}
}

// AdminAuth 兼容旧代码的包装：保留原签名，但内部直接走 Cookie 校验。
func AdminAuth(getDB func() *pgxpool.Pool) gin.HandlerFunc {
	_ = getDB // 目前不再需要 DB 做认证，但保留参数以兼容旧调用
	return AdminAuthRequired()
}