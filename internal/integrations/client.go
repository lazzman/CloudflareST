package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Connection struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	AccountID string            `json:"account_id"`
	Secrets   map[string]string `json:"-"`
}

type Target struct {
	ConnectionID string     `json:"connection_id"`
	AccountID    string     `json:"account_id"`
	RecordID     string     `json:"record_id"`
	Domain       string     `json:"domain"`
	RR           string     `json:"rr"`
	Type         string     `json:"type"`
	Line         string     `json:"line"`
	TTL          int        `json:"ttl"`
	Value        string     `json:"value"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
}

type DNSObservation struct {
	Value string    `json:"value"`
	At    time.Time `json:"at"`
}

type Outcome struct {
	State    string          `json:"state"`
	Reason   string          `json:"reason"`
	Verified bool            `json:"verified"`
	NoWrite  bool            `json:"no_write,omitempty"`
	Before   *DNSObservation `json:"before,omitempty"`
	Readback *DNSObservation `json:"readback,omitempty"`
}

type Client struct {
	HTTPClient *http.Client      `json:"-"`
	Endpoints  map[string]string `json:"-"`
}

const (
	requestTimeout   = 15 * time.Second
	maxResponseSize  = 4 << 20
	alidnsEndpoint   = "https://alidns.aliyuncs.com/"
	stsEndpoint      = "https://sts.aliyuncs.com/"
	telegramEndpoint = "https://api.telegram.org"
)

var botTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

func New() *Client {
	return &Client{HTTPClient: &http.Client{Timeout: requestTimeout}}
}

func (connection Connection) String() string {
	encoded, _ := json.Marshal(connection)
	return Redact(string(encoded))
}

func (connection Connection) GoString() string {
	return connection.String()
}

func ValidateConnection(connection Connection) error {
	switch connection.Kind {
	case "alidns":
		if strings.TrimSpace(connection.Secrets["access_key_id"]) == "" || strings.TrimSpace(connection.Secrets["access_key_secret"]) == "" {
			return errors.New("阿里云连接缺少访问密钥标识或访问密钥")
		}
	case "telegram":
		if !botTokenPattern.MatchString(connection.Secrets["bot_token"]) {
			return errors.New("Telegram 机器人令牌格式无效")
		}
		if strings.TrimSpace(connection.Secrets["chat_id"]) == "" {
			return errors.New("Telegram 连接缺少会话标识")
		}
	case "wecom":
		if _, err := parseWebhook(connection.Secrets["webhook_url"]); err != nil {
			return err
		}
	default:
		return errors.New("不支持的服务连接类型")
	}
	return nil
}

func parseWebhook(rawURL string) (*url.URL, error) {
	endpoint, err := url.Parse(rawURL)
	if err != nil || endpoint.Scheme != "https" || !strings.EqualFold(endpoint.Hostname(), "qyapi.weixin.qq.com") || endpoint.User != nil || endpoint.Fragment != "" || (endpoint.Port() != "" && endpoint.Port() != "443") || endpoint.EscapedPath() != "/cgi-bin/webhook/send" {
		return nil, errors.New("企业微信地址必须是官方 HTTPS 群机器人发送地址")
	}
	query, err := url.ParseQuery(endpoint.RawQuery)
	if err != nil || len(query) != 1 || len(query["key"]) != 1 || strings.TrimSpace(query.Get("key")) == "" {
		return nil, errors.New("企业微信地址缺少有效的机器人密钥")
	}
	return endpoint, nil
}

func (client *Client) endpoint(kind string) (string, error) {
	var endpoint string
	switch kind {
	case "alidns":
		endpoint = alidnsEndpoint
	case "sts":
		endpoint = stsEndpoint
	case "telegram":
		endpoint = telegramEndpoint
	default:
		return "", errors.New("不支持的服务请求地址类型")
	}
	if client != nil && client.Endpoints[kind] != "" {
		endpoint = client.Endpoints[kind]
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("服务请求地址无效")
	}
	return strings.TrimRight(endpoint, "/"), nil
}

type requestError struct {
	reason    string
	uncertain bool
}

func (failure *requestError) Error() string {
	return failure.reason
}

func (client *Client) exchange(ctx context.Context, endpoint, contentType string, payload []byte) ([]byte, int, *requestError) {
	if ctx.Err() != nil {
		return nil, 0, &requestError{reason: "请求已取消，未发送"}
	}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, &requestError{reason: "无法构造服务请求"}
	}
	request.GetBody = nil
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	httpClient := http.Client{Timeout: requestTimeout}
	if client != nil && client.HTTPClient != nil {
		httpClient = *client.HTTPClient
	}
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := httpClient.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, 0, &requestError{reason: "服务请求超时或连接中断，结果未确认", uncertain: true}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return nil, response.StatusCode, &requestError{reason: "服务响应读取不完整，结果未确认", uncertain: true}
	}
	return body, response.StatusCode, nil
}

func outcomeFromError(failure *requestError) Outcome {
	state := "failed"
	if failure.uncertain {
		state = "unconfirmed"
	}
	return Outcome{State: state, Reason: failure.reason}
}

func validJSONObject(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 1 && trimmed[0] == '{' && json.Valid(trimmed)
}
