package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func notificationConnection(kind string) Connection {
	connection := Connection{ID: "notification-1", Kind: kind}
	if kind == "telegram" {
		connection.Secrets = map[string]string{"bot_token": "123456:" + "private-token", "chat_id": "-100123"}
	} else {
		connection.Secrets = map[string]string{"webhook_url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook"}
	}
	return connection
}

func routeWecom(t *testing.T, client *Client) {
	t.Helper()
	localURL, err := url.Parse(client.Endpoints["wecom"])
	if err != nil {
		t.Fatal("模拟地址无效")
	}
	transport := client.HTTPClient.Transport
	client.HTTPClient.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "qyapi.weixin.qq.com" || request.URL.Path != "/cgi-bin/webhook/send" {
			t.Error("企业微信请求必须先核验真实官方地址")
			return nil, errors.New("企业微信地址不正确")
		}
		request = request.Clone(request.Context())
		request.URL.Scheme = localURL.Scheme
		request.URL.Host = localURL.Host
		return transport.RoundTrip(request)
	})
}

func TestNotifyResponses(t *testing.T) {
	for _, kind := range []string{"telegram", "wecom"} {
		t.Run(kind, func(t *testing.T) {
			cases := []struct {
				name   string
				status int
				body   string
				state  string
			}{
				{"成功", 200, "success", "success"},
				{"明确业务失败", 200, "failure", "failed"},
				{"认证拒绝", 403, "failure", "failed"},
				{"限流", 429, "failure", "failed"},
				{"HTTP失败", 500, "private-response", "failed"},
				{"网关超时", 504, "private-response", "unconfirmed"},
				{"缺失确认", 200, "{}", "unconfirmed"},
				{"格式错误", 200, "private-response", "unconfirmed"},
			}
			for _, testCase := range cases {
				t.Run(testCase.name, func(t *testing.T) {
					var calls int32
					client := mockClient(t, kind, func(response http.ResponseWriter, request *http.Request) {
						atomic.AddInt32(&calls, 1)
						if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
							t.Error("通知必须使用 JSON POST")
						}
						var payload map[string]interface{}
						if json.NewDecoder(request.Body).Decode(&payload) != nil {
							t.Error("通知请求体不是 JSON")
						}
						if kind == "telegram" {
							if request.URL.Path != "/bot123456:private-token/sendMessage" || payload["chat_id"] != "-100123" || payload["text"] != "任务结束 <原文>" || payload["parse_mode"] != nil {
								t.Error("Telegram 目标或纯文本请求体错误")
							}
						} else {
							text, ok := payload["text"].(map[string]interface{})
							if request.URL.Query().Get("key") != "private-webhook" || payload["msgtype"] != "text" || !ok || text["content"] != "任务结束 <原文>" {
								t.Error("企业微信目标或纯文本请求体错误")
							}
						}
						response.WriteHeader(testCase.status)
						switch testCase.body {
						case "success":
							if kind == "telegram" {
								io.WriteString(response, "{\"ok\":true,\"result\":{\"message_id\":1}}")
							} else {
								io.WriteString(response, "{\"errcode\":0,\"errmsg\":\"ok\"}")
							}
						case "failure":
							if kind == "telegram" {
								writeJSON(response, map[string]interface{}{"ok": false, "description": "private-token private-webhook"})
							} else {
								writeJSON(response, map[string]interface{}{"errcode": 40058, "errmsg": "private-webhook private-token"})
							}
						default:
							io.WriteString(response, testCase.body)
						}
					})
					if kind == "wecom" {
						routeWecom(t, client)
					}
					outcome := client.Notify(context.Background(), notificationConnection(kind), "任务结束 <原文>")
					if outcome.State != testCase.state || outcome.Verified || atomic.LoadInt32(&calls) != 1 {
						t.Fatalf("通知结果错误或发生重试：%+v，次数=%d", outcome, calls)
					}
					assertSafe(t, outcome.Reason)
				})
			}
		})
	}
}

func TestNotifyTimeoutAndDisconnectNeverRetry(t *testing.T) {
	for _, kind := range []string{"telegram", "wecom"} {
		t.Run(kind, func(t *testing.T) {
			var calls int32
			client := mockClient(t, kind, func(response http.ResponseWriter, request *http.Request) {
				atomic.AddInt32(&calls, 1)
				io.Copy(io.Discard, request.Body)
				<-request.Context().Done()
			})
			client.HTTPClient.Timeout = 40 * time.Millisecond
			if kind == "wecom" {
				routeWecom(t, client)
			}
			outcome := client.Notify(context.Background(), notificationConnection(kind), "结束摘要")
			if outcome.State != "unconfirmed" || atomic.LoadInt32(&calls) != 1 || !strings.Contains(outcome.Reason, "重复消息") {
				t.Fatalf("超时必须未确认且只发送一次：%+v，次数=%d", outcome, calls)
			}
			assertSafe(t, outcome.Reason)
		})
		t.Run(kind+"断连脱敏", func(t *testing.T) {
			var calls int
			client := New()
			client.HTTPClient.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				return nil, fmt.Errorf("private-response %s private-token private-webhook", request.URL)
			})
			outcome := client.Notify(context.Background(), notificationConnection(kind), "结束摘要")
			if outcome.State != "unconfirmed" || calls != 1 {
				t.Fatalf("断连必须未确认且不重试：%+v，次数=%d", outcome, calls)
			}
			assertSafe(t, outcome.Reason)
		})
	}
}

func TestNotifyRejectsRedirect(t *testing.T) {
	var calls int32
	client := mockClient(t, "telegram", func(response http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&calls, 1)
		response.Header().Set("Location", "/redirected")
		response.WriteHeader(http.StatusTemporaryRedirect)
	})
	outcome := client.Notify(context.Background(), notificationConnection("telegram"), "摘要")
	if outcome.State != "failed" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("不能跟随重定向导致凭据泄漏或重复发送：%+v", outcome)
	}
}

func TestValidateConnection(t *testing.T) {
	for _, connection := range []Connection{dnsConnection(), notificationConnection("telegram"), notificationConnection("wecom")} {
		if err := ValidateConnection(connection); err != nil {
			t.Fatalf("合法连接被拒绝：%v", err)
		}
		connection.Secrets = nil
		if err := ValidateConnection(connection); err == nil {
			t.Fatal("缺少凭据应拒绝")
		}
	}
	if ValidateConnection(Connection{Kind: "custom-provider"}) == nil {
		t.Fatal("不能接受未知服务商")
	}
	for _, webhook := range []string{
		"http://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook",
		"https://qyapi.weixin.qq.com.evil.example/cgi-bin/webhook/send?key=private-webhook",
		"https://qyapi.weixin.qq.com@evil.example/cgi-bin/webhook/send?key=private-webhook",
		"https://evil.example@qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook",
		"https://qyapi.weixin.qq.com:8443/cgi-bin/webhook/send?key=private-webhook",
		"https://127.0.0.1/cgi-bin/webhook/send?key=private-webhook",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook#fragment",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook&key=other",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=invalid%zz",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/upload_media?key=private-webhook",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=%zz",
	} {
		connection := Connection{Kind: "wecom", Secrets: map[string]string{"webhook_url": webhook}}
		if err := ValidateConnection(connection); err == nil {
			t.Fatal("不应接受非官方或无效 Webhook 地址")
		} else {
			assertSafe(t, err.Error())
		}
	}
}
