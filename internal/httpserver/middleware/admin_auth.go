package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/models"
)

const (
	adminSessionCookieName = "ib_admin_session"
	adminSessionMaxAge     = 30 * 24 * time.Hour // 30 天
)

// 简单的内存 session 存储（进程重启会丢，足够当前场景使用）
type adminSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]string // sessionID -> username
}

func newAdminSessionStore() *adminSessionStore {
	return &adminSessionStore{
		sessions: make(map[string]string),
	}
}

func (s *adminSessionStore) Set(sessionID, username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = username
}

func (s *adminSessionStore) Get(sessionID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.sessions[sessionID]
	return u, ok
}

func (s *adminSessionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

var globalAdminSessions = newAdminSessionStore()

func generateSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// AdminAuthRequired：必须是已登录管理员才能访问
func AdminAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie(adminSessionCookieName)
		if err != nil || cookie == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "admin_not_logged_in",
			})
			return
		}
		if _, ok := globalAdminSessions.Get(cookie); !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "admin_session_invalid",
			})
			return
		}
		c.Next()
	}
}

// HandleAdminLogin：POST /api/admin/login
// 依赖 models.CheckAdminCredentials
func HandleAdminLogin(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		type req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		var body req
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_json"})
			return
		}

		ok, err := models.CheckAdminCredentials(c.Request.Context(), pool, body.Username, body.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "check_credentials_failed"})
			return
		}
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_username_or_password"})
			return
		}

		sessionID, err := generateSessionID()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "generate_session_failed"})
			return
		}
		globalAdminSessions.Set(sessionID, body.Username)

		// 30 天有效期，HttpOnly，路径 /
		httpOnly := true
		secure := c.Request.TLS != nil // 有 https 就标记 secure
		c.SetCookie(
			adminSessionCookieName,
			sessionID,
			int(adminSessionMaxAge.Seconds()),
			"/",
			"",    // domain 留空 = 当前域
			secure,
			httpOnly,
		)

		c.JSON(http.StatusOK, gin.H{
			"ok":       true,
			"username": body.Username,
		})
	}
}

// HandleAdminLogout：POST /api/admin/logout
func HandleAdminLogout() gin.HandlerFunc {
	return func(c *gin.Context) {
		if cookie, err := c.Cookie(adminSessionCookieName); err == nil && cookie != "" {
			globalAdminSessions.Delete(cookie)
		}
		// 清掉 Cookie
		c.SetCookie(adminSessionCookieName, "", -1, "/", "", false, true)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// HandleAdminSessionStatus：GET /api/admin/session
// 用于前端判断“是否已登录”，登录后返回用户名。
func HandleAdminSessionStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie(adminSessionCookieName)
		if err != nil || cookie == "" {
			c.JSON(http.StatusOK, gin.H{
				"logged_in": false,
			})
			return
		}
		if username, ok := globalAdminSessions.Get(cookie); ok {
			c.JSON(http.StatusOK, gin.H{
				"logged_in": true,
				"username":  username,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"logged_in": false,
		})
	}
}

// 你需要在 router.go 里类似这样挂上：
//
//   adminAPI := api.Group("/admin")
//   {
//       adminAPI.POST("/login", middleware.HandleAdminLogin(dbPool))
//       adminAPI.POST("/logout", middleware.HandleAdminLogout())
//       adminAPI.GET("/session", middleware.HandleAdminSessionStatus())
//
//       auth := adminAPI.Group("")
//       auth.Use(middleware.AdminAuthRequired())
//       auth.GET("/buckets", handlers.ListBuckets(...))
//       auth.POST("/buckets", handlers.CreateBucket(...))
//       ...  // 其他需要管理员权限的接口
//   }
//
// 这样：
// - 未登录只允许访问 /api/admin/login & /api/admin/session
// - 登录后访问 /api/admin/buckets 等才会成功。