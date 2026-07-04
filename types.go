package main

import (
	"encoding/json"
	"strings"
)

// ---- requests ----

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Text extracts plain text from either a JSON string or an array of content parts.
func (m ChatMessage) Text() string {
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

type CompletionRequest struct {
	Model  string          `json:"model"`
	Prompt json.RawMessage `json:"prompt"`
	Stream bool            `json:"stream"`
}

func (r CompletionRequest) PromptText() string {
	var s string
	if json.Unmarshal(r.Prompt, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(r.Prompt, &list) == nil {
		return strings.Join(list, "\n")
	}
	return ""
}

// ---- responses ----

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type FullMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type DeltaMessage struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type ChatChoice struct {
	Index        int           `json:"index"`
	Message      *FullMessage  `json:"message,omitempty"`
	Delta        *DeltaMessage `json:"delta,omitempty"`
	FinishReason *string       `json:"finish_reason"`
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`
}

type TextChoice struct {
	Index        int     `json:"index"`
	Text         string  `json:"text"`
	FinishReason *string `json:"finish_reason"`
}

type CompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []TextChoice `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type APIError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}
