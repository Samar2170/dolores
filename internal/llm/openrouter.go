package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

type Client struct {
	apiKey    string
	models    []string
	maxTokens int
	hc        *http.Client
}

// Option configures optional Client behaviour.
type Option func(*Client)

func NewClient(apiKey string, models []string, opts ...Option) *Client {
	if len(models) == 0 {
		models = []string{"z-ai/glm-5.3-flash"}
	}
	c := &Client{
		apiKey: apiKey,
		models: models,
		hc:     &http.Client{Timeout: 3 * time.Minute},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// CompleteJSON sends a strict-JSON request to the configured models in order,
// falling back on transport or model errors. The reply is reduced to the
// outermost JSON value it contains (code fences are tolerated).
func (c *Client) CompleteJSON(ctx context.Context, system, user string) (json.RawMessage, error) {
	var lastErr error
	for _, model := range c.models {
		raw, err := c.chat(ctx, model, system, user)
		if err != nil {
			lastErr = fmt.Errorf("model %s: %w", model, err)
			continue
		}
		extracted := extractJSON(raw)
		if extracted == nil {
			lastErr = fmt.Errorf("model %s: no JSON object/array in response", model)
			continue
		}
		return json.RawMessage(extracted), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable LLM response")
	}
	return nil, lastErr
}

// CompleteText returns the raw text answer of the first responding model.
func (c *Client) CompleteText(ctx context.Context, system, user string) (string, error) {
	var lastErr error
	for _, model := range c.models {
		raw, err := c.chat(ctx, model, system, user)
		if err != nil {
			lastErr = fmt.Errorf("model %s: %w", model, err)
			continue
		}
		return raw, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable LLM response")
	}
	return "", lastErr
}

func (c *Client) chat(ctx context.Context, model, system, user string) (string, error) {
	payload, err := json.Marshal(chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens: c.maxTokens,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, defaultBaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("%s %s: %w", resp.Status, "bad body", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("%s %s", resp.Status, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s %s", resp.Status, "empty choices")
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("%s %s", resp.Status, "empty content")
	}
	return content, nil
}

// extractJSON finds the first balanced JSON object/array starting at '{' or '['.
func extractJSON(s string) []byte {
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return nil
	}
	open := s[start]
	close := byte('}')
	if open == '[' {
		close = ']'
	}

	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case esc:
			esc = false
		case ch == '\\' && inStr:
			esc = true
		case ch == '"':
			inStr = !inStr
		case inStr:
		case ch == open:
			depth++
		case ch == close:
			depth--
			if depth == 0 {
				return []byte(s[start : i+1])
			}
		}
	}
	return nil
}
