package engine

import "strings"

func generationPayload(protocol, model, policy, input string) any {
	switch protocol {
	case protocolResponses:
		return map[string]any{
			"model": model, "instructions": policy, "input": input,
			"max_output_tokens": 2048, "store": false,
		}
	case protocolAnthropic:
		return map[string]any{
			"model": model, "system": policy, "messages": []chatMessage{{"user", input}},
			"max_tokens": 512,
		}
	case protocolGemini:
		return map[string]any{
			"systemInstruction": geminiContent{Parts: []geminiPart{{Text: policy}}},
			"contents":          []geminiContent{{Role: "user", Parts: []geminiPart{{Text: input}}}},
			"generationConfig":  map[string]any{"responseMimeType": "application/json", "maxOutputTokens": 2048},
		}
	default:
		return chatRequest{model, []chatMessage{{"system", policy}, {"user", input}}, 512}
	}
}

type apiReply interface {
	result() modelResult
}

func emptyAPIReply(protocol string) apiReply {
	switch protocol {
	case protocolResponses:
		return &responsesReply{}
	case protocolAnthropic:
		return &anthropicReply{}
	case protocolGemini:
		return &geminiReply{}
	default:
		return &chatResponse{}
	}
}

type cachedDetails struct {
	Cached *int64 `json:"cached_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Tier  string `json:"service_tier"`
	Usage struct {
		Input   *int64        `json:"prompt_tokens"`
		Output  *int64        `json:"completion_tokens"`
		Details cachedDetails `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func (r *chatResponse) result() modelResult {
	var text string
	if len(r.Choices) > 0 {
		text = r.Choices[0].Message.Content
	}
	return apiDecision(text, usage{r.Usage.Input, r.Usage.Output, r.Usage.Details.Cached}, r.Tier)
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesReply struct {
	Status string `json:"status"`
	Tier   string `json:"service_tier"`
	Output []struct {
		Type    string      `json:"type"`
		Role    string      `json:"role"`
		Content []textBlock `json:"content"`
	} `json:"output"`
	Usage struct {
		Input   *int64        `json:"input_tokens"`
		Output  *int64        `json:"output_tokens"`
		Details cachedDetails `json:"input_tokens_details"`
	} `json:"usage"`
}

func (r *responsesReply) result() modelResult {
	var text strings.Builder
	if r.Status == "" || r.Status == "completed" {
		for _, item := range r.Output {
			if item.Type == "message" && (item.Role == "" || item.Role == "assistant") {
				appendTextBlocks(&text, item.Content, "output_text")
			}
		}
	}
	return apiDecision(text.String(), usage{r.Usage.Input, r.Usage.Output, r.Usage.Details.Cached}, r.Tier)
}

type anthropicReply struct {
	Content []textBlock `json:"content"`
	Usage   struct {
		Input        *int64 `json:"input_tokens"`
		Output       *int64 `json:"output_tokens"`
		CacheCreated *int64 `json:"cache_creation_input_tokens"`
		CacheRead    *int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func (r *anthropicReply) result() modelResult {
	var text strings.Builder
	appendTextBlocks(&text, r.Content, "text")
	u := r.Usage
	input := sumReportedTokens(u.Input, u.CacheCreated, u.CacheRead)
	return apiDecision(text.String(), usage{input, u.Output, u.CacheRead}, "")
}

func appendTextBlocks(text *strings.Builder, blocks []textBlock, kind string) {
	for _, block := range blocks {
		if block.Type == kind {
			text.WriteString(block.Text)
		}
	}
}

type geminiPart struct {
	Text    string `json:"text"`
	Thought bool   `json:"thought,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiReply struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Usage struct {
		Input   *int64 `json:"promptTokenCount"`
		Output  *int64 `json:"candidatesTokenCount"`
		Cached  *int64 `json:"cachedContentTokenCount"`
		Thought *int64 `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

func (r *geminiReply) result() modelResult {
	var text strings.Builder
	if len(r.Candidates) > 0 {
		for _, part := range r.Candidates[0].Content.Parts {
			if !part.Thought {
				text.WriteString(part.Text)
			}
		}
	}
	u := r.Usage
	return apiDecision(text.String(), usage{u.Input, sumReportedTokens(u.Output, u.Thought), u.Cached}, "")
}

func sumReportedTokens(base *int64, extra ...*int64) *int64 {
	if cleanTokens(base) == nil {
		return nil
	}
	total := *base
	for _, count := range extra {
		if count == nil {
			continue
		}
		if cleanTokens(count) == nil {
			return nil
		}
		total += *count
	}
	return cleanTokens(&total)
}
