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

// R2Config Cloudflare R2 全局默认配置（可选）。
//
// ⚠️ 注意：在当前版本中，实际用于上传的 R2 配置已经迁移到数据库 buckets
// 表中，按「每个桶独立账号 / endpoint」进行管理。
// 这里的 R2Config 仅保留为：
//
//   1. 兼容旧版 config.json 结构；
//   2. 作为你手工新建桶时的“默认模板来源”（如果以后需要，可以在后台表单中添加
//      “从全局配置导入”按钮）。
//
// 运行时的核心逻辑（handlers/images.go 等）不再直接依赖 Config.R2。
type R2Config struct {
	AccountID       string `json:"account_id"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region"`   // auto / eu / ...
	Endpoint        string `json:"endpoint"` // 可留空，自动根据 account_id + region 推导
}

// TurnstileConfig Turnstile 基础配置。
//
// site_key / secret_key 的最终生效配置已经迁移到数据库表 turnstile_settings 中，
// router / handlers 会优先以数据库为准。
// 这里保留一个开关和可选的后端 secret，仅用于兼容旧结构或你将来扩展。
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
	// 单文件最大上传大小（字节）
	MaxUploadBytes int64 `json:"max_upload_bytes"`
	// 允许上传的 MIME 类型白名单
	AllowedMimeTypes []string `json:"allowed_mime_types"`
}

// Config 总配置。
//
// 注意：Installed 用于标记系统是否已经完成初始化安装，
// router.go 会用它来决定是否强制跳转 /setup。
type Config struct {
	HTTP       HTTPConfig       `json:"http"`
	Database   DatabaseConfig   `json:"database"`
	R2         R2Config         `json:"r2"`        // 全局默认 / 兼容字段，实际 R2 配置以 buckets 表为准
	Turnstile  TurnstileConfig  `json:"turnstile"` // 最终生效配置在 turnstile_settings 表
	Moderation ModerationConfig `json:"moderation"`
	App        AppConfig        `json:"app"`

	Installed bool `json:"installed"` // 是否已完成初始化安装
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
		Installed: false,
	}
}

// applyDefaults 在从 JSON 读出之后，补全一些必需的默认值。
func (c *Config) applyDefaults() {
	if c.HTTP.Addr == "" {
		c.HTTP.Addr = ":9000"
	}
	// R2.Region 只作为“全局默认模板”存在，不影响每桶配置
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
//
//   - 如果文件不存在，会自动用 Default() 生成一份 config.json 写入磁盘并返回默认配置；
//   - 如果文件存在但内容为空，也会写入默认配置；
//   - 如果 JSON 结构不合法，返回错误。
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

	// 先带默认值，再覆盖，避免新字段缺失时出现零值问题
	cfg := Default()
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
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, buf, 0o640); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Save 用于把当前配置写回到磁盘（例如初始化安装完成后）。
// router.go 会在 /setup 完成时调用它。
func Save(path string, cfg *Config) error {
	if path == "" {
		return fmt.Errorf("empty config path")
	}
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	return writeConfigFile(path, cfg)
}

// MustLoad 是一个方便函数，失败时直接 panic。
func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}
	return cfg
}