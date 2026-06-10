# HONEYDIPPER_CONTEXT.md - hd-driver-claude

## Overview

The hd-driver-claude is a Honeydipper AI model driver that enables the agent service to communicate with Anthropic's Claude API. It implements the `send_to_model` RPC handler that the agent service calls to exchange messages with Claude language models.

## Architecture

This driver follows the same pattern as hd-driver-openai and other Honeydipper AI drivers:

1. **RPC Handler**: Registers `send_to_model|interruptible` RPC handler
2. **Message Conversion**: Converts between Honeydipper's agent message format and Claude's API format
3. **HTTP Client**: Makes HTTP POST requests to Claude's messages API endpoint
4. **Response Processing**: Parses Claude's response and converts it back to Honeydipper's message format

## Key Components

### Engine Configuration

Each engine in the driver configuration represents a Claude model endpoint:

```yaml
drivers:
  claude:
    data:
      engines:
        claude3_opus:
          model: claude-3-opus-20240229
          api_key: ${ANTHROPIC_API_KEY}
          base_url: https://api.anthropic.com/v1/messages  # optional
          anthropic_version: "2023-06-01"  # optional
```

### Message Format Conversion

The driver handles conversion between:
- **Honeydipper format**: Uses `agentpkg.Message` with roles (system, user, agent, tool, tool_result)
- **Claude format**: Uses `claudeMessage` with roles (user, assistant) and content blocks

### Tool Calling

The driver supports Claude's tool_use functionality:
1. Tool definitions are converted from Honeydipper format to Claude's input_schema format
2. Tool calls in Claude's response are converted back to Honeydipper's ToolCall format
3. Tool results are sent back as tool_result content blocks

## API Integration

### Claude API Details

- **Endpoint**: `https://api.anthropic.com/v1/messages`
- **Authentication**: `x-api-key` header with Anthropic API key
- **API Version**: `anthropic-version` header (default: "2023-06-01")
- **Content Types**: Supports text and tool_use content blocks

### Key Differences from OpenAI

1. **System Messages**: Claude uses a top-level `system` field, not system messages in the conversation
2. **Content Blocks**: Claude uses structured content blocks (text, tool_use, tool_result)
3. **Tool Format**: Different JSON schema format for tool definitions
4. **Stop Reasons**: Uses `stop_reason` (end_turn, max_tokens, stop_sequence, tool_use)

## Configuration Options

| Field | Required | Description |
|-------|----------|-------------|
| `model` | Yes | Claude model name (e.g., claude-3-opus-20240229) |
| `api_key` | Yes | Anthropic API key |
| `base_url` | No | Override API endpoint URL |
| `anthropic_version` | No | API version (default: 2023-06-01) |

## RPC Interface

### send_to_model|interruptible

**Input:**
- Labels: `agent_session_id`
- Payload: `engine`, `history`, `tools`, `model_data`

**Output:**
- Channel: `agentbus`
- Subject: `receive`
- Payload: `message` (agentpkg.Message)

## Error Handling

The driver uses Honeydipper's `SafeExitOnError` pattern:
- Panics are recovered and sent back as error messages via agentbus
- HTTP errors from Claude API are captured and reported
- Invalid configurations are caught early with descriptive error messages

## Limitations

1. **Streaming**: Currently not implemented (falls back to non-streaming)
2. **System Message Concatenation**: Multiple system messages are concatenated with newlines
3. **Tool Result IDs**: Generated UUIDs may not match the original tool call IDs (limitation in current implementation)

## Testing

Run tests with:
```bash
cd cmd/hd-driver-claude
go test -v ./...
```

## Building

Build the driver with:
```bash
cd cmd/hd-driver-claude
go build -o hd-driver-claude
```
