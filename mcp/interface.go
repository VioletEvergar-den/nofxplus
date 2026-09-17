package mcp

import (
	"net/http"
	"time"
)

// AIClient public AI client interface (for external use)
type AIClient interface {
	SetAPIKey(apiKey string, customURL string, customModel string)
	SetTimeout(timeout time.Duration)
	CallWithMessages(systemPrompt, userPrompt string) (string, error)
	// CallWithMessagesWithImages 多模态调用：user 消息为 content 数组（文本+图片），失败返回错误由调用方决定回退
	CallWithMessagesWithImages(systemPrompt string, userParts []ContentPart) (string, error)
	CallWithRequest(req *Request) (string, error) // Builder pattern API (supports advanced features)
	CallWithRawMessages(messages []map[string]any) (string, error) // Raw messages array API (supports multimodal content)
}

// clientHooks internal hook interface (for subclass to override specific steps)
// These methods are only used inside the package to implement dynamic dispatch
type clientHooks interface {
	// Hook methods that can be overridden by subclass

	call(systemPrompt, userPrompt string) (string, error)

	// callWithParts 多模态调用：user 消息为 content 数组（文本+图片）
	callWithParts(systemPrompt string, userParts []ContentPart) (string, error)

	buildMCPRequestBody(systemPrompt, userPrompt string) map[string]any

	// buildMCPRequestBodyWithParts 多模态请求体
	buildMCPRequestBodyWithParts(systemPrompt string, userParts []ContentPart) map[string]any
	buildUrl() string
	buildRequest(url string, jsonData []byte) (*http.Request, error)
	setAuthHeader(reqHeaders http.Header)
	marshalRequestBody(requestBody map[string]any) ([]byte, error)
	parseMCPResponse(body []byte) (string, error)
	isRetryableError(err error) bool
}
