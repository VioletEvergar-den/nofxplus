package mcp

import "encoding/json"

// Message represents a conversation message
type Message struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"` // Message content
	// ContentParts 多模态内容数组（非空时整个 content 序列化为数组，Content 字段被忽略）
	ContentParts []ContentPart `json:"-"`
}

// MarshalJSON 兼容两种形态：纯文本消息输出 content 字符串；多模态消息输出 content 数组
func (m Message) MarshalJSON() ([]byte, error) {
	if len(m.ContentParts) > 0 {
		return json.Marshal(struct {
			Role    string        `json:"role"`
			Content []ContentPart `json:"content"`
		}{Role: m.Role, Content: m.ContentParts})
	}
	return json.Marshal(struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: m.Role, Content: m.Content})
}

// ContentPart OpenAI 兼容多模态消息内容片段
type ContentPart struct {
	Type     string    `json:"type"` // "text" 或 "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL 图片引用（支持 data:image/png;base64,... 形式的内联图片）
type ImageURL struct {
	URL string `json:"url"`
}

// NewTextPart 创建文本内容片段
func NewTextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

// NewImagePart 创建图片内容片段
func NewImagePart(dataURL string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: dataURL}}
}

// Tool represents a tool/function that AI can call
type Tool struct {
	Type     string      `json:"type"`     // Usually "function"
	Function FunctionDef `json:"function"` // Function definition
}

// FunctionDef function definition
type FunctionDef struct {
	Name        string         `json:"name"`                  // Function name
	Description string         `json:"description,omitempty"` // Function description
	Parameters  map[string]any `json:"parameters,omitempty"`  // Parameter schema (JSON Schema)
}

// Request AI API request (supports advanced features)
type Request struct {
	// Basic fields
	Model    string    `json:"model"`            // Model name
	Messages []Message `json:"messages"`         // Conversation message list
	Stream   bool      `json:"stream,omitempty"` // Whether to stream response

	// Optional parameters (for fine-grained control)
	Temperature      *float64 `json:"temperature,omitempty"`       // Temperature (0-2), controls randomness
	MaxTokens        *int     `json:"max_tokens,omitempty"`        // Maximum token count
	TopP             *float64 `json:"top_p,omitempty"`             // Nucleus sampling parameter (0-1)
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"` // Frequency penalty (-2 to 2)
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`  // Presence penalty (-2 to 2)
	Stop             []string `json:"stop,omitempty"`              // Stop sequences

	// Advanced features
	Tools      []Tool `json:"tools,omitempty"`       // Available tools list
	ToolChoice string `json:"tool_choice,omitempty"` // Tool choice strategy ("auto", "none", {"type": "function", "function": {"name": "xxx"}})
}

// NewMessage creates a message
func NewMessage(role, content string) Message {
	return Message{
		Role:    role,
		Content: content,
	}
}

// NewSystemMessage creates a system message
func NewSystemMessage(content string) Message {
	return Message{
		Role:    "system",
		Content: content,
	}
}

// NewUserMessage creates a user message
func NewUserMessage(content string) Message {
	return Message{
		Role:    "user",
		Content: content,
	}
}

// NewAssistantMessage creates an assistant message
func NewAssistantMessage(content string) Message {
	return Message{
		Role:    "assistant",
		Content: content,
	}
}
