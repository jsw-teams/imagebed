package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/models"
)

// 管理员登录状态使用的 Cookie 名称
const adminSessionCookieName = "imagebed_admin_session"

// ---- 内部工具函数 ----

// isAdminLoggedIn 用于统一判断当前请求是否已登录管理员。
// 既给 AdminAuthRequired 中间件用，也给 router.go 调用。
func IsAdminLoggedIn(c *gin.Context) bool {
	cookie, err := c.Request.Cookie(adminSessionCookieName)
	if err != nil {
		return false
	}
	if cookie.Value != "ok" {
		return false
	}
	return true
}

// setAdminSessionCookie 设置长效（例如 30 天）的管理员登录 Cookie。
func setAdminSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    "ok",
		Path:     "/",                         // 整站可用：/admin /api 都能带上
		HttpOnly: true,                        // JS 无法读写，减少被盗风险
		Secure:   true,                        // 只在 HTTPS 发送
		SameSite: http.SameSiteLaxMode,        // 正常同站导航不会被挡
		Expires:  time.Now().Add(30 * 24 * time.Hour), // 30 天
	})
}

// clearAdminSessionCookie 清除登录 Cookie。
func clearAdminSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ---- 对外导出的中间件 & Handler ----

// AdminAuthRequired：用于保护 /api/admin 下需要管理员权限的接口。
func AdminAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAdminLoggedIn(c) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}

// HandleAdminLogin：登录接口 /api/admin/login
// 成功后设置 Cookie，前端只需要 fetch(..., { credentials: "include" }).
func HandleAdminLogin(getDB func() *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		db := getDB()
		if db == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db_not_ready"})
			return
		}

		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}

		req.Username = strings.TrimSpace(req.Username)
		req.Password = strings.TrimSpace(req.Password)
		if req.Username == "" || req.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing_fields"})
			return
		}

		ok, err := models.CheckAdminCredentials(c.Request.Context(), db, req.Username, req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":  "auth_failed",
				"detail": err.Error(),
			})
			return
		}
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_credentials"})
			return
		}

		// 登录成功：写 Cookie
		setAdminSessionCookie(c)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// HandleAdminLogout：注销接口 /api/admin/logout
func HandleAdminLogout() gin.HandlerFunc {
	return func(c *gin.Context) {
		clearAdminSessionCookie(c)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// HandleAdminSessionStatus：前端检查当前是否登录的接口 /api/admin/session
func HandleAdminSessionStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		loggedIn := IsAdminLoggedIn(c)
		c.JSON(http.StatusOK, gin.H{
			"logged_in": loggedIn,
		})
	}
}