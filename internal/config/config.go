package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type HTTPConfig struct {
	Addr string `json:"addr"`
}

type DatabaseConfig struct {
	DSN string `json:"dsn"`
}

type R2Config struct {
	AccountID       string `json:"account_id"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region"`            // "auto" or "eu" 等
	Endpoint        string `json:"endpoint"`          // 可选，自定义 endpoint，高级用法
}

type TurnstileConfig struct {
	Enabled   bool   `json:"enabled"`
	SecretKey string `json:"secret_key"`
}

type ModerationConfig struct {
	Enabled            bool   `json:"enabled"`
	ThirdPartyEndpoint string `json:"third_party_endpoint"`
	ThirdPartyAPIKey   string `json:"third_party_api_key"`
}

type AppConfig struct {
	MaxUploadBytes   int64    `json:"max_upload_bytes"`
	AllowedMimeTypes []string `json:"allowed_mime_types"`
}

// Installed 标记应用是否已经完成初始化安装
type Config struct {
	Installed  bool             `json:"installed"`
	HTTP       HTTPConfig       `json:"http"`
	Database   DatabaseConfig   `json:"database"`
	R2         R2Config         `json:"r2"`
	Turnstile  TurnstileConfig  `json:"turnstile"`
	Moderation ModerationConfig `json:"moderation"`
	App        AppConfig        `json:"app"`
}

func Load(path string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("config path is required")
	}

	var cfg Config

	b, err := os.ReadFile(path)
	if err != nil {
		// 如果文件不存在，则使用默认空配置，进入安装模式
		if os.IsNotExist(err) {
			applyDefaults(&cfg)
			return &cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.HTTP.Addr == "" {
		cfg.HTTP.Addr = ":8080"
	}
	if cfg.R2.Region == "" {
		cfg.R2.Region = "auto"
	}
	if cfg.App.MaxUploadBytes == 0 {
		cfg.App.MaxUploadBytes = 10 * 1024 * 1024 // 默认 10MB
	}
	if len(cfg.App.AllowedMimeTypes) == 0 {
		cfg.App.AllowedMimeTypes = []string{
			"image/jpeg",
			"image/png",
			"image/gif",
			"image/webp",
		}
	}
}

// Save 将配置写回到指定文件（用于 setup 完成后持久化）
func Save(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// 0600：只有 owner 可读写
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
