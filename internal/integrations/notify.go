package integrations

import (
	"context"
	"encoding/json"
	"net/http"
)

func (client *Client) Notify(ctx context.Context, connection Connection, text string) Outcome {
	if err := ValidateConnection(connection); err != nil {
		return Outcome{State: "failed", Reason: err.Error()}
	}
	var endpoint string
	var payload interface{}
	switch connection.Kind {
	case "telegram":
		base, err := client.endpoint("telegram")
		if err != nil {
			return Outcome{State: "failed", Reason: err.Error()}
		}
		endpoint = base + "/bot" + connection.Secrets["bot_token"] + "/sendMessage"
		payload = struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}{ChatID: connection.Secrets["chat_id"], Text: text}
	case "wecom":
		endpoint = connection.Secrets["webhook_url"]
		payload = struct {
			MessageType string `json:"msgtype"`
			Text        struct {
				Content string `json:"content"`
			} `json:"text"`
		}{MessageType: "text", Text: struct {
			Content string `json:"content"`
		}{Content: text}}
	default:
		return Outcome{State: "failed", Reason: "此操作需要 Telegram 或企业微信连接"}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Outcome{State: "failed", Reason: "无法构造通知内容"}
	}
	body, status, failure := client.exchange(ctx, endpoint, "application/json", encoded)
	if failure != nil {
		outcome := outcomeFromError(failure)
		if outcome.State == "unconfirmed" {
			outcome.Reason = "通知结果未确认，未自动重试；手动重发可能产生重复消息"
		}
		return outcome
	}
	var response struct {
		OK        *bool `json:"ok"`
		ErrorCode *int  `json:"errcode"`
	}
	valid := validJSONObject(body) && json.Unmarshal(body, &response) == nil
	if valid && ((connection.Kind == "telegram" && response.OK != nil && !*response.OK) || (connection.Kind == "wecom" && response.ErrorCode != nil && *response.ErrorCode != 0)) {
		return Outcome{State: "failed", Reason: "通知接口明确拒绝发送，未自动重试"}
	}
	if status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout {
		return Outcome{State: "unconfirmed", Reason: "通知接口超时，未自动重试；手动重发可能产生重复消息"}
	}
	if status < 200 || status >= 300 {
		return Outcome{State: "failed", Reason: "通知接口返回失败状态，未自动重试"}
	}
	if !valid || (connection.Kind == "telegram" && response.OK == nil) || (connection.Kind == "wecom" && response.ErrorCode == nil) {
		return Outcome{State: "unconfirmed", Reason: "通知响应无法确认，未自动重试；手动重发可能产生重复消息"}
	}
	return Outcome{State: "success", Reason: "通知接口已确认发送成功"}
}
