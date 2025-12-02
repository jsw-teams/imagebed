package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// HTTPConfig HTTP 服务相关配置。
type HTTPConfig struct {
	Addr string `json:"addr"` // 例如 ":9000"
}

// DatabaseConfig 数据库配置。
type DatabaseConfig struct {
	DSN string `json:"dsn"` // 例如 postgres://user:pass@localhost:5432/imagebed?sslmode=disable
}

// R2Config Cloudflare R2 配置。
type R2Config struct {
	AccountID       string `json:"account_id"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region"`   // auto / eu / ...
	Endpoint        string `json:"endpoint"` // 可留空，自动根据 account_id + region 推导
}

// TurnstileConfig Turnstile 基础配置。
// 现在 site_key/secret_key 已经移到数据库里，这里只保留一个开关和可选的后端 secret。
type TurnstileConfig struct {
	Enabled   bool   `json:"enabled"`
	SecretKey string `json:"secret_key"`
}

// ModerationConfig 图片审查相关配置。
type ModerationConfig struct {
	Enabled            bool   `json:"enabled"`
	ThirdPartyEndpoint string `json:"third_party_endpoint"`
	ThirdPartyAPIKey   string `json:"third_party_api_key"`
}

// AppConfig 应用层配置。
type AppConfig struct {
	MaxUploadBytes   int64    `json:"max_upload_bytes"`
	AllowedMimeTypes []string `json:"allowed_mime_types"`
}

// Config 总配置。
type Config struct {
	HTTP       HTTPConfig       `json:"http"`
	Database   DatabaseConfig   `json:"database"`
	R2         R2Config         `json:"r2"`
	Turnstile  TurnstileConfig  `json:"turnstile"`
	Moderation ModerationConfig `json:"moderation"`
	App        AppConfig        `json:"app"`
}

const (
	defaultMaxUploadBytes int64 = 10 * 1024 * 1024 // 10 MiB
)

// Default 返回一份带默认值的配置（不包含敏感信息）。
func Default() *Config {
	return &Config{
		HTTP: HTTPConfig{
			Addr: ":9000",
		},
		Database: DatabaseConfig{
			DSN: "",
		},
		R2: R2Config{
			AccountID:       "",
			AccessKeyID:     "",
			SecretAccessKey: "",
			Region:          "auto",
			Endpoint:        "",
		},
		Turnstile: TurnstileConfig{
			Enabled:   false,
			SecretKey: "",
		},
		Moderation: ModerationConfig{
			Enabled:            false,
			ThirdPartyEndpoint: "",
			ThirdPartyAPIKey:   "",
		},
		App: AppConfig{
			MaxUploadBytes: defaultMaxUploadBytes,
			AllowedMimeTypes: []string{
				"image/jpeg",
				"image/png",
				"image/gif",
				"image/webp",
			},
		},
	}
}

// applyDefaults 在从 JSON 读出之后，补全一些必需的默认值。
func (c *Config) applyDefaults() {
	if c.HTTP.Addr == "" {
		c.HTTP.Addr = ":9000"
	}
	if c.R2.Region == "" {
		c.R2.Region = "auto"
	}
	if c.App.MaxUploadBytes == 0 {
		c.App.MaxUploadBytes = defaultMaxUploadBytes
	}
	if len(c.App.AllowedMimeTypes) == 0 {
		c.App.AllowedMimeTypes = []string{
			"image/jpeg",
			"image/png",
			"image/gif",
			"image/webp",
		}
	}
}

// Load 从指定路径读取配置；
// - 如果文件不存在，会自动用 Default() 生成一份 config.json 写入磁盘并返回默认配置；
// - 如果文件存在但内容为空，也会写入默认配置；
// - 如果 JSON 结构不合法，返回错误。
func Load(path string) (*Config, error) {
	// 没有指定路径时，直接返回默认配置（一般不会出现，因为 main 会传 -config）
	if path == "" {
		cfg := Default()
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %w", err)
		}

		// 文件不存在：自动创建目录 + 默认配置文件
		cfg := Default()

		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("make config dir: %w", err)
		}
		if err := writeConfigFile(path, cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	// 处理空文件：当作默认配置，并写回去。
	if len(bytes.TrimSpace(data)) == 0 {
		cfg := Default()
		if err := writeConfigFile(path, cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	cfg := Default() // 先带默认值，再覆盖
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

// writeConfigFile 把 cfg 写到 path，使用缩进和较安全的权限。
func writeConfigFile(path string, cfg *Config) error {
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal default config: %w", err)
	}
	if err := os.WriteFile(path, buf, 0o640); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// MustLoad 是一个方便函数，失败时直接 panic / log fatal。
func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}
	return cfg
}