package management

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/masgzy/CloudflareST/internal/optimizer"
)

func TestRCRegressionDownloadSecretMustNotEnterReadableSnapshot(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	task := acceptanceTask("下载地址敏感查询参数")
	secret := "FAKE_DOWNLOAD_TOKEN_DO_NOT_USE"
	task.Config.DownloadURL = "https://download.example.invalid/file.bin?token=" + secret
	saved, err := acceptanceSaveTask(fixture, task, "", 0)
	if err != nil {
		test.Logf("敏感 URL 在保存时被拒绝：%v", err)
		return
	}
	var run Run
	acceptanceTx(test, fixture, func(transaction *sql.Tx) error {
		var enqueueErr error
		run, enqueueErr = enqueue(transaction, saved, "manual", "", fixture.service)
		return enqueueErr
	})
	fixture.service.mu.Lock()
	defer fixture.service.mu.Unlock()
	stored, err := getRunMetadata(fixture.service.db, run.ID)
	if err != nil {
		test.Fatal(err)
	}
	body, err := json.Marshal(stored.Snapshot)
	if err != nil {
		test.Fatal(err)
	}
	exposed := strings.Contains(string(body), secret)
	updates, messages := fixture.provider.callCounts()
	test.Logf("配置保存成功；历史快照含虚构token=%t；DNS调用=%d，通知调用=%d；无真实下载", exposed, updates, messages)
	if exposed {
		test.Fatal("R3非敏感快照契约未满足：下载URL中的认证查询参数被原样保存在可读取历史中")
	}
}

func TestRCRegressionInterruptedSummaryKeepsMainStatus(test *testing.T) {
	for _, outcome := range []string{"", "completed"} {
		label := "优选阶段未保存"
		if outcome != "" {
			label = "优选完成后收尾中断"
		}
		test.Run(label, func(test *testing.T) {
			fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
			acceptanceConnection(test, fixture, "audit-interrupted-channel", "telegram", "")
			run := Run{ID: "audit-interrupted-run", TaskID: "audit-interrupted-task", TaskName: "模拟中断摘要", Status: "running", Snapshot: acceptanceTask("模拟中断摘要"), Result: optimizer.Result{Status: outcome, Candidates: []optimizer.Candidate{}}, Deliveries: []Delivery{}}
			acceptancePut(test, fixture, "runs", run.ID, run)
			fixture.service.mu.Lock()
			err := fixture.service.recoverRuns()
			fixture.service.mu.Unlock()
			if err != nil {
				test.Fatal(err)
			}
			recovered := acceptanceGet[Run](test, fixture, "runs", run.ID)
			if recovered.Status != "interrupted" {
				test.Fatalf("恢复未得到中断状态：%s", recovered.Status)
			}
			ids := acceptanceRunManualNotification(test, fixture, run.ID, []notificationItem{{Kind: "telegram", ConnectionID: "audit-interrupted-channel"}}, true)
			acceptanceWake(fixture)
			acceptanceWaitDelivery(test, fixture, run.ID, ids[0], "success")
			messages := fixture.provider.notificationMessages()
			if len(messages) != 1 {
				test.Fatalf("模拟摘要调用次数错误：%d", len(messages))
			}
			test.Logf("恢复主状态=%s；优选结论=%q；模拟摘要=%q", recovered.Status, outcome, messages[0])
			if !strings.Contains(messages[0], "结束结论 interrupted") && !strings.Contains(messages[0], "结束结论 已中断") {
				test.Fatal("AC-72/98：人工摘要未体现运行已中断的真实结束状态")
			}
		})
	}
}

func TestRCRegressionPurgeRemovesSnapshotFromCancelReceipt(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	run := Run{ID: "audit-cancel-run", TaskID: "audit-cancel-task", TaskName: "回收站清理检验", Status: "queued", Snapshot: acceptanceTask("FAKE_PURGED_SNAPSHOT_MARKER"), QueuedAt: acceptanceNow(), Deliveries: []Delivery{}}
	operationID := "audit-cancel-operation-id"
	_, _, err := fixture.service.mutate(operationID, "POST /api/v1/runs/"+run.ID+"/cancel", []byte("{}"), func(transaction *sql.Tx) (string, any, error) {
		if err := put(transaction, "runs", run.ID, run); err != nil {
			return "", nil, err
		}
		return fixture.service.cancelRun(transaction, run.ID)
	})
	if err != nil {
		test.Fatal(err)
	}
	acceptanceTx(test, fixture, func(transaction *sql.Tx) error {
		_, _, err := fixture.service.historyAction(transaction, run.ID, false)
		return err
	})
	fixture.clock.Set(acceptanceNow().Add(8 * 24 * time.Hour))
	fixture.service.mu.Lock()
	err = fixture.service.cleanup()
	_, runErr := getRunMetadata(fixture.service.db, run.ID)
	fixture.service.mu.Unlock()
	if err != nil || !errors.Is(runErr, errNotFound) {
		test.Fatalf("回收站尚未完成清理：%v / %v", err, runErr)
	}
	response := httptest.NewRecorder()
	fixture.service.readAPI(response, httptest.NewRequest("GET", "/api/v1/operations/"+operationID, nil))
	if response.Code != 200 {
		test.Fatalf("读取原操作号失败：%d", response.Code)
	}
	var receipt Operation
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		test.Fatal(err)
	}
	retained := strings.Contains(response.Body.String(), "FAKE_PURGED_SNAPSHOT_MARKER")
	test.Logf("原运行已永久清理；取消收据状态=%s；查询收据仍包含已删快照=%t；未发真实外部请求", receipt.State, retained)
	if retained {
		test.Fatal("AC-64：原运行已清理，但取消收据仍公开保留完整历史快照")
	}
}
