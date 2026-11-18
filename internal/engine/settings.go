package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const (
	shortTitleLength    = 15
	detailedTitleLength = 30
)

type Settings struct {
	Provider        string `json:"provider"`
	CodexModel      string `json:"codexModel"`
	Fast            bool   `json:"fast"`
	APIProtocol     string `json:"apiProtocol"`
	APIBaseURL      string `json:"apiBaseUrl"`
	APIModel        string `json:"apiModel"`
	HasAPIKey       bool   `json:"hasApiKey"`
	FallbackEnabled bool   `json:"fallbackEnabled"`
	MaxTitleLength  int    `json:"maxTitleLength"`
	apiLegacy       bool
}

type settingsPatch struct {
	Provider        *string         `json:"provider"`
	CodexModel      *string         `json:"codexModel"`
	Fast            *bool           `json:"fast"`
	APIProtocol     *string         `json:"apiProtocol"`
	APIBaseURL      *string         `json:"apiBaseUrl"`
	APIModel        *string         `json:"apiModel"`
	APIKey          *string         `json:"apiKey"`
	FallbackEnabled *bool           `json:"fallbackEnabled"`
	MaxTitleLength  json.RawMessage `json:"maxTitleLength"`
}

func loadSettings(ctx context.Context, q queryer) (Settings, []byte, error) {
	var s Settings
	var secret []byte
	err := q.QueryRowContext(ctx, `SELECT provider,codex_model,fast,api_protocol,api_url,api_model,api_key,fallback,max_length,api_legacy
		FROM settings WHERE id=1`).Scan(&s.Provider, &s.CodexModel, &s.Fast, &s.APIProtocol, &s.APIBaseURL, &s.APIModel, &secret, &s.FallbackEnabled, &s.MaxTitleLength, &s.apiLegacy)
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
		saved := s
		patch(&s.Provider, p.Provider)
		patch(&s.CodexModel, p.CodexModel)
		patch(&s.Fast, p.Fast)
		patch(&s.APIProtocol, p.APIProtocol)
		patch(&s.APIBaseURL, p.APIBaseURL)
		patch(&s.APIModel, p.APIModel)
		patch(&s.FallbackEnabled, p.FallbackEnabled)
		if len(p.MaxTitleLength) > 0 {
			var length int
			if err := json.Unmarshal(p.MaxTitleLength, &length); err != nil {
				return Settings{}, errors.New("标题长度只能选择简短或详细预设")
			}
			s.MaxTitleLength = length
		}
		s.apiLegacy = saved.useLegacy(s.APIProtocol, s.APIBaseURL)
		if err = validateSettings(&s); err != nil {
			return Settings{}, err
		}
		if !saved.sameAPIAddress(s.APIBaseURL) && p.APIKey == nil && len(key) > 0 {
			return Settings{}, errors.New("修改 API 地址时请重新填写或清除密钥")
		}
		if p.APIKey != nil {
			if *p.APIKey != "" && s.APIBaseURL == "" {
				return Settings{}, errors.New("请先填写 API 地址")
			}
			key, err = protectAPIKey(*p.APIKey)
			if err != nil {
				return Settings{}, err
			}
		}
		s.HasAPIKey = len(key) > 0
		_, err = tx.ExecContext(ctx, `UPDATE settings SET provider=?,codex_model=?,fast=?,api_protocol=?,api_url=?,api_model=?,api_key=?,fallback=?,max_length=?,api_legacy=? WHERE id=1`,
			s.Provider, s.CodexModel, s.Fast, s.APIProtocol, s.APIBaseURL, s.APIModel, key, s.FallbackEnabled, s.MaxTitleLength, s.apiLegacy)
		return s, err
	})
}

func validateSettings(s *Settings) error {
	s.Provider = strings.TrimSpace(s.Provider)
	s.CodexModel = strings.TrimSpace(s.CodexModel)
	s.APIModel = strings.TrimSpace(s.APIModel)
	s.APIBaseURL = strings.TrimSpace(s.APIBaseURL)
	if s.Provider != "codex" && s.Provider != "api" {
		return errors.New("请选择 Codex 或自定义 API")
	}
	if s.CodexModel == "" || !validString(s.CodexModel, 256) || !validString(s.APIModel, 256) {
		return errors.New("模型名称格式无效")
	}
	if s.MaxTitleLength != shortTitleLength && s.MaxTitleLength != detailedTitleLength {
		return errors.New("标题长度只能选择简短或详细预设")
	}
	return validateAPISettings(s.APIProtocol, s.APIBaseURL, s.APIModel, s.apiLegacy)
}

func (s Settings) sameAPIAddress(base string) bool {
	return s.APIBaseURL == strings.TrimSpace(base)
}

func (s Settings) useLegacy(protocol, base string) bool {
	return s.apiLegacy && protocol == s.APIProtocol && s.sameAPIAddress(base)
}

func protectAPIKey(key string) ([]byte, error) {
	if !validString(key, 4096) || strings.TrimSpace(key) != key {
		return nil, errors.New("API 密钥格式无效")
	}
	secret, err := credential([]byte(key), false)
	if err != nil {
		return nil, errors.New("无法处理 API 密钥")
	}
	return secret, nil
}
