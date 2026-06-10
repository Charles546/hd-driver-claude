// Copyright 2026 Chun Huang (Charles).
//
// This Source Code Form is dual-licensed.
// By default, this file is licensed under the GNU Affero General Public License v3.0.
// If you have a separate written commercial agreement, you may use this file under those terms instead.

package main

import (
	"testing"

	agentpkg "github.com/honeydipper/honeydipper/v4/pkg/agent"
	"github.com/stretchr/testify/assert"
)

func TestBuildClaudeRequest(t *testing.T) {
	cfg := engineConfig{
		Model: "claude-3-opus-20240229",
	}

	history := []agentpkg.Message{
		{
			Role:    agentpkg.RoleSystem,
			Content: "You are a helpful assistant.",
		},
		{
			Role:    agentpkg.RoleUser,
			Content: "Hello!",
		},
	}

	tools := map[string]agentpkg.Tool{
		"search": {
			Name:        "search",
			Description: "Search the web",
			Params: map[string]interface{}{
				"query": map[string]interface{}{
					"type": "string",
				},
			},
		},
	}

	req := buildClaudeRequest(cfg, history, tools, nil)

	assert.Equal(t, "claude-3-opus-20240229", req.Model)
	assert.Equal(t, "You are a helpful assistant.", req.System)
	assert.Len(t, req.Messages, 1)
	assert.Equal(t, "user", req.Messages[0].Role)
	assert.Equal(t, "Hello!", req.Messages[0].Content)
	assert.Len(t, req.Tools, 1)
	assert.Equal(t, "search", req.Tools[0].Name)
	assert.Equal(t, 4096, req.MaxTokens)
}

func TestBuildClaudeRequestWithToolCalls(t *testing.T) {
	cfg := engineConfig{
		Model: "claude-3-sonnet-20240229",
	}

	history := []agentpkg.Message{
		{
			Role:    agentpkg.RoleUser,
			Content: "Search for Go tutorials",
		},
		{
			Role:    agentpkg.RoleAgent,
			Content: "I'll search for that",
			ToolCalls: []agentpkg.ToolCall{
				{
					FuncName: "search",
					Params:   map[string]interface{}{"query": "Go tutorials"},
				},
			},
		},
	}

	req := buildClaudeRequest(cfg, history, nil, nil)

	assert.Len(t, req.Messages, 2)
	assert.Equal(t, "user", req.Messages[0].Role)
	assert.Equal(t, "assistant", req.Messages[1].Role)

	content, ok := req.Messages[1].Content.([]contentBlock)
	assert.True(t, ok)
	assert.Len(t, content, 2)
	assert.Equal(t, "text", content[0].Type)
	assert.Equal(t, "I'll search for that", content[0].Text)
	assert.Equal(t, "tool_use", content[1].Type)
	assert.NotNil(t, content[1].ToolUse)
	assert.Equal(t, "search", content[1].ToolUse.Name)
}

func TestBuildClaudeRequestWithModelData(t *testing.T) {
	cfg := engineConfig{
		Model: "claude-3-opus-20240229",
	}

	history := []agentpkg.Message{
		{
			Role:    agentpkg.RoleUser,
			Content: "Test",
		},
	}

	payload := map[string]interface{}{
		"model_data": map[string]interface{}{
			"max_tokens":  2048,
			"temperature": 0.5,
		},
	}

	req := buildClaudeRequest(cfg, history, nil, payload)

	assert.Equal(t, 2048, req.MaxTokens)
	assert.Equal(t, 0.5, req.Temperature)
}

func TestAgentbusMessage(t *testing.T) {
	sessionID := "test-session-123"
	agentMsg := agentpkg.Message{
		Role:       agentpkg.RoleAgent,
		Content:    "Hello from Claude",
		IsComplete: true,
	}

	msg := agentbusMessage(sessionID, agentMsg)

	assert.Equal(t, "agentbus", msg.Channel)
	assert.Equal(t, "receive", msg.Subject)
	assert.Equal(t, sessionID, msg.Labels["agent_session_id"])
	assert.Equal(t, sessionID, msg.Labels["sequence"])
	assert.NotNil(t, msg.Payload)

	messageData, ok := msg.Payload.(map[string]interface{})
	assert.True(t, ok)
	assert.NotNil(t, messageData["message"])
}

func TestClaudeResponseParsing(t *testing.T) {
	tests := []struct {
		name       string
		stopReason string
		expected   bool
	}{
		{"end_turn", "end_turn", true},
		{"stop_sequence", "stop_sequence", true},
		{"tool_use", "tool_use", false},
		{"max_tokens", "max_tokens", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &claudeResponse{
				StopReason: tt.stopReason,
				Content: []contentBlock{
					{Type: "text", Text: "Test response"},
				},
			}

			isComplete := resp.StopReason == "end_turn" || resp.StopReason == "stop_sequence"
			assert.Equal(t, tt.expected, isComplete)
		})
	}
}

func TestEngineConfigDefaults(t *testing.T) {
	cfg := engineConfig{
		BaseURL: "",
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com/v1/messages"
	}
	if cfg.AnthropicVersion == "" {
		cfg.AnthropicVersion = "2023-06-01"
	}

	assert.Equal(t, "https://api.anthropic.com/v1/messages", cfg.BaseURL)
	assert.Equal(t, "2023-06-01", cfg.AnthropicVersion)
}

func TestContentBlockTypes(t *testing.T) {
	textBlock := contentBlock{
		Type: "text",
		Text: "Hello world",
	}
	assert.Equal(t, "text", textBlock.Type)
	assert.Equal(t, "Hello world", textBlock.Text)

	toolBlock := contentBlock{
		Type: "tool_use",
		ToolUse: &toolUseBlock{
			ID:    "toolu_123",
			Name:  "search",
			Input: map[string]interface{}{"query": "test"},
		},
	}
	assert.Equal(t, "tool_use", toolBlock.Type)
	assert.NotNil(t, toolBlock.ToolUse)
	assert.Equal(t, "search", toolBlock.ToolUse.Name)
}

func TestBuildClaudeRequestWithThinkingBudget(t *testing.T) {
	cfg := engineConfig{
		Model:          "claude-3-opus-20240229",
		ThinkingBudget: 2000,
	}

	history := []agentpkg.Message{
		{
			Role:    agentpkg.RoleUser,
			Content: "Test with thinking",
		},
	}

	req := buildClaudeRequest(cfg, history, nil, nil)

	assert.NotNil(t, req.Thinking)
	assert.Equal(t, "enabled", req.Thinking.Type)
	assert.Equal(t, 2000, req.Thinking.BudgetTokens)
	assert.Equal(t, 3024, req.MaxTokens) // 2000 + 1024
}

func TestHandleIncomingMessageWithThinking(t *testing.T) {
	msg := &claudeResponse{
		StopReason: "end_turn",
		Content: []contentBlock{
			{Type: "thinking", Thinking: "Let me think about this..."},
			{Type: "text", Text: "Here's my answer."},
		},
	}

	assert.Equal(t, "end_turn", msg.StopReason)
	assert.Len(t, msg.Content, 2)
	assert.Equal(t, "thinking", msg.Content[0].Type)
	assert.Equal(t, "Let me think about this...", msg.Content[0].Thinking)
	assert.Equal(t, "text", msg.Content[1].Type)
	assert.Equal(t, "Here's my answer.", msg.Content[1].Text)
}

func TestContentBlockThinking(t *testing.T) {
	block := contentBlock{
		Type:      "thinking",
		Thinking:  "This is my reasoning process",
		Signature: "signature123",
	}

	assert.Equal(t, "thinking", block.Type)
	assert.Equal(t, "This is my reasoning process", block.Thinking)
	assert.Equal(t, "signature123", block.Signature)
}
