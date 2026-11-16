package management

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/masgzy/CloudflareST/internal/integrations"
)

func TestPostRCNotificationRetryRejectsSupersededAttempts(test *testing.T) {
	for _, kind := range []string{"telegram", "wecom"} {
		test.Run(kind, func(test *testing.T) {
			fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
			acceptanceConnection(test, fixture, "retry-channel", kind, "")
			for _, state := range []string{"success", "pending", "dispatching", "running", "failed", "unconfirmed", "skipped"} {
				for _, connectionID := range []string{"retry-channel", "other-channel"} {
					test.Run(state+"/"+connectionID, func(test *testing.T) {
						fixture.service.mu.Lock()
						defer fixture.service.mu.Unlock()
						transaction, err := fixture.service.db.Begin()
						if err != nil {
							test.Fatal(err)
						}
						defer transaction.Rollback()
						run := Run{ID: "superseded-run", Status: "completed", Deliveries: []Delivery{
							{ID: "old-attempt", Kind: kind, ConnectionID: "retry-channel", State: "failed", Reason: "保留原失败事实"},
							{ID: "latest-attempt", Kind: kind, ConnectionID: connectionID, State: state},
						}}
						if err = put(transaction, "runs", run.ID, run); err != nil {
							test.Fatal(err)
						}
						_, _, err = fixture.service.manualNotifications(transaction, run.ID, []notificationItem{{Kind: kind, ConnectionID: "retry-channel", PriorAttemptID: "old-attempt"}}, true)
						acceptanceAPIError(test, err, http.StatusConflict)
						stored, err := getRunMetadata(transaction, run.ID)
						if err != nil || len(stored.Deliveries) != 2 || stored.Deliveries[0].Reason != "保留原失败事实" {
							test.Fatalf("拒绝后不应新增投递或改写旧事实：%+v，%v", stored.Deliveries, err)
						}
					})
				}
			}
		})
	}
}

func TestPostRCNotificationRetryKeepsLatestEligibility(test *testing.T) {
	for _, kind := range []string{"telegram", "wecom"} {
		for _, state := range []string{"failed", "unconfirmed"} {
			for _, cancelled := range []bool{false, true} {
				runStatus := "completed"
				if cancelled {
					runStatus = "cancelled"
				}
				test.Run(kind+"/"+state+"/"+runStatus, func(test *testing.T) {
					fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
					acceptanceConnection(test, fixture, "latest-channel", kind, "")
					fixture.service.mu.Lock()
					defer fixture.service.mu.Unlock()
					transaction, err := fixture.service.db.Begin()
					if err != nil {
						test.Fatal(err)
					}
					defer transaction.Rollback()
					otherKind := "telegram"
					if kind == otherKind {
						otherKind = "wecom"
					}
					run := Run{ID: "eligible-run", Status: runStatus, Deliveries: []Delivery{
						{ID: "original-attempt", Kind: kind, ConnectionID: "latest-channel", State: "failed"},
						{ID: "latest-attempt", Kind: kind, ConnectionID: "latest-channel", State: state, Cancellation: cancelled, Reason: "保留最新尝试事实"},
						{ID: "other-kind", Kind: otherKind, State: "success"},
						{ID: "dns-attempt", Kind: "dns", State: "success"},
					}}
					if err = put(transaction, "runs", run.ID, run); err != nil {
						test.Fatal(err)
					}
					channels := []notificationItem{{Kind: kind, ConnectionID: "latest-channel", PriorAttemptID: "latest-attempt"}}
					_, _, err = fixture.service.manualNotifications(transaction, run.ID, channels, false)
					acceptanceAPIError(test, err, http.StatusBadRequest)
					_, _, err = fixture.service.manualNotifications(transaction, run.ID, []notificationItem{{Kind: kind, ConnectionID: "wrong-channel", PriorAttemptID: "latest-attempt"}}, true)
					acceptanceAPIError(test, err, http.StatusConflict)
					_, _, err = fixture.service.manualNotifications(transaction, run.ID, channels, true, "latest-retry-operation")
					if err != nil {
						test.Fatalf("最新合法尝试应允许重试，其他渠道不应阻断：%v", err)
					}
					stored, err := getRunMetadata(transaction, run.ID)
					if err != nil || len(stored.Deliveries) != 5 {
						test.Fatalf("应只新增一个重试：%+v，%v", stored.Deliveries, err)
					}
					retry := stored.Deliveries[4]
					if retry.PriorAttemptID != "latest-attempt" || retry.ConnectionID != "latest-channel" || retry.Cancellation != cancelled || retry.State != "pending" || retry.OperationID != "latest-retry-operation" || stored.Deliveries[1].Reason != "保留最新尝试事实" {
						test.Fatalf("重试须保留原尝试、操作关联和取消摘要身份：%+v", stored.Deliveries)
					}
				})
			}
		}
	}
}

func TestPostRCNotificationRetryConcurrentRequestsAndIdempotency(test *testing.T) {
	release := make(chan struct{}, 1)
	provider := newAcceptanceProvider()
	provider.notify = func(ctx context.Context, _ integrations.Connection, _ string) integrations.Outcome {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return integrations.Outcome{State: "success"}
	}
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, provider)
	test.Cleanup(func() { close(release) })
	acceptanceConnection(test, fixture, "concurrent-channel", "telegram", "")
	run := Run{ID: "concurrent-run", Status: "completed", Snapshot: acceptanceTask("并发通知重试"), Deliveries: []Delivery{{ID: "failed-attempt", Kind: "telegram", ConnectionID: "concurrent-channel", State: "failed"}}}
	acceptancePut(test, fixture, "runs", run.ID, run)
	server := httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	client := acceptanceLogin(test, server)
	type outcome struct {
		status int
		body   []byte
		input  []byte
		err    error
	}
	responses := make(chan outcome, 2)
	start := make(chan struct{})
	for _, operationID := range []string{"concurrent-retry-one", "concurrent-retry-two"} {
		encoded, err := json.Marshal(map[string]any{"operation_id": operationID, "risk_acknowledged": true, "channels": []notificationItem{{Kind: "telegram", ConnectionID: "concurrent-channel", PriorAttemptID: "failed-attempt"}}})
		if err != nil {
			test.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/runs/"+run.ID+"/notifications", strings.NewReader(string(encoded)))
		if err != nil {
			test.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", client.csrf)
		request.Header.Set("Origin", server.URL)
		go func() {
			<-start
			response, err := client.client.Do(request)
			if err != nil {
				responses <- outcome{err: err}
				return
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			responses <- outcome{status: response.StatusCode, body: body, input: encoded, err: err}
		}()
	}
	close(start)
	counts := map[int]int{}
	var accepted outcome
	for range 2 {
		response := <-responses
		if response.err != nil {
			test.Fatal(response.err)
		}
		counts[response.status]++
		if response.status == http.StatusAccepted {
			accepted = response
		}
	}
	if counts[http.StatusAccepted] != 1 || counts[http.StatusConflict] != 1 {
		test.Fatalf("并发新操作号只能受理一次：%v", counts)
	}
	response := acceptanceDo(test, client.client, http.MethodPost, server.URL+"/api/v1/runs/"+run.ID+"/notifications", accepted.input, client.csrf, server.URL)
	defer response.Body.Close()
	var original, replay Operation
	if err := json.Unmarshal(accepted.body, &original); err != nil {
		test.Fatal(err)
	}
	if err := json.NewDecoder(response.Body).Decode(&replay); err != nil {
		test.Fatal(err)
	}
	stored := acceptanceGet[Run](test, fixture, "runs", run.ID)
	if response.StatusCode != http.StatusAccepted || replay.ID != original.ID || len(stored.Deliveries) != 2 || stored.Deliveries[0].State != "failed" {
		test.Fatalf("同号重试应返回原收据且不新增投递：HTTP=%d，%+v，%+v", response.StatusCode, replay, stored.Deliveries)
	}
	if operations := acceptanceAll[Operation](test, fixture, "operations"); len(operations) != 1 {
		test.Fatalf("冲突请求不应生成第二份业务收据：%+v", operations)
	}
	release <- struct{}{}
	acceptanceWaitDelivery(test, fixture, run.ID, stored.Deliveries[1].ID, "success")
	replayed := acceptanceDo(test, client.client, http.MethodPost, server.URL+"/api/v1/runs/"+run.ID+"/notifications", accepted.input, client.csrf, server.URL)
	acceptanceStatus(test, replayed, http.StatusAccepted)
	updates, notifications := provider.callCounts()
	if updates != 0 || notifications != 1 {
		test.Fatalf("只能调用一次替身通知且不得运行DNS：%d/%d", updates, notifications)
	}
	test.Logf("并发新操作号：202=%d，409=%d；完成前后同号重放不新发；模拟通知=%d，DNS=%d", counts[202], counts[409], notifications, updates)
}

func TestPostRCNotificationRetryBatchConflictRollsBack(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	acceptanceConnection(test, fixture, "batch-telegram", "telegram", "")
	acceptanceConnection(test, fixture, "batch-wecom", "wecom", "")
	run := Run{ID: "batch-run", Status: "completed", Deliveries: []Delivery{
		{ID: "telegram-failed", Kind: "telegram", ConnectionID: "batch-telegram", State: "failed"},
		{ID: "wecom-failed", Kind: "wecom", ConnectionID: "batch-wecom", State: "failed"},
		{ID: "wecom-success", Kind: "wecom", ConnectionID: "batch-wecom", State: "success"},
	}}
	acceptancePut(test, fixture, "runs", run.ID, run)
	response := regressionRequest(test, fixture, http.MethodPost, "/api/v1/runs/"+run.ID+"/notifications", "", map[string]any{
		"operation_id": "batch-retry-operation", "risk_acknowledged": true,
		"channels": []notificationItem{
			{Kind: "telegram", ConnectionID: "batch-telegram", PriorAttemptID: "telegram-failed"},
			{Kind: "wecom", ConnectionID: "batch-wecom", PriorAttemptID: "wecom-failed"},
		},
	})
	stored := acceptanceGet[Run](test, fixture, "runs", run.ID)
	updates, notifications := fixture.provider.callCounts()
	if response.Code != http.StatusConflict || len(stored.Deliveries) != 3 || updates != 0 || notifications != 0 || len(acceptanceAll[Operation](test, fixture, "operations")) != 0 {
		test.Fatalf("后一渠道冲突必须回滚整批请求：HTTP=%d，投递=%+v，调用=%d/%d", response.Code, stored.Deliveries, updates, notifications)
	}
}
