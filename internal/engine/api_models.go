package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type modelsRequest struct {
	APIProtocol string  `json:"apiProtocol"`
	APIBaseURL  string  `json:"apiBaseUrl"`
	APIKey      *string `json:"apiKey"`
}

type modelItem struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type modelCatalog struct {
	Items []modelItem `json:"items"`
}

func (e *Engine) listModels(parent context.Context, p modelsRequest) (modelCatalog, error) {
	saved, secret, err := loadSettings(parent, e.db)
	if err != nil {
		return modelCatalog{}, err
	}
	endpoints, err := resolveEndpoints(p.APIProtocol, p.APIBaseURL, "", saved.useLegacy(p.APIProtocol, p.APIBaseURL))
	if err != nil {
		return modelCatalog{}, err
	}
	secret, err = draftAPIKey(saved, p.APIBaseURL, secret, p.APIKey)
	if err != nil {
		return modelCatalog{}, err
	}
	key, err := credential(secret, true)
	if err != nil {
		return modelCatalog{}, errors.New("无法解密当前用户的 API 密钥，请重新保存")
	}
	defer clear(key)
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	return fetchModels(ctx, endpoints.ModelsURL, p.APIProtocol, key)
}

type modelPage struct {
	Data []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
	Models  []struct {
		Name         string   `json:"name"`
		DisplayName  string   `json:"displayName"`
		Capabilities []string `json:"supportedGenerationMethods"`
	} `json:"models"`
	NextPageToken string `json:"nextPageToken"`
}

func fetchModels(ctx context.Context, endpoint, protocol string, key []byte) (modelCatalog, error) {
	result := modelCatalog{Items: []modelItem{}}
	ids, cursors := map[string]bool{}, map[string]bool{}
	cursorName, cursor := "after_id", ""
	if protocol == protocolGemini {
		cursorName = "pageToken"
	}
	for range 64 {
		page, err := requestModelPage(ctx, endpoint, protocol, key, cursorName, cursor)
		if err != nil {
			return modelCatalog{}, err
		}
		items, next, more, err := page.items(protocol)
		if err != nil {
			return modelCatalog{}, err
		}
		for _, item := range items {
			if item.ID != "" && validString(item.ID, 256) && !ids[item.ID] {
				ids[item.ID] = true
				result.Items = append(result.Items, item)
			}
		}
		if !more {
			return result, nil
		}
		if next == "" || !validString(next, 4096) || cursors[next] || (len(key) > 0 && strings.Contains(next, string(key))) {
			return modelCatalog{}, errors.New("API 返回的模型列表分页信息无效")
		}
		cursors[next], cursor = true, next
	}
	return modelCatalog{}, errors.New("模型列表超过可读取的页数，请手动填写模型 ID")
}

func requestModelPage(ctx context.Context, endpoint, protocol string, key []byte, cursorName, cursor string) (modelPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return modelPage{}, errors.New("无法构造模型列表请求")
	}
	if cursor != "" {
		req.URL.RawQuery = url.Values{cursorName: {cursor}}.Encode()
	}
	req.Header.Set("Accept", "application/json")
	setAPIHeaders(req, protocol, key)
	res, err := apiClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return modelPage{}, errors.New("获取模型列表超时或已取消")
		}
		return modelPage{}, errors.New("无法获取模型列表，请检查地址与网络")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return modelPage{}, errors.New("获取模型列表失败，API 返回 HTTP " + strconv.Itoa(res.StatusCode))
	}
	const limit = 2 << 20
	var page modelPage
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return modelPage{}, errors.New("无法完整读取模型列表")
	}
	if len(raw) > limit || json.Unmarshal(raw, &page) != nil {
		return modelPage{}, errors.New("API 返回的模型列表无效")
	}
	return page, nil
}

func (p modelPage) items(protocol string) ([]modelItem, string, bool, error) {
	if protocol == protocolGemini {
		if p.Models == nil {
			return nil, "", false, errors.New("API 返回的 Gemini 模型列表无效")
		}
		items := make([]modelItem, 0, len(p.Models))
		for _, model := range p.Models {
			if canGenerateContent(model.Capabilities) {
				items = append(items, cleanModelItem(strings.TrimPrefix(model.Name, "models/"), model.DisplayName))
			}
		}
		return items, p.NextPageToken, p.NextPageToken != "", nil
	}
	if p.Data == nil {
		return nil, "", false, errors.New("API 返回的模型列表无效")
	}
	items := make([]modelItem, 0, len(p.Data))
	for _, model := range p.Data {
		name := model.Name
		if model.DisplayName != "" {
			name = model.DisplayName
		}
		items = append(items, cleanModelItem(model.ID, name))
	}
	if p.HasMore && p.LastID == "" && len(p.Data) > 0 {
		p.LastID = p.Data[len(p.Data)-1].ID
	}
	return items, p.LastID, p.HasMore, nil
}

func cleanModelItem(id, name string) modelItem {
	if !validString(name, 256) {
		name = ""
	}
	return modelItem{strings.TrimSpace(id), strings.TrimSpace(name)}
}

func canGenerateContent(methods []string) bool {
	if methods == nil {
		return true
	}
	for _, method := range methods {
		if method == "generateContent" {
			return true
		}
	}
	return false
}
