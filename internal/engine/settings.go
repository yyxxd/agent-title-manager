package engine

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strings"
)

type Settings struct {
	Provider        string `json:"provider"`
	CodexModel      string `json:"codexModel"`
	Fast            bool   `json:"fast"`
	APIBaseURL      string `json:"apiBaseUrl"`
	APIModel        string `json:"apiModel"`
	HasAPIKey       bool   `json:"hasApiKey"`
	FallbackEnabled bool   `json:"fallbackEnabled"`
	MaxTitleLength  int    `json:"maxTitleLength"`
}

type settingsPatch struct {
	Provider        *string `json:"provider"`
	CodexModel      *string `json:"codexModel"`
	Fast            *bool   `json:"fast"`
	APIBaseURL      *string `json:"apiBaseUrl"`
	APIModel        *string `json:"apiModel"`
	APIKey          *string `json:"apiKey"`
	FallbackEnabled *bool   `json:"fallbackEnabled"`
	MaxTitleLength  *int    `json:"maxTitleLength"`
}

func loadSettings(ctx context.Context, q queryer) (Settings, []byte, error) {
	var s Settings
	var secret []byte
	err := q.QueryRowContext(ctx, `SELECT provider,codex_model,fast,api_url,api_model,api_key,fallback,max_length
		FROM settings WHERE id=1`).Scan(&s.Provider, &s.CodexModel, &s.Fast, &s.APIBaseURL, &s.APIModel, &secret, &s.FallbackEnabled, &s.MaxTitleLength)
	s.HasAPIKey = len(secret) > 0
	return s, secret, err
}

func (e *Engine) getSettings(ctx context.Context) (Settings, error) {
	s, _, err := loadSettings(ctx, e.db)
	return s, err
}

func patch[T any](place *T, value *T) {
	if value != nil {
		*place = *value
	}
}

func (e *Engine) saveSettings(ctx context.Context, p settingsPatch) (Settings, error) {
	return transact(ctx, e.db, func(tx *sql.Tx) (Settings, error) {
		s, key, err := loadSettings(ctx, tx)
		if err != nil {
			return Settings{}, err
		}
		oldURL := s.APIBaseURL
		patch(&s.Provider, p.Provider)
		patch(&s.CodexModel, p.CodexModel)
		patch(&s.Fast, p.Fast)
		patch(&s.APIBaseURL, p.APIBaseURL)
		patch(&s.APIModel, p.APIModel)
		patch(&s.FallbackEnabled, p.FallbackEnabled)
		patch(&s.MaxTitleLength, p.MaxTitleLength)
		if err = validateSettings(&s); err != nil {
			return Settings{}, err
		}
		if oldURL != s.APIBaseURL && p.APIKey == nil && len(key) > 0 {
			return Settings{}, errors.New("修改 API 地址时请重新填写或清除密钥")
		}
		if p.APIKey != nil {
			switch {
			case !validString(*p.APIKey, 4096) || strings.TrimSpace(*p.APIKey) != *p.APIKey:
				return Settings{}, errors.New("API 密钥格式无效")
			case *p.APIKey != "" && s.APIBaseURL == "":
				return Settings{}, errors.New("请先填写 API 地址")
			}
			key, err = credential([]byte(*p.APIKey), false)
			if err != nil {
				return Settings{}, errors.New("无法加密保存 API 密钥")
			}
		}
		s.HasAPIKey = len(key) > 0
		_, err = tx.ExecContext(ctx, `UPDATE settings SET provider=?,codex_model=?,fast=?,api_url=?,api_model=?,api_key=?,fallback=?,max_length=? WHERE id=1`,
			s.Provider, s.CodexModel, s.Fast, s.APIBaseURL, s.APIModel, key, s.FallbackEnabled, s.MaxTitleLength)
		return s, err
	})
}

func validateSettings(s *Settings) error {
	s.Provider = strings.TrimSpace(s.Provider)
	s.CodexModel = strings.TrimSpace(s.CodexModel)
	s.APIModel = strings.TrimSpace(s.APIModel)
	s.APIBaseURL = strings.TrimRight(strings.TrimSpace(s.APIBaseURL), "/")
	if s.Provider != "codex" && s.Provider != "api" {
		return errors.New("请选择 Codex 或自定义 API")
	}
	if s.CodexModel == "" || !validString(s.CodexModel, 256) || !validString(s.APIModel, 256) {
		return errors.New("模型名称格式无效")
	}
	if s.MaxTitleLength < 8 || s.MaxTitleLength > 48 {
		return errors.New("标题长度必须在 8 到 48 个 Unicode 字符之间")
	}
	if s.APIBaseURL != "" {
		_, err := chatEndpoint(s.APIBaseURL)
		return err
	}
	return nil
}

func chatEndpoint(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || len(base) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.ForceQuery || u.RawQuery != "" {
		return "", errors.New("API 地址必须为不含账号、查询参数或片段的完整 URL")
	}
	ip := net.ParseIP(u.Hostname())
	local := strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", errors.New("API 地址必须使用 HTTPS，本机服务可使用 HTTP")
	}
	if strings.HasSuffix(u.Path, "/chat/completions") {
		return u.String(), nil
	}
	return u.JoinPath("chat", "completions").String(), nil
}
