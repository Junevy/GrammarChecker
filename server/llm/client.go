// Package llm 封装对 OpenAI 兼容服务的调用（唯一外呼点，技术文档第 1/2 节）。
//
// 供应商：DeepSeek（deepseek-chat，2026-09-25 人员确认）；prompt 模板初稿见 prompts.go。
// 客户端职责：统一超时/重试、Bearer 鉴权、JSON 输出模式与 JSON 编解码；
// 业务接通在 server/api 中完成（四类业务共用 Chat）。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNoAPIKey 表示调用时未携带 API KEY（设置表未配置）。
var ErrNoAPIKey = errors.New("LLM API KEY 未配置")

// Config LLM 客户端配置。
type Config struct {
	BaseURL string        // OpenAI 兼容服务根地址（含 /v1），默认 DeepSeek
	Model   string        // 模型名称，默认 deepseek-chat
	Timeout time.Duration // 单次请求超时，文档约定 60s
	Retries int           // 网络 / 5xx 失败后的额外重试次数
}

// DefaultConfig 返回默认配置。
// 供应商：DeepSeek（2026-09-25 人员确认）——OpenAI 兼容接口，模型 deepseek-chat。
func DefaultConfig() Config {
	return Config{
		BaseURL: "https://api.deepseek.com/v1",
		Model:   "deepseek-chat",
		Timeout: 60 * time.Second,
		Retries: 1,
	}
}

// Client OpenAI 兼容 Chat Completions 客户端。
// 并发安全：内部仅含不可变配置与可重入的 http.Client，可多协程共用。
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient 构造客户端。
func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

// Message 一条对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// responseFormat JSON 输出模式（DeepSeek / OpenAI 兼容字段）。
type responseFormat struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// Chat 调用 /chat/completions 并返回首条回复文本。
//
// 固定启用 JSON 输出模式（response_format=json_object，要求模板内含 "JSON" 字样，
// 见 prompts.go）并使用低温（0.2）保证教学场景输出的稳定可解析。
// apiKey 由调用方从设置表读取后传入（仅随请求携带，不落第三方，
// 文档第 1 节隐私边界）；网络错误与 5xx 按 cfg.Retries 重试。
func (c *Client) Chat(ctx context.Context, apiKey string, messages []Message) (string, error) {
	if apiKey == "" {
		return "", ErrNoAPIKey
	}
	if c.cfg.BaseURL == "" || c.cfg.Model == "" {
		return "", errors.New("LLM BaseURL/Model 未配置")
	}
	payload, err := json.Marshal(chatRequest{
		Model:          c.cfg.Model,
		Messages:       messages,
		Temperature:    0.2,
		ResponseFormat: &responseFormat{Type: "json_object"},
	})
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			// 重试间隔 1s，同时尊重调用方的取消/超时
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Second):
			}
		}
		text, retryable, err := c.doOnce(ctx, payload, apiKey)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
	}
	return "", lastErr
}

// doOnce 执行单次请求；retryable 标记网络错误或 5xx（可重试）。
func (c *Client) doOnce(ctx context.Context, payload []byte, apiKey string) (text string, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", false, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("请求 LLM 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", true, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 500 {
			return "", true, fmt.Errorf("LLM 服务错误 %d: %s", resp.StatusCode, truncate(body, 300))
		}
		// 4xx（含鉴权失败）重试无意义，直接返回
		return "", false, fmt.Errorf("LLM 请求被拒绝 %d: %s", resp.StatusCode, truncate(body, 300))
	}

	var out chatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", false, fmt.Errorf("解析响应失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", false, errors.New("LLM 响应不含任何选项")
	}
	return out.Choices[0].Message.Content, false, nil
}

// truncate 截断字节串用于错误信息展示。
func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
