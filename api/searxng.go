package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// getSearxngURL 读取 SearXNG 实例地址（环境变量 SEARXNG_URL，为空表示禁用联网搜索）
func getSearxngURL() string {
	return strings.TrimRight(os.Getenv("SEARXNG_URL"), "/")
}

// SearxngResult SearXNG 单条搜索结果
type SearxngResult struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Content       string `json:"content"`
	PublishedDate string `json:"publishedDate"`
}

// SearxngClient SearXNG 搜索客户端（用于 AI 专家团会诊时的市场情报收集）
type SearxngClient struct {
	baseURL string
	client  *http.Client
}

// NewSearxngClient 创建 SearXNG 客户端（3 秒超时，失败自动降级）
func NewSearxngClient() *SearxngClient {
	return &SearxngClient{
		baseURL: getSearxngURL(),
		client:  &http.Client{Timeout: 3 * time.Second},
	}
}

// Enabled 是否已配置 SearXNG
func (c *SearxngClient) Enabled() bool {
	return c.baseURL != ""
}

// Search 执行搜索，返回裁剪后的结果列表（失败返回错误，由调用方降级处理）
func (c *SearxngClient) Search(query, lang string, maxResults int) ([]SearxngResult, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("searxng not configured")
	}
	if maxResults <= 0 || maxResults > 10 {
		maxResults = 6
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "news,general")
	params.Set("safesearch", "1")
	if lang == "zh" {
		params.Set("language", "zh-CN")
	} else {
		params.Set("language", "en")
	}

	req, err := http.NewRequest("GET", c.baseURL+"/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "nofxplus-council/1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng returned status %d", resp.StatusCode)
	}

	var parsed struct {
		Results []SearxngResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	results := parsed.Results
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	// 裁剪摘要长度，控制注入 token
	for i := range results {
		results[i].Content = truncateRunes(results[i].Content, 200)
		results[i].Title = truncateRunes(results[i].Title, 80)
	}
	return results, nil
}

// BuildBrief 将搜索结果组装为注入 prompt 的「市场情报简报」文本
func BuildBrief(results []SearxngResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, r := range results {
		date := r.PublishedDate
		if len(date) > 10 {
			date = date[:10]
		}
		fmt.Fprintf(&sb, "%d. [%s] %s\n", i+1, date, r.Title)
		if r.Content != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Content)
		}
		fmt.Fprintf(&sb, "   来源: %s\n", r.URL)
	}
	return sb.String()
}

// truncateRunes 按字符数截断字符串（避免截断多字节字符产生乱码）
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}
