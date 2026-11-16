package management

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/masgzy/CloudflareST/internal/optimizer"
)

func TestRCPrivateMigrationScrubsLegacyCopiesWithoutReplay(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	secret := "FAKE_LEGACY_DOWNLOAD_SECRET"
	task := acceptanceTask("旧任务")
	task.ID = "legacy-secret-task"
	task.Revision = 7
	task.Config.DownloadURL = "https://example.invalid/data?token=" + secret
	task.Schedule.Enabled = true
	run := Run{ID: "legacy-secret-run", TaskID: task.ID, Status: "completed", Snapshot: task, Result: optimizer.Result{Status: "completed", Error: "连接失败: https://example.invalid/data?token=" + secret}}
	operation := Operation{ID: "legacy-cancel-operation", ObjectID: run.ID, State: "completed", RequestHash: "retain-hash", Result: run, CreatedAt: acceptanceNow()}
	acceptanceTx(test, fixture, func(tx *sql.Tx) error {
		for _, item := range []struct {
			kind, id string
			value    any
		}{{"tasks", task.ID, task}, {"runs", run.ID, run}, {"operations", operation.ID, operation}, {"logs", "legacy-secret-log", Log{ID: "legacy-secret-log", Reason: "token=" + secret}}} {
			if err := putRaw(tx, item.kind, item.id, item.value); err != nil {
				return err
			}
		}
		candidate := optimizer.Candidate{IP: "192.0.2.1", Family: "ipv4", Reason: "读取 https://example.invalid/data?token=" + secret, Steps: []optimizer.Decision{{Reason: "secret=" + secret}}}
		body, err := json.Marshal(candidate)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO run_candidates(run_id,position,ip,family,eligible,state,body) VALUES(?,?,?,?,?,?,?)", run.ID, 0, candidate.IP, "ipv4", 0, "failed", body); err != nil {
			return err
		}
		_, err = tx.Exec("PRAGMA user_version=2")
		return err
	})
	fixture.reopen(test)
	stored := acceptanceGet[Task](test, fixture, "tasks", task.ID)
	if stored.Config.DownloadURL != sensitiveURLRemoved || stored.Schedule.Enabled || stored.ConfigurationIssue == "" || stored.Revision != 8 {
		test.Fatalf("旧任务未安全停用与提示：%+v", stored)
	}
	prior := acceptanceGet[Operation](test, fixture, "operations", operation.ID)
	body, _ := json.Marshal(prior.Result)
	if strings.Contains(string(body), "snapshot") || prior.RequestHash != "retain-hash" || prior.State != "completed" {
		test.Fatalf("收据清理改坏事实或仍有副本：%s", body)
	}
	fixture.service.mu.Lock()
	var objects, candidates int
	err := fixture.service.db.QueryRow("SELECT COUNT(*) FROM objects WHERE instr(CAST(body AS TEXT),?)>0", secret).Scan(&objects)
	if err == nil {
		err = fixture.service.db.QueryRow("SELECT COUNT(*) FROM run_candidates WHERE instr(CAST(body AS TEXT),?)>0", secret).Scan(&candidates)
	}
	_, enqueueErr := enqueue(fixture.service.db, stored, "manual", "", fixture.service)
	fixture.service.mu.Unlock()
	if err != nil || objects != 0 || candidates != 0 || enqueueErr == nil {
		test.Fatalf("秘密残留或停用绕过：%d/%d %v %v", objects, candidates, err, enqueueErr)
	}
	updates, messages := fixture.provider.callCounts()
	if updates != 0 || messages != 0 {
		test.Fatal("迁移不应执行投递")
	}
	stored.Config.DownloadURL = "https://example.invalid/file?bytes=1000"
	saved, err := acceptanceSaveTask(fixture, stored, stored.ID, stored.Revision)
	if err != nil || saved.ConfigurationIssue != "" || saved.Schedule.Enabled {
		test.Fatalf("安全修正未生效或静默启用：%+v %v", saved, err)
	}
	fixture.reopen(test)
	if acceptanceGet[Task](test, fixture, "tasks", task.ID).Revision != 9 {
		test.Fatal("重复启动重新修改任务版本")
	}
}

func TestRCPrivateMigrationRollbackKeepsPreviousVersion(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	fixture.service.mu.Lock()
	defer fixture.service.mu.Unlock()
	if _, err := fixture.service.db.Exec("INSERT INTO objects(kind,id,body) VALUES('zz-broken','broken','[]'); PRAGMA user_version=2"); err != nil {
		test.Fatal(err)
	}
	if err := migratePrivateHistory(fixture.service.db); err == nil {
		test.Fatal("损坏对象必须阻止迁移")
	}
	var version int
	if err := fixture.service.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		test.Fatalf("失败迁移错误升级：%d %v", version, err)
	}
}

func TestRCNormalSummaryStillUsesOptimizationDuringTail(test *testing.T) {
	run := Run{Status: "running", Result: optimizer.Result{Status: "completed"}}
	if !strings.Contains(summary(run, false), "结束结论 completed") {
		test.Fatal("正常收尾仍须使用已确定优选结论")
	}
	run.Status = "interrupted"
	message := summary(run, false)
	if !strings.Contains(message, "结束结论 interrupted") || !strings.Contains(message, "优选阶段结论 completed") {
		test.Fatal("必须区分执行中断与已保存优选结果")
	}
}

func TestRCVersionThreeRecleansCDNAuthenticationParameters(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	for _, parameter := range []string{"auth_key", "hdnts", "hdnea"} {
		task := acceptanceTask("CDN鉴权旧配置")
		task.ID = "v3-" + parameter
		task.Revision = 1
		task.Config.DownloadURL = "https://example.invalid/file?" + parameter + "=FAKE_CDN_PRIVATE"
		task.Schedule.Enabled = true
		if _, err := acceptanceSaveTask(fixture, task, "", 0); err == nil {
			test.Fatalf("新配置未拒绝%s", parameter)
		}
		acceptanceTx(test, fixture, func(tx *sql.Tx) error { return putRaw(tx, "tasks", task.ID, task) })
	}
	fixture.service.mu.Lock()
	_, err := fixture.service.db.Exec("PRAGMA user_version=3")
	fixture.service.mu.Unlock()
	if err != nil {
		test.Fatal(err)
	}
	fixture.reopen(test)
	for _, parameter := range []string{"auth_key", "hdnts", "hdnea"} {
		task := acceptanceGet[Task](test, fixture, "tasks", "v3-"+parameter)
		if task.Config.DownloadURL != sensitiveURLRemoved || task.Schedule.Enabled {
			test.Errorf("版本3配置未清理%s", parameter)
		}
	}
}
