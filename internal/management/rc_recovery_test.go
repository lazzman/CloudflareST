package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func rcRequest(test *testing.T, client acceptanceHTTPClient, path, method string, input any, want int) []byte {
	test.Helper()
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			test.Fatal(err)
		}
	}
	response := acceptanceDo(test, client.client, method, client.base+path, body, client.csrf, client.base)
	defer response.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		test.Fatal(err)
	}
	if response.StatusCode != want {
		test.Fatalf("请求 %s %s 期望%d，实际%d：%s", method, path, want, response.StatusCode, raw)
	}
	return raw
}

func TestRCRecoveryRequiresRiskAndKeepsPriorFacts(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	server := httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	client := acceptanceLogin(test, server)
	prior := Operation{ID: "rc-prior-operation", Action: "POST /api/v1/connections ", State: "unconfirmed", CreatedAt: acceptanceNow(), Result: map[string]string{"state": "unconfirmed", "reason": "虚构未确认事实"}}
	acceptancePut(test, fixture, "operations", prior.ID, prior)
	body := map[string]any{"operation_id": "rc-review-operation", "method": "POST", "path": "/api/v1/connections"}
	path := "/api/v1/operations/" + prior.ID + "/recovery"
	rcRequest(test, client, path, "POST", body, 400)
	body["risk_acknowledged"] = true
	result := rcRequest(test, client, path, "POST", body, 202)
	var receipt Operation
	if err := json.Unmarshal(result, &receipt); err != nil {
		test.Fatal(err)
	}
	if receipt.Recovery == nil || receipt.Recovery.PriorID != prior.ID {
		test.Fatalf("未返回非敏感关联许可：%s", result)
	}
	stored := acceptanceGet[Operation](test, fixture, "operations", prior.ID)
	if stored.State != "unconfirmed" || stored.RecoveryOperationID != receipt.ID {
		test.Fatal("原未确认事实被改写")
	}
	rcRequest(test, client, path, "POST", body, 202)
	body["operation_id"] = "rc-duplicate-review"
	rcRequest(test, client, path, "POST", body, 409)
	updates, messages := fixture.provider.callCounts()
	if updates != 0 || messages != 0 {
		test.Fatal("核查许可不应触发投递")
	}
}

func TestRCRecoveryUnknownCreatesTombstoneAndSingleLinkedAttempt(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	server := httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	client := acceptanceLogin(test, server)
	priorID, reviewID, newID := "rc-unknown-operation", "rc-recovery-operation", "rc-new-operation"
	path := "/api/v1/operations/" + priorID + "/recovery"
	body := map[string]any{"operation_id": reviewID, "method": "POST", "path": "/api/v1/tasks", "risk_acknowledged": true}
	rcRequest(test, client, path, "POST", body, 202)
	rcRequest(test, client, "/api/v1/tasks", "POST", map[string]any{"operation_id": priorID, "task": acceptanceTask("迟到原请求")}, 410)
	bad := map[string]any{"operation_id": newID, "timezone": "UTC", "history_days": 30, "prior_operation_id": priorID, "recovery_id": reviewID}
	rcRequest(test, client, "/api/v1/settings", "PATCH", bad, 409)
	request := map[string]any{"operation_id": newID, "task": acceptanceTask("明确新尝试"), "prior_operation_id": priorID, "recovery_id": reviewID}
	result := rcRequest(test, client, "/api/v1/tasks", "POST", request, 202)
	var receipt Operation
	if err := json.Unmarshal(result, &receipt); err != nil {
		test.Fatal(err)
	}
	if receipt.PriorOperationID != priorID || receipt.RecoveryID != reviewID {
		test.Fatal("新动作没有持久化原关联")
	}
	rcRequest(test, client, "/api/v1/tasks", "POST", request, 202)
	request["operation_id"] = "rc-replay-new-id"
	rcRequest(test, client, "/api/v1/tasks", "POST", request, 409)
	if len(acceptanceAll[Task](test, fixture, "tasks")) != 1 {
		test.Fatal("许可重复创建任务")
	}
	result = rcRequest(test, client, "/api/v1/operations/"+priorID, "GET", nil, 200)
	if err := json.Unmarshal(result, &receipt); err != nil || receipt.Recovery == nil || receipt.Recovery.ConsumedBy != newID {
		test.Fatalf("读原号没有许可消费事实：%s %v", result, err)
	}
	fixture.clock.Set(acceptanceNow().Add(40 * 24 * time.Hour))
	fixture.service.mu.Lock()
	err := fixture.service.cleanup()
	fixture.service.mu.Unlock()
	if err != nil {
		test.Fatal(err)
	}
	fixture.reopen(test)
	fixture.service.mu.Lock()
	prior, err := get[Operation](fixture.service.db, "operations", priorID)
	if err == nil {
		err = fixture.service.attachRecovery(&prior)
	}
	fixture.service.mu.Unlock()
	if err != nil || prior.Recovery == nil || prior.Recovery.ConsumedBy != newID {
		test.Fatalf("重启/收据到期丢失核查关联：%+v %v", prior, err)
	}
}

func TestRCRecoveryAuthAndAcceptedCannotBeBypassed(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	unauthorized := httptest.NewRecorder()
	fixture.service.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/v1/operations/rc-not-known/recovery", nil))
	if unauthorized.Code != 401 {
		test.Fatal("恢复许可缺少认证保护")
	}
	server := httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	client := acceptanceLogin(test, server)
	for _, state := range []string{"accepted", "completed"} {
		id := "rc-blocked-" + state
		acceptancePut(test, fixture, "operations", id, Operation{ID: id, State: state, Action: "POST /api/v1/tasks", CreatedAt: acceptanceNow()})
		rcRequest(test, client, "/api/v1/operations/"+id+"/recovery", "POST", map[string]any{"operation_id": "rc-review-" + state, "method": "POST", "path": "/api/v1/tasks", "risk_acknowledged": true}, 409)
	}
}
