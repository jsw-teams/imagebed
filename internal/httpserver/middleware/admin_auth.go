package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/models"
)

// DBProvider 用来从 Server 里取出数据库连接，避免循环依赖。
type DBProvider func() *pgxpool.Pool

// AdminAuth 要求请求携带 Basic Auth，并校验为有效管理员。
func AdminAuth(dbProvider DBProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		db := dbProvider()
		if db == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "db_not_ready",
			})
			return
		}

		username, password, ok := c.Request.BasicAuth()
		if !ok || username == "" || password == "" {
			c.Header("WWW-Authenticate", `Basic realm="Imagebed Admin"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
			})
			return
		}

		okCred, err := models.CheckAdminCredentials(c.Request.Context(), db, username, password)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error":  "auth_failed",
				"detail": err.Error(),
			})
			return
		}
		if !okCred {
			c.Header("WWW-Authenticate", `Basic realm="Imagebed Admin"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid_credentials",
			})
			return
		}

		c.Set("admin_username", username)
		c.Next()
	}
}
