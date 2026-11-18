package engine

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

const (
	protocolChat      = "chat"
	protocolResponses = "responses"
	protocolAnthropic = "anthropic"
	protocolGemini    = "gemini"
)

type endpointRequest struct {
	APIProtocol string `json:"apiProtocol"`
	APIBaseURL  string `json:"apiBaseUrl"`
	APIModel    string `json:"apiModel"`
}

type apiEndpoints struct {
	GenerationURL string `json:"generationUrl"`
	ModelsURL     string `json:"modelsUrl"`
}

func (e *Engine) previewEndpoints(ctx context.Context, p endpointRequest) (apiEndpoints, error) {
	saved, _, err := loadSettings(ctx, e.db)
	if err != nil {
		return apiEndpoints{}, err
	}
	return resolveEndpoints(p.APIProtocol, p.APIBaseURL, p.APIModel, saved.useLegacy(p.APIProtocol, p.APIBaseURL))
}

func validateAPISettings(protocol, base, model string, legacy bool) error {
	if err := validateAPIProtocol(protocol); err != nil || base == "" {
		return err
	}
	_, err := resolveEndpoints(protocol, base, model, legacy)
	return err
}

func validateAPIProtocol(protocol string) error {
	switch protocol {
	case protocolChat, protocolResponses, protocolAnthropic, protocolGemini:
		return nil
	default:
		return errors.New("请选择有效的 API 协议")
	}
}

func resolveEndpoints(protocol, base, model string, legacy bool) (apiEndpoints, error) {
	if err := validateAPIProtocol(protocol); err != nil {
		return apiEndpoints{}, err
	}
	u, err := parseAPIBase(base)
	if err != nil {
		return apiEndpoints{}, err
	}
	model = strings.TrimSpace(model)
	if !validString(model, 256) {
		return apiEndpoints{}, errors.New("模型名称格式无效")
	}
	if protocol == protocolGemini {
		return geminiEndpoints(u, model)
	}
	if legacy {
		generation := u
		if !strings.HasSuffix(u.Path, "/chat/completions") {
			generation = u.JoinPath("chat", "completions")
		}
		prefix := strings.TrimSuffix(generation.EscapedPath(), "/chat/completions")
		return apiEndpoints{generation.String(), withAPIPath(u, prefix+"/models")}, nil
	}
	return standardEndpoints(u, protocol), nil
}

func parseAPIBase(base string) (*url.URL, error) {
	base = strings.TrimSpace(base)
	u, err := url.Parse(base)
	if err != nil || len(base) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.ForceQuery || u.RawQuery != "" {
		return nil, errors.New("API 地址必须为不含账号、查询参数或片段的完整 URL")
	}
	ip := net.ParseIP(u.Hostname())
	local := strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("API 地址必须使用 HTTPS，本机服务可使用 HTTP")
	}
	return u, nil
}

var apiVersionPath = regexp.MustCompile(`(?:^|/)v[0-9]+(?:[a-z]+[0-9]*)?(?:/|$)`)

func standardEndpoints(u *url.URL, protocol string) apiEndpoints {
	suffix := "/chat/completions"
	switch protocol {
	case protocolResponses:
		suffix = "/responses"
	case protocolAnthropic:
		suffix = "/messages"
	}
	prefix := strings.TrimRight(u.EscapedPath(), "/")
	switch {
	case strings.HasSuffix(prefix, suffix):
		prefix = strings.TrimSuffix(prefix, suffix)
	case strings.HasSuffix(prefix, "/models"):
		prefix = strings.TrimSuffix(prefix, "/models")
	case prefix == "" || (protocol == protocolAnthropic && !apiVersionPath.MatchString(prefix)):
		prefix += "/v1"
	}
	return apiEndpoints{withAPIPath(u, prefix+suffix), withAPIPath(u, prefix+"/models")}
}

func geminiEndpoints(u *url.URL, model string) (apiEndpoints, error) {
	prefix := strings.TrimRight(u.EscapedPath(), "/")
	marker := strings.LastIndex(prefix, "/models/")
	switch {
	case marker >= 0 && strings.HasSuffix(prefix, ":generateContent"):
		if model == "" {
			return apiEndpoints{withAPIPath(u, prefix), withAPIPath(u, prefix[:marker]+"/models")}, nil
		}
		prefix = prefix[:marker]
	case strings.HasSuffix(prefix, "/models"):
		prefix = strings.TrimSuffix(prefix, "/models")
	case prefix == "":
		prefix = "/v1beta"
	}
	modelPath, err := geminiModelPath(model)
	if err != nil {
		return apiEndpoints{}, err
	}
	modelsPath := prefix + "/models"
	generation := withAPIPath(u, modelsPath+"/"+modelPath+":generateContent")
	if model == "" {
		generation = withAPIPath(u, modelsPath) + "/{model}:generateContent"
	}
	return apiEndpoints{generation, withAPIPath(u, modelsPath)}, nil
}

func geminiModelPath(model string) (string, error) {
	if model == "" {
		return "", nil
	}
	parts := strings.Split(strings.TrimPrefix(model, "models/"), "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `\:%`) {
			return "", errors.New("Gemini 模型 ID 格式无效")
		}
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/"), nil
}

func withAPIPath(base *url.URL, escapedPath string) string {
	u := *base
	u.RawPath = escapedPath
	u.Path, _ = url.PathUnescape(escapedPath)
	return u.String()
}
