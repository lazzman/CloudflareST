package integrations

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	structured, _ := json.Marshal(map[string]string{
		"access_key_id": "test-secret", "access_key_secret": "test-secret with whitespace",
		"bot_token": "123456:" + "private-token", "chat_id": "private-response",
		"webhook_url":   "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook",
		"Authorization": "Bearer private-response", "Signature": "private-signature",
	})
	cases := []string{
		string(structured),
		"access_key_id=test-secret&access_key_secret=test-secret&Signature=private-signature",
		"accessKeySecret='test-secret with space'",
		"access_key_secret=\"test-secret with space\"",
		"POST https://api.telegram.org/bot123456:private-token/sendMessage",
		"POST https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook",
		"GET https://provider.example/?Signature=private-signature&AccessKeyId=test-secret",
		"Authorization: Bearer private-response",
		"bot_token=123456:private-token",
		url.QueryEscape("access_key_secret=test-secret&Signature=private-signature"),
		url.QueryEscape(url.QueryEscape("https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook")),
	}
	for _, input := range cases {
		output := Redact(input)
		assertSafe(t, output)
		if !strings.Contains(output, "已脱敏") {
			t.Fatal("脱敏后应保留可读提示")
		}
	}
	if !json.Valid([]byte(Redact(string(structured)))) {
		t.Fatal("脱敏后有效 JSON 应保持可解析")
	}
	if Redact("DNS 更新失败，记录 record-1") != "DNS 更新失败，记录 record-1" {
		t.Fatal("不应破坏普通错误文本")
	}
}

func TestConnectionSerializationDoesNotExposeSecrets(t *testing.T) {
	connection := dnsConnection()
	connection.Secrets["bot_token"] = "123456:" + "private-token"
	connection.Secrets["webhook_url"] = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=private-webhook"
	encoded, err := json.Marshal(connection)
	if err != nil {
		t.Fatal("连接序列化失败")
	}
	for _, output := range []string{string(encoded), fmt.Sprint(connection), fmt.Sprintf("%+v", connection), fmt.Sprintf("%#v", connection)} {
		assertSafe(t, output)
		if strings.Contains(output, "Secrets") || strings.Contains(output, "access_key_secret") {
			t.Fatal("连接输出不能包含秘密字段")
		}
	}
}
