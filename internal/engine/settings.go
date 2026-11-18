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
	CodexEffort     string `json:"codexEffort"`
	ClaudeModel     string `json:"claudeModel"`
	ClaudeEffort    string `json:"claudeEffort"`
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
	nativePatch
	Provider        *string         `json:"provider"`
	APIProtocol     *string         `json:"apiProtocol"`
	APIBaseURL      *string         `json:"apiBaseUrl"`
	APIModel        *string         `json:"apiModel"`
	APIKey          *string         `json:"apiKey"`
	FallbackEnabled *bool           `json:"fallbackEnabled"`
	MaxTitleLength  json.RawMessage `json:"maxTitleLength"`
}

type nativePatch struct {
	CodexModel   *string `json:"codexModel"`
	CodexEffort  *string `json:"codexEffort"`
	ClaudeModel  *string `json:"claudeModel"`
	ClaudeEffort *string `json:"claudeEffort"`
	Fast         *bool   `json:"fast"`
}

func (p nativePatch) apply(s *Settings) {
	patch(&s.CodexModel, p.CodexModel)
	patch(&s.CodexEffort, p.CodexEffort)
	patch(&s.ClaudeModel, p.ClaudeModel)
	patch(&s.ClaudeEffort, p.ClaudeEffort)
	patch(&s.Fast, p.Fast)
}

func (s Settings) nativeChoice(provider string) (model, effort string) {
	if provider == "claude" {
		return s.ClaudeModel, s.ClaudeEffort
	}
	return s.CodexModel, s.CodexEffort
}

func loadSettings(ctx context.Context, q queryer) (Settings, []byte, error) {
	var s Settings
	var secret []byte
	err := q.QueryRowContext(ctx, `SELECT provider,codex_model,codex_effort,claude_model,claude_effort,fast,api_protocol,api_url,api_model,api_key,fallback,max_length,api_legacy
		FROM settings WHERE id=1`).Scan(&s.Provider, &s.CodexModel, &s.CodexEffort, &s.ClaudeModel, &s.ClaudeEffort,
		&s.Fast, &s.APIProtocol, &s.APIBaseURL, &s.APIModel, &secret, &s.FallbackEnabled, &s.MaxTitleLength, &s.apiLegacy)
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
	saved, err := e.getSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	s, err := p.draft(saved)
	if err != nil {
		return Settings{}, err
	}
	if err := e.validateChangedChoices(ctx, saved, s); err != nil {
		return Settings{}, err
	}
	return transact(ctx, e.db, func(tx *sql.Tx) (Settings, error) {
		current, key, err := loadSettings(ctx, tx)
		if err != nil {
			return Settings{}, err
		}
		if current != saved {
			return Settings{}, errors.New("配置已在其他操作中更新，请重试保存")
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
		_, err = tx.ExecContext(ctx, `UPDATE settings SET provider=?,codex_model=?,codex_effort=?,claude_model=?,claude_effort=?,
			fast=?,api_protocol=?,api_url=?,api_model=?,api_key=?,fallback=?,max_length=?,api_legacy=? WHERE id=1`,
			s.Provider, s.CodexModel, s.CodexEffort, s.ClaudeModel, s.ClaudeEffort, s.Fast, s.APIProtocol,
			s.APIBaseURL, s.APIModel, key, s.FallbackEnabled, s.MaxTitleLength, s.apiLegacy)
		return s, err
	})
}

func (p settingsPatch) draft(saved Settings) (Settings, error) {
	s := saved
	p.nativePatch.apply(&s)
	patch(&s.Provider, p.Provider)
	patch(&s.APIProtocol, p.APIProtocol)
	patch(&s.APIBaseURL, p.APIBaseURL)
	patch(&s.APIModel, p.APIModel)
	patch(&s.FallbackEnabled, p.FallbackEnabled)
	if len(p.MaxTitleLength) > 0 {
		if err := json.Unmarshal(p.MaxTitleLength, &s.MaxTitleLength); err != nil || string(p.MaxTitleLength) == "null" {
			return Settings{}, errors.New("标题长度只能选择简短或详细预设")
		}
	}
	s.apiLegacy = saved.useLegacy(s.APIProtocol, s.APIBaseURL)
	err := validateSettings(&s)
	return s, err
}

func (e *Engine) validateChangedChoices(ctx context.Context, saved, draft Settings) error {
	for _, provider := range []string{"codex", "claude"} {
		oldModel, oldEffort := saved.nativeChoice(provider)
		model, effort := draft.nativeChoice(provider)
		activated := draft.Provider == provider && draft.Provider != saved.Provider
		if activated || model != oldModel || effort != oldEffort {
			target := draft
			target.Provider = provider
			if err := e.checkSubscription(ctx, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSettings(s *Settings) error {
	s.Provider = strings.TrimSpace(s.Provider)
	s.APIModel = strings.TrimSpace(s.APIModel)
	s.APIBaseURL = strings.TrimSpace(s.APIBaseURL)
	if s.Provider != "codex" && s.Provider != "claude" && s.Provider != "api" {
		return errors.New("请选择 Codex、Claude Code 或自定义 API")
	}
	if !validNativeValue(s.CodexModel) || !validNativeValue(s.ClaudeModel) || !validString(s.APIModel, 256) {
		return errors.New("模型名称格式无效")
	}
	if !validNativeValue(s.CodexEffort) || !validNativeValue(s.ClaudeEffort) {
		return errors.New("思考强度格式无效")
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
