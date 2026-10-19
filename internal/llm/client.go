package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Options configures the DeepSeek adapter.
type Options struct {
	BaseURL     string
	APIKey      string
	Model       string
	Timeout     time.Duration
	MaxRetries  int
	Temperature float64
	HTTPClient  *http.Client
	RetryDelay  time.Duration
}

type DeepSeekClient struct {
	baseURL     string
	apiKey      string
	model       string
	maxRetries  int
	temperature float64
	http        *http.Client
	retryDelay  time.Duration
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 500 {
		body = body[:500] + "..."
	}
	if body == "" {
		return fmt.Sprintf("DeepSeek API 返回 HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("DeepSeek API 返回 HTTP %d: %s", e.StatusCode, body)
}

func NewDeepSeek(opts Options) (*DeepSeekClient, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, errors.New("DeepSeek base URL 不能为空")
	}
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, errors.New("DeepSeek API key 不能为空")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, errors.New("DeepSeek model 不能为空")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	delay := opts.RetryDelay
	if delay <= 0 {
		delay = 400 * time.Millisecond
	}
	return &DeepSeekClient{
		baseURL:     strings.TrimRight(opts.BaseURL, "/"),
		apiKey:      opts.APIKey,
		model:       opts.Model,
		maxRetries:  max(0, opts.MaxRetries),
		temperature: opts.Temperature,
		http:        httpClient,
		retryDelay:  delay,
	}, nil
}

func (c *DeepSeekClient) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	temperature := req.Temperature
	if temperature == 0 {
		temperature = c.temperature
	}
	messages := make([]Message, len(req.Messages))
	for i, m := range req.Messages {
		m.ReasoningContent = ""
		messages[i] = m
	}
	payload := chatPayload{
		Model:       model,
		Messages:    messages,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
		Temperature: temperature,
		MaxTokens:   req.MaxTokens,
	}
	if len(req.Tools) == 0 {
		payload.ToolChoice = ""
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("encode DeepSeek request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		resp, err := c.doOnce(ctx, body)
		if err == nil {
			return resp, nil
		}
		if !isRetryable(err) || attempt >= c.maxRetries {
			return ChatResponse{}, err
		}
		if err := sleepContext(ctx, c.retryDelay*time.Duration(1<<attempt)); err != nil {
			return ChatResponse{}, err
		}
	}
}

func (c *DeepSeekClient) doOnce(ctx context.Context, body []byte) (ChatResponse, error) {
	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("build DeepSeek request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "goblin/0.1")

	resp, err := c.http.Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("调用 DeepSeek 失败: %w", err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return ChatResponse{}, fmt.Errorf("读取 DeepSeek 响应失败: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResponse{}, &APIError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	var wire chatResponsePayload
	if err := json.Unmarshal(data, &wire); err != nil {
		return ChatResponse{}, fmt.Errorf("解析 DeepSeek 响应失败: %w", err)
	}
	if len(wire.Choices) == 0 {
		return ChatResponse{}, errors.New("DeepSeek 响应不包含 choices")
	}
	choice := wire.Choices[0]
	requestID := resp.Header.Get("x-request-id")
	if requestID == "" {
		requestID = wire.ID
	}
	return ChatResponse{
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
		ToolCalls:        choice.Message.ToolCalls,
		FinishReason:     choice.FinishReason,
		Usage:            wire.Usage,
		RequestID:        requestID,
	}, nil
}

func isRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500 || apiErr.StatusCode == http.StatusRequestTimeout
	}
	// Network errors are retryable; malformed payloads are not.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type chatPayload struct {
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
}

type chatResponsePayload struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}
