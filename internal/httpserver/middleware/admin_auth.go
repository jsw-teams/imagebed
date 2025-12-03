package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/models"
)

// 管理员登录状态的 Cookie 名称
const adminSessionCookieName = "imagebed_admin_session"

// 简单版本：只要 Cookie 为固定值，就视为已登录
const adminSessionValue = "1"

//
// --------- 管理员登录 / 会话相关中间件 ---------
//

// HandleAdminSessionStatus
// GET /api/admin/session
// 返回当前会话是否已登录管理员。
// 前端可在 /admin 页面加载时调用这个接口。
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

// HandleAdminLogin
// POST /api/admin/login
// 验证账号密码并写入 Cookie 会话。
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

		// 登录成功：写入长期 Cookie（例如 30 天）。
		maxAge := int((30 * 24 * time.Hour).Seconds())
		c.SetCookie(
			adminSessionCookieName,
			adminSessionValue,
			maxAge,
			"/",
			"",
			false, // secure: 本地调试用 false，生产在 HTTPS+反代下仍走加密通道
			true,  // httpOnly
		)

		c.JSON(200, gin.H{"ok": true})
	}
}

// HandleAdminLogout
// POST /api/admin/logout
// 清除管理员会话 Cookie。
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

// AdminAuthRequired
// 用于保护后台 API：要求已登录管理员。
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

// AdminAuth
// 兼容旧代码的包装：保留原签名，但内部直接走 Cookie 校验。
// 现在不再依赖 DB 做每次请求的身份验证。
func AdminAuth(getDB func() *pgxpool.Pool) gin.HandlerFunc {
	_ = getDB // 为兼容保留参数，避免未使用警告
	return AdminAuthRequired()
}