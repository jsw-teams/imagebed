package moderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jsw-teams/imagebed/internal/config"
)

type Decision string

const (
	DecisionApproved Decision = "approved"
	DecisionRejected Decision = "rejected"
)

// Service 同时承担：
// 1. 一方基础审查（MIME 白名单等）
// 2. 预留第三方审查 HTTP 接口（可接阿里、腾讯、CF Images 等）
type Service struct {
	enabled     bool
	allowedMIME map[string]struct{}

	httpClient *http.Client
	endpoint   string
	apiKey     string
}

func NewService(mcfg config.ModerationConfig, allowedMimeTypes []string) *Service {
	allowed := make(map[string]struct{})
	for _, m := range allowedMimeTypes {
		allowed[strings.ToLower(m)] = struct{}{}
	}

	return &Service{
		enabled:     mcfg.Enabled,
		allowedMIME: allowed,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		endpoint: mcfg.ThirdPartyEndpoint,
		apiKey:   mcfg.ThirdPartyAPIKey,
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.enabled
}

func (s *Service) isAllowed(contentType string) bool {
	if len(s.allowedMIME) == 0 {
		return true
	}
	ct := strings.ToLower(contentType)
	if _, ok := s.allowedMIME[ct]; ok {
		return true
	}
	// 允许带 charset 的情况：image/jpeg; charset=binary
	for base := range s.allowedMIME {
		if strings.HasPrefix(ct, base) {
			return true
		}
	}
	return false
}

// Moderate 先进行本地白名单审查，再可选调用第三方接口
func (s *Service) Moderate(ctx context.Context, contentType string, data []byte) (Decision, error) {
	// 一方基础校验（强制）
	if !s.isAllowed(contentType) {
		return DecisionRejected, nil
	}

	// 未启用第三方，直接通过
	if !s.enabled || s.endpoint == "" {
		return DecisionApproved, nil
	}

	// 预留的通用第三方 HTTP JSON 协议
	payload := map[string]any{
		"content_type":  contentType,
		"content_base64": base64.StdEncoding.EncodeToString(data),
	}

	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// 这里策略你可以改成 “出错就拒绝”
		return DecisionApproved, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("moderation service non-200 status")
	}

	var res struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if !res.Allowed {
		return DecisionRejected, nil
	}
	return DecisionApproved, nil
}
