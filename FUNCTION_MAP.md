# FUNCTION_MAP.md - hd-driver-claude

This document describes the RPC functions provided by the hd-driver-claude driver.

## RPC Handlers

### send_to_model|interruptible

Sends a conversation to Claude's API and returns the model's response.

**Handler Type**: Interruptible RPC (can be cancelled by driver shutdown or context cancellation)

**Input Labels:**
| Label | Type | Required | Description |
|-------|------|----------|-------------|
| `agent_session_id` | string | Yes | Unique identifier for the agent session |

**Input Payload:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `engine` | string | Yes | Engine name from driver configuration |
| `history` | []agentpkg.Message | No | Conversation history |
| `tools` | map[string]agentpkg.Tool | No | Available tools for the model |
| `model_data` | map[string]interface{} | No | Additional model parameters (max_tokens, temperature, etc.) |
| `should_stream` | bool | No | Whether to use streaming (currently not implemented) |
| `agent_settings` | map[string]interface{} | No | Agent configuration settings |

**Output:**
Sent to `agentbus:receive` channel with labels:
- `agent_session_id`: Same as input
- `sequence`: Same as agent_session_id (for ordered processing)

Payload contains:
```yaml
message:
  Role: "agent" | "tool"
  Content: string  # Text response (if any)
  IsComplete: bool  # Whether this is the final message
  ToolCalls: []ToolCall  # Tool calls requested by the model
  InputTokens: int  # Tokens used in prompt
  OutputTokens: int  # Tokens generated in response
```

**Error Handling:**
On error, sends message with:
- Label `status`: "error"
- Label `reason`: Error description

## Configuration Functions

The driver doesn't expose additional functions but reads configuration from:

**Driver Options Path**: `data.engines.<engine_name>`

**Expected Configuration Structure:**
```yaml
drivers:
  claude:
    data:
      engines:
        <engine_name>:
          model: string
          api_key: string
          base_url: string  # optional
          anthropic_version: string  # optional
```

## Internal Functions

### buildClaudeRequest
Converts Honeydipper agent messages to Claude API request format.

### callClaudeAPI
Makes HTTP POST request to Claude's API endpoint.

### handleIncomingMessage
Processes Claude's response and converts to Honeydipper message format.

### agentbusMessage
Wraps agent message in dipper.Message for agentbus transport.

## Usage with Honeydipper Agent

Example agent configuration:
```yaml
agents:
  my-claude-agent:
    driver: claude
    engine: claude3_opus
    system_prompt: "You are a helpful assistant."
    model_data:
      max_tokens: 4096
      temperature: 0.7
```

The agent service will automatically call `send_to_model` RPC when processing conversations.
