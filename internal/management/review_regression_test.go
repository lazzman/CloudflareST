package management

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/masgzy/CloudflareST/internal/integrations"
)

func regressionRequest(test *testing.T, fixture *acceptanceFixture, method, path, revision string, body any) *httptest.ResponseRecorder {
	test.Helper()
	token := "FAKE_AUDIT_SESSION"
	session := sessionRecord{ID: fixture.service.digest([]byte(token)), CSRF: "FAKE_AUDIT_CSRF", PasswordVersion: fixture.service.passwordVersion, ExpiresAt: fixture.clock.Now().Add(time.Hour)}
	acceptancePut(test, fixture, "sessions", session.ID, session)
	payload, err := json.Marshal(body)
	if err != nil {
		test.Fatal(err)
	}
	request := httptest.NewRequest(method, "http://audit.invalid"+path, strings.NewReader(string(payload)))
	request.AddCookie(&http.Cookie{Name: "cfst_session", Value: token})
	request.Header.Set("X-CSRF-Token", session.CSRF)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", revision)
	response := httptest.NewRecorder()
	fixture.service.Handler().ServeHTTP(response, request)
	return response
}

func TestRegressionTelegramBotIdentityMustNotChangeInPlace(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	ciphertext, err := fixture.service.encrypt(map[string]string{"bot_token": "100001" + ":" + "FAKE_only_test_token", "chat_id": "-100999"})
	if err != nil {
		test.Fatal(err)
	}
	connection := Connection{ID: "audit-telegram", Kind: "telegram", Name: "验收虚构连接", Revision: 1, CredentialsSet: true, Ciphertext: ciphertext}
	acceptancePut(test, fixture, "connections", connection.ID, connection)
	task := acceptanceTask("验收机器人身份")
	task.Notifications.Telegram = Channel{Enabled: true, ConnectionID: connection.ID}
	saved, err := acceptanceSaveTask(fixture, task, "", 0)
	if err != nil {
		test.Fatal(err)
	}
	response := regressionRequest(test, fixture, http.MethodPatch, "/api/v1/connections/"+connection.ID, "1", map[string]any{
		"operation_id": "audit-change-bot-identity",
		"connection":   map[string]any{"kind": "telegram", "name": "验收虚构连接", "account_id": "", "secrets": map[string]string{"bot_token": "200002" + ":" + "FAKE_only_test_token", "chat_id": "-100999"}},
	})
	current := acceptanceGet[Connection](test, fixture, "connections", connection.ID)
	credential, err := fixture.service.credentials(current)
	if err != nil {
		test.Fatal(err)
	}
	unchangedTask := acceptanceGet[Task](test, fixture, "tasks", saved.ID)
	botID := strings.SplitN(credential.Secrets["bot_token"], ":", 2)[0]
	updates, notifications := fixture.provider.callCounts()
	test.Logf("HTTP=%d，机器人ID 100001→%s，任务仍引用原连接=%t，DNS/通知调用=%d/%d", response.Code, botID, unchangedTask.Notifications.Telegram.ConnectionID == connection.ID, updates, notifications)
	if response.Code != http.StatusConflict {
		test.Errorf("AC-76：机器人身份改变必须拒绝原地替换并要求新连接，实际HTTP=%d", response.Code)
	}
}

func TestRegressionCancelledNotificationFailureMustBeManuallyRetryable(test *testing.T) {
	provider := newAcceptanceProvider()
	provider.notify = func(context.Context, integrations.Connection, string) integrations.Outcome {
		return integrations.Outcome{State: "failed", Reason: "虚构失败，无外部请求"}
	}
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, provider)
	acceptanceConnection(test, fixture, "audit-cancel-channel", "telegram", "")
	task := acceptanceTask("验收取消摘要")
	task.ID = "audit-cancel-task"
	task.Notifications.Telegram = Channel{Enabled: true, ConnectionID: "audit-cancel-channel"}
	var cancelled Run
	acceptanceTx(test, fixture, func(tx *sql.Tx) error {
		run, err := enqueue(tx, task, "manual", "", fixture.service)
		if err != nil {
			return err
		}
		_, result, err := fixture.service.cancelRun(tx, run.ID)
		if err == nil {
			cancelled = result.(Run)
		}
		return err
	})
	acceptanceWake(fixture)
	first := acceptanceWaitDelivery(test, fixture, cancelled.ID, cancelled.Deliveries[0].ID, "failed")
	response := regressionRequest(test, fixture, http.MethodPost, "/api/v1/runs/"+cancelled.ID+"/notifications", "", map[string]any{
		"operation_id": "audit-retry-cancel-summary", "risk_acknowledged": true,
		"channels": []notificationItem{{Kind: "telegram", ConnectionID: "audit-cancel-channel", PriorAttemptID: first.ID}},
	})
	if response.Code != http.StatusAccepted {
		test.Fatalf("手动重发未受理：%d %s", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	var retry Delivery
	for time.Now().Before(deadline) {
		run := acceptanceGet[Run](test, fixture, "runs", cancelled.ID)
		retry = run.Deliveries[len(run.Deliveries)-1]
		if terminalDelivery(retry.State) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, notifications := provider.callCounts()
	test.Logf("首次取消摘要=failed；重发HTTP=%d；重发状态=%s，Cancellation=%t；累计模拟Notify=%d；原因=%s", response.Code, retry.State, retry.Cancellation, notifications, retry.Reason)
	if notifications != 2 || retry.State == "skipped" {
		test.Errorf("AC-73：确认风险后应调用通知适配器重发取消摘要；实际未调用且跳过")
	}
	if !retry.Cancellation || retry.PriorAttemptID != first.ID {
		test.Fatal("重发必须继承原取消摘要身份并关联原尝试")
	}
	for _, message := range provider.notificationMessages() {
		if !strings.Contains(message, "cancelled") {
			test.Fatal("取消摘要重发不得变成普通结束通知")
		}
	}
}

func TestRegressionTelegramCredentialRotationKeepsBotAndChat(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	oldToken := "100001:FAKE_old_test_token"
	newToken := "100001:FAKE_rotated_test_token"
	ciphertext, err := fixture.service.encrypt(map[string]string{"bot_token": oldToken, "chat_id": "-100999"})
	if err != nil {
		test.Fatal(err)
	}
	connection := Connection{ID: "rotation-telegram", Kind: "telegram", Name: "同机器人轮换", Revision: 1, CredentialsSet: true, Ciphertext: ciphertext}
	acceptancePut(test, fixture, "connections", connection.ID, connection)
	task := acceptanceTask("轮换保留引用")
	task.Notifications.Telegram = Channel{Enabled: true, ConnectionID: connection.ID}
	if _, err = acceptanceSaveTask(fixture, task, "", 0); err != nil {
		test.Fatal(err)
	}
	body := map[string]any{"operation_id": "rotation-test-operation", "connection": map[string]any{"kind": "telegram", "name": connection.Name, "account_id": "", "secrets": map[string]string{"bot_token": newToken, "chat_id": "-100999"}}}
	response := regressionRequest(test, fixture, http.MethodPatch, "/api/v1/connections/"+connection.ID, "1", body)
	if response.Code != http.StatusAccepted {
		test.Fatalf("同机器人轮换应成功: %d %s", response.Code, response.Body.String())
	}
	current := acceptanceGet[Connection](test, fixture, "connections", connection.ID)
	credentials, err := fixture.service.credentials(current)
	if err != nil {
		test.Fatal(err)
	}
	if current.Revision != 2 || credentials.Secrets["bot_token"] != newToken {
		test.Fatal("轮换未保存或身份变更")
	}
	retry := regressionRequest(test, fixture, http.MethodPatch, "/api/v1/connections/"+connection.ID, "1", body)
	if retry.Code != http.StatusAccepted {
		test.Fatal("原操作号应返回已保存收据")
	}
	if latest := acceptanceGet[Connection](test, fixture, "connections", connection.ID); latest.Revision != 2 {
		test.Fatal("重复操作不应再次写入")
	}
	if updates, notifications := fixture.provider.callCounts(); updates != 0 || notifications != 0 {
		test.Fatal("保存凭据不得触发DNS或通知")
	}
}

func TestRegressionCancelledRunOnlyRetriesOriginalCancellationSummary(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	fixture.service.cancel()
	fixture.service.wg.Wait()
	acceptanceConnection(test, fixture, "cancel-scope-connection", "telegram", "")
	run := Run{ID: "cancel-scope-run", Status: "cancelled", CancelRequested: true, Deliveries: []Delivery{
		{ID: "original-normal", Kind: "telegram", ConnectionID: "cancel-scope-connection", State: "failed"},
		{ID: "original-cancel", Kind: "telegram", ConnectionID: "cancel-scope-connection", State: "unconfirmed", Cancellation: true},
	}}
	acceptancePut(test, fixture, "runs", run.ID, run)
	for _, scenario := range []struct {
		prior string
		risk  bool
		want  int
	}{{"", true, 409}, {"original-normal", true, 409}, {"original-cancel", false, 400}, {"original-cancel", true, 0}} {
		fixture.service.mu.Lock()
		tx, err := fixture.service.db.Begin()
		if err != nil {
			fixture.service.mu.Unlock()
			test.Fatal(err)
		}
		_, _, err = fixture.service.manualNotifications(tx, run.ID, []notificationItem{{Kind: "telegram", ConnectionID: "cancel-scope-connection", PriorAttemptID: scenario.prior}}, scenario.risk)
		if scenario.want == 0 {
			if err != nil {
				test.Errorf("明确取消摘要应允许人工重发: %v", err)
			} else {
				updated, readErr := get[Run](tx, "runs", run.ID)
				if readErr != nil || !updated.Deliveries[len(updated.Deliveries)-1].Cancellation {
					test.Error("取消摘要属性未保留")
				}
			}
		} else {
			acceptanceAPIError(test, err, scenario.want)
		}
		_ = tx.Rollback()
		fixture.service.mu.Unlock()
	}
	if updates, notifications := fixture.provider.callCounts(); updates != 0 || notifications != 0 {
		test.Fatal("只检验受理范围，不能发送外部请求")
	}
}

func TestRegressionRestartCancelledRunCannotStartNewNotification(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	fixture.service.cancel()
	fixture.service.wg.Wait()
	acceptanceConnection(test, fixture, "restart-cancel-notify", "telegram", "")
	run := Run{ID: "restart-cancel-run", Status: "queued", Snapshot: acceptanceTask("重启取消"), Deliveries: []Delivery{}}
	acceptancePut(test, fixture, "runs", run.ID, run)
	fixture.reopen(test)
	recovered := acceptanceGet[Run](test, fixture, "runs", run.ID)
	if recovered.Status != "cancelled" || recovered.CancelRequested {
		test.Fatal("必须覆盖重启取消而非手动停止的事实")
	}
	response := regressionRequest(test, fixture, http.MethodPost, "/api/v1/runs/"+run.ID+"/notifications", "", map[string]any{
		"operation_id": "restart-cancel-first-message", "risk_acknowledged": true,
		"channels": []notificationItem{{Kind: "telegram", ConnectionID: "restart-cancel-notify"}},
	})
	if response.Code != http.StatusConflict {
		test.Fatalf("重启取消后不得发起普通通知: %d %s", response.Code, response.Body.String())
	}
	if updates, notifications := fixture.provider.callCounts(); updates != 0 || notifications != 0 {
		test.Fatal("拒绝应不触发外部操作")
	}
}
