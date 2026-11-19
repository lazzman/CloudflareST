package management

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestActionKindClassifiesBusinessAndHTTPActions(t *testing.T) {
	cases := map[string]string{
		"task_create":                                                    "task_create",
		"POST /api/v1/tasks ":                                            "task_create",
		"PATCH /api/v1/tasks/task-1 4":                                   "task_update",
		"POST /api/v1/tasks/task-1/runs ":                                "run_start",
		"POST /api/v1/tasks/task-1/archive ":                             "task_archive",
		"POST /api/v1/tasks/task-1/restore ":                             "task_restore",
		"POST /api/v1/tasks/task-1/schedule-state ":                      "schedule_update",
		"POST /api/v1/runs/run-1/cancel ":                                "run_cancel",
		"POST /api/v1/runs/run-1/restore ":                               "history_restore",
		"POST /api/v1/runs/run-1/dns-deliveries ":                        "dns_delivery",
		"POST /api/v1/runs/run-1/notifications ":                         "notification_delivery",
		"DELETE /api/v1/runs/run-1 ":                                     "history_delete",
		"POST /api/v1/dns-targets/target-1/auto-write-protection/clear ": "dns_protection_clear",
		"PATCH /api/v1/settings ":                                        "settings_update",
		"POST /api/v1/connections ":                                      "connection_create",
		"PATCH /api/v1/connections/connection-1 2":                       "connection_update",
		"DELETE /api/v1/connections/connection-1 ":                       "connection_delete",
		"POST /api/v1/operations/operation-1/recovery ":                  "operation_recovery",
		"delivery_result":                                                "delivery_result",
		"history_expire":                                                 "history_expire",
		"schedule_trigger":                                               "schedule_trigger",
		"restart_recovery":                                               "restart_recovery",
		"fake_operation":                                                 actionKindOther,
		"":                                                               actionKindOther,
		"POST /api/v1/unknown ":                                          actionKindOther,
	}
	for action, expected := range cases {
		if actual := actionKind(action); actual != expected {
			t.Errorf("actionKind(%q) = %q, want %q", action, actual, expected)
		}
	}
}

func TestLogActionFilterMatchesKindOrLegacySubstring(t *testing.T) {
	cases := []struct {
		action string
		filter string
		want   bool
	}{
		{"POST /api/v1/tasks/task-1/runs ", "", true},
		{"POST /api/v1/tasks/task-1/runs ", "run_start", true},
		{"POST /api/v1/tasks/task-1/runs ", "run_finished", false},
		{"run_finished", "run_finished", true},
		{"target_protection", "target_protection", true},
		{"POST /api/v1/tasks/task-1/runs ", "runs", true},
		{"run_finished", "target_protection", false},
		{"fake_operation", "other", true},
		{"POST /api/v1/tasks/task-1/runs ", "other", false},
	}
	for _, test := range cases {
		if actual := logActionMatches(test.action, test.filter); actual != test.want {
			t.Errorf("logActionMatches(%q, %q) = %v, want %v", test.action, test.filter, actual, test.want)
		}
	}
}

func TestOperationLogsReturnActionKindAndFilterByKind(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	acceptancePut(t, fixture, "logs", "log-http", Log{ID: "log-http", At: acceptanceNow(), Actor: "共享管理身份", Action: "POST /api/v1/tasks/task-1/runs ", Object: "run-1", Result: "accepted"})
	acceptancePut(t, fixture, "logs", "log-business", Log{ID: "log-business", At: acceptanceNow(), Actor: "系统", Action: "run_finished", Object: "run-1", Result: "completed"})
	acceptancePut(t, fixture, "logs", "log-other", Log{ID: "log-other", At: acceptanceNow(), Actor: "系统", Action: "fake_operation", Object: "object-1", Result: "accepted"})

	read := func(query string) []Log {
		response := httptest.NewRecorder()
		fixture.service.readAPI(response, httptest.NewRequest("GET", "/api/v1/operation-logs"+query, nil))
		if response.Code != 200 {
			t.Fatalf("读取操作日志 %s: 状态 %d, %s", query, response.Code, response.Body.String())
		}
		var logs []Log
		if err := json.Unmarshal(response.Body.Bytes(), &logs); err != nil {
			t.Fatal(err)
		}
		return logs
	}

	all := read("")
	kinds := map[string]string{}
	for _, entry := range all {
		kinds[entry.ID] = entry.ActionKind
	}
	if kinds["log-http"] != "run_start" || kinds["log-business"] != "run_finished" || kinds["log-other"] != actionKindOther {
		t.Fatalf("动作类型返回错误: %+v", kinds)
	}
	if logs := read("?action=run_start"); len(logs) != 1 || logs[0].ID != "log-http" {
		t.Fatalf("按动作类型筛选错误: %+v", logs)
	}
	if logs := read("?action=POST%20/api/v1/tasks"); len(logs) != 1 || logs[0].ID != "log-http" {
		t.Fatalf("旧关键字筛选错误: %+v", logs)
	}
	if logs := read("?action=other"); len(logs) != 1 || logs[0].ID != "log-other" {
		t.Fatalf("其他动作筛选错误: %+v", logs)
	}
	if logs := read("?actor=" + url.QueryEscape("系统")); len(logs) != 2 {
		t.Fatalf("操作人筛选错误: %+v", logs)
	}
	if logs := read("?actor=" + url.QueryEscape("共享管理身份") + "&result=accepted"); len(logs) != 1 || logs[0].ID != "log-http" {
		t.Fatalf("操作人与结果组合筛选错误: %+v", logs)
	}
}
