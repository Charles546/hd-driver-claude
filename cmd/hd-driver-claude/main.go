// Copyright 2026 Chun Huang (Charles).
//
// This Source Code Form is dual-licensed.
// By default, this file is licensed under the GNU Affero General Public License v3.0.
// If you have a separate written commercial agreement, you may use this file under those terms instead.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	agentpkg "github.com/honeydipper/honeydipper/v4/pkg/agent"
	"github.com/honeydipper/honeydipper/v4/pkg/dipper"
	"github.com/mitchellh/mapstructure"
)

var driver *dipper.Driver

type engineConfig struct {
	Model            string `mapstructure:"model"`
	APIKey           string `mapstructure:"api_key"`
	BaseURL          string `mapstructure:"base_url"`
	AnthropicVersion string `mapstructure:"anthropic_version"`
	ThinkingBudget   int    `mapstructure:"thinking_budget_tokens"`
}

type claudeMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type contentBlock struct {
	Type      string        `json:"type"`
	Thinking  string        `json:"thinking,omitempty"`
	Signature string        `json:"signature,omitempty"`
	Text      string        `json:"text,omitempty"`
	ToolUseID string        `json:"tool_use_id,omitempty"`
	Input     interface{}   `json:"input,omitempty"`
	ToolUse   *toolUseBlock `json:"tool_use,omitempty"`
}

type toolUseBlock struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Input interface{} `json:"input"`
}

type claudeTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type claudeRequest struct {
	Model       string          `json:"model"`
	Messages    []claudeMessage `json:"messages"`
	System      string          `json:"system,omitempty"`
	Tools       []claudeTool    `json:"tools,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Thinking    *thinkingConfig `json:"thinking,omitempty"`
}

type claudeResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      *claudeUsage   `json:"usage"`
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func main() {
	flag.Parse()
	driver = dipper.NewDriver(flag.Arg(0), "claude")
	driver.RPCHandlers["send_to_model|interruptible"] = sendToModel
	driver.Reload = func(m *dipper.Message) {}
	driver.Run()
}

func sendToModel(msg *dipper.Message) {
	msg = dipper.DeserializePayload(msg)

	sessionID := msg.Labels["agent_session_id"]
	defer dipper.SafeExitOnError("[claude] send_to_model", func(r interface{}) {
		if r != nil {
			m := agentbusMessage(sessionID, agentpkg.Message{
				Role:       agentpkg.RoleAgent,
				IsComplete: true,
			})
			m.Labels["status"] = "error"
			m.Labels["reason"] = fmt.Sprintf("%+v", r)
			driver.SendMessage(m)
		}
	})

	engineName := dipper.MustGetMapDataStr(msg.Payload, "engine")

	engRaw, ok := dipper.GetMapData(driver.Options, "data.engines."+engineName)
	if !ok || engRaw == nil {
		dipper.Logger.Panicf("[claude] unknown engine %q session=%s", engineName, sessionID)
		return
	}

	var cfg engineConfig
	dipper.Must(mapstructure.Decode(engRaw, &cfg))

	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com/v1/messages"
	} else if !strings.HasSuffix(cfg.BaseURL, "/v1/messages") {
		if strings.HasSuffix(cfg.BaseURL, "/") {
			cfg.BaseURL = cfg.BaseURL + "v1/messages"
		} else {
			cfg.BaseURL = cfg.BaseURL + "/v1/messages"
		}
	}
	if cfg.AnthropicVersion == "" {
		cfg.AnthropicVersion = "2023-06-01"
	}

	historyRaw, _ := dipper.GetMapData(msg.Payload, "history")
	var history []agentpkg.Message
	if historyRaw != nil {
		dipper.Must(mapstructure.Decode(historyRaw, &history))
	}

	toolsRaw, _ := dipper.GetMapData(msg.Payload, "tools")
	var tools map[string]agentpkg.Tool
	if toolsRaw != nil {
		dipper.Must(mapstructure.Decode(toolsRaw, &tools))
	}

	claudeReq := buildClaudeRequest(cfg, history, tools, msg.Payload)

	ctx, cancel := driver.GetContext(msg)
	defer cancel()

	shouldStream, _ := dipper.GetMapDataBool(msg.Payload, "should_stream")
	if shouldStream {
		sendToModelStreaming(ctx, cfg, claudeReq, sessionID)
		return
	}

	completion := dipper.Must(callClaudeAPI(ctx, cfg, claudeReq)).(*claudeResponse)
	handleIncomingMessage(completion, sessionID)
}

func buildClaudeRequest(cfg engineConfig, history []agentpkg.Message, tools map[string]agentpkg.Tool, payload interface{}) claudeRequest {
	req := claudeRequest{
		Model:    cfg.Model,
		Messages: []claudeMessage{},
	}

	var systemContent string
	messages := make([]claudeMessage, 0, len(history))

	for _, msg := range history {
		switch msg.Role {
		case agentpkg.RoleSystem:
			if systemContent != "" {
				systemContent += "\n" + msg.Content
			} else {
				systemContent = msg.Content
			}

		case agentpkg.RoleUser:
			messages = append(messages, claudeMessage{
				Role:    "user",
				Content: msg.Content,
			})

		case agentpkg.RoleAgent:
			if len(msg.ToolCalls) > 0 {
				content := []contentBlock{}
				if msg.Content != "" {
					content = append(content, contentBlock{
						Type: "text",
						Text: msg.Content,
					})
				}
				for _, tc := range msg.ToolCalls {
					params := tc.Params
					if params == nil {
						params = map[string]interface{}{}
					}
					content = append(content, contentBlock{
						Type: "tool_use",
						ToolUse: &toolUseBlock{
							ID:    fmt.Sprintf("toolu_%s", uuid.New().String()),
							Name:  tc.FuncName,
							Input: params,
						},
					})
				}
				messages = append(messages, claudeMessage{
					Role:    "assistant",
					Content: content,
				})
			} else {
				messages = append(messages, claudeMessage{
					Role:    "assistant",
					Content: msg.Content,
				})
			}

		case agentpkg.RoleTool:
			content := []contentBlock{}
			for _, tc := range msg.ToolCalls {
				params := tc.Params
				if params == nil {
					params = map[string]interface{}{}
				}
				content = append(content, contentBlock{
					Type: "tool_use",
					ToolUse: &toolUseBlock{
						ID:    fmt.Sprintf("toolu_%s", uuid.New().String()),
						Name:  tc.FuncName,
						Input: params,
					},
				})
			}
			messages = append(messages, claudeMessage{
				Role:    "user",
				Content: content,
			})

		case agentpkg.RoleToolResult:
			content := []contentBlock{}
			for i, result := range msg.ToolResult {
				resultBytes, _ := json.Marshal(result)
				toolUseID := fmt.Sprintf("toolu_result_%d", i)
				content = append(content, contentBlock{
					Type:      "tool_result",
					ToolUseID: toolUseID,
					Input:     string(resultBytes),
				})
			}
			messages = append(messages, claudeMessage{
				Role:    "user",
				Content: content,
			})
		}
	}

	req.Messages = messages
	if systemContent != "" {
		req.System = systemContent
	}

	if len(tools) > 0 {
		claudeTools := make([]claudeTool, 0, len(tools))
		for _, tool := range tools {
			parameters := map[string]interface{}{
				"type":       "object",
				"properties": tool.Params,
			}

			required := make([]string, 0)
			for paramName, paramDef := range tool.Params {
				if paramMap, ok := paramDef.(map[string]interface{}); ok {
					if req, ok := paramMap["required"].(bool); ok && req {
						required = append(required, paramName)
					}
				}
			}
			if len(required) > 0 {
				parameters["required"] = required
			}

			claudeTools = append(claudeTools, claudeTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: parameters,
			})
		}
		req.Tools = claudeTools
	}

	if modelDataRaw, _ := dipper.GetMapData(payload, "model_data"); modelDataRaw != nil {
		if modelData, ok := modelDataRaw.(map[string]interface{}); ok {
			if maxTokens, ok := modelData["max_tokens"].(int); ok {
				req.MaxTokens = maxTokens
			}
			if temp, ok := modelData["temperature"].(float64); ok {
				req.Temperature = temp
			}
		}
	}

	if req.MaxTokens == 0 {
		req.MaxTokens = 4096

		if cfg.ThinkingBudget > 0 {
			req.Thinking = &thinkingConfig{
				Type:         "enabled",
				BudgetTokens: cfg.ThinkingBudget,
			}
			req.MaxTokens = cfg.ThinkingBudget + 1024
		}
	}

	return req
}

func callClaudeAPI(ctx context.Context, cfg engineConfig, req claudeRequest) (*claudeResponse, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", cfg.BaseURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("x-api-key", cfg.APIKey)
	httpReq.Header.Set("anthropic-version", cfg.AnthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call Claude API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claude API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &claudeResp, nil
}

func agentbusMessage(sessionID string, msg agentpkg.Message) *dipper.Message {
	return &dipper.Message{
		Channel: "agentbus",
		Subject: "receive",
		Labels: map[string]string{
			"agent_session_id": sessionID,
			"sequence":         sessionID,
		},
		Payload: map[string]interface{}{"message": msg},
	}
}

func handleIncomingMessage(msg *claudeResponse, sessionID string) {
	agentMsg := agentpkg.Message{
		Role:       agentpkg.RoleAgent,
		IsComplete: msg.StopReason == "end_turn" || msg.StopReason == "stop_sequence",
		Content:    "",
		ToolCalls:  []agentpkg.ToolCall{},
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "thinking":
			agentMsg.IsThinking = true
			agentMsg.Thoughts += block.Thinking + "\n"

		case "text":
			agentMsg.Content += block.Text
		case "tool_use":
			if block.ToolUse != nil {
				params, _ := block.ToolUse.Input.(map[string]interface{})
				agentMsg.ToolCalls = append(agentMsg.ToolCalls, agentpkg.ToolCall{
					FuncName: block.ToolUse.Name,
					Params:   params,
				})
			}
		}
	}

	if len(agentMsg.ToolCalls) > 0 {
		agentMsg.IsComplete = false
	}

	if msg.Usage != nil {
		agentMsg.InputTokens = msg.Usage.InputTokens
		agentMsg.OutputTokens = msg.Usage.OutputTokens
	}

	driver.SendMessage(agentbusMessage(sessionID, agentMsg))
}

func sendToModelStreaming(ctx context.Context, cfg engineConfig, req claudeRequest, sessionID string) {
	req.Stream = false
	completion := dipper.Must(callClaudeAPI(ctx, cfg, req)).(*claudeResponse)
	handleIncomingMessage(completion, sessionID)
}
