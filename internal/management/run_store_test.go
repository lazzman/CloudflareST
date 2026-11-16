package management

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/masgzy/CloudflareST/internal/optimizer"
)

func TestRunStorageSeparatesCandidatesAndPreservesMetadataChanges(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	run := Run{ID: "split-run", TaskID: "split-task", Status: "completed", Result: optimizer.Result{Status: "completed", Candidates: []optimizer.Candidate{}}}
	for index := 0; index < 105; index++ {
		run.Result.Candidates = append(run.Result.Candidates, optimizer.Candidate{IP: fmt.Sprintf("192.0.2.%d", index+1), Family: "ipv4", Eligible: index%2 == 0, Rank: index + 1, State: "passed"})
	}
	acceptancePut(test, fixture, "runs", run.ID, run)
	metadata, err := getRunMetadata(fixture.service.db, run.ID)
	if err != nil || metadata.Result.Candidates != nil || metadata.CandidateSummary.Total != 105 || metadata.CandidateSummary.Families[0].Eligible != 53 {
		test.Fatalf("摘要没有与候选分离：%+v %v", metadata.CandidateSummary, err)
	}
	metadata.Reason = "仅改变元数据"
	acceptancePut(test, fixture, "runs", run.ID, metadata)
	page, err := readCandidatePage(fixture.service.db, run.ID, 2, 50, "ipv4", "")
	if err != nil || page.Total != 105 || len(page.Items) != 50 || page.Items[0].IP != "192.0.2.51" {
		test.Fatalf("分页错误或元数据更新清空候选：%+v %v", page, err)
	}
	filtered, err := readCandidatePage(fixture.service.db, run.ID, 99, 50, "ipv4", "eligible")
	if err != nil || filtered.Total != 53 || filtered.Page != 2 || len(filtered.Items) != 3 {
		test.Fatalf("筛选或越页纠正错误：%+v %v", filtered, err)
	}
	qualified, err := eligibleCandidate(fixture.service.db, run.ID, "192.0.2.1", "ipv4")
	if err != nil || !qualified {
		test.Fatalf("合格IP查询错误：%v", err)
	}
	qualified, err = eligibleCandidate(fixture.service.db, run.ID, "192.0.2.2", "ipv4")
	if err != nil || qualified {
		test.Fatalf("不合格IP被放行：%v", err)
	}
	if err = remove(fixture.service.db, "runs", run.ID); err != nil {
		test.Fatal(err)
	}
	page, err = readCandidatePage(fixture.service.db, run.ID, 1, 50, "", "")
	if err != nil || page.Total != 0 {
		test.Fatalf("删除留下候选：%d %v", page.Total, err)
	}
}

func TestRunStorageMigratesLegacyWithoutReplay(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	run := Run{ID: "legacy-run", TaskID: "legacy-task", Status: "completed", Result: optimizer.Result{Status: "completed", Candidates: []optimizer.Candidate{{IP: "192.0.2.1", Family: "ipv4", Eligible: true, Rank: 1, Sources: []string{"source-original"}}}}}
	fixture.service.mu.Lock()
	err := putRaw(fixture.service.db, "runs", run.ID, run)
	if err == nil {
		_, err = fixture.service.db.Exec("PRAGMA user_version=1")
	}
	fixture.service.mu.Unlock()
	if err != nil {
		test.Fatal(err)
	}
	fixture.reopen(test)
	stored := acceptanceGet[Run](test, fixture, "runs", run.ID)
	if len(stored.Result.Candidates) != 1 || stored.Result.Candidates[0].Sources[0] != "source-original" || stored.Status != "completed" {
		test.Fatalf("迁移丢失原事实：%+v", stored)
	}
	updates, messages := fixture.provider.callCounts()
	if updates != 0 || messages != 0 {
		test.Fatal("迁移触发外部操作")
	}
	fixture.reopen(test)
	page, err := readCandidatePage(fixture.service.db, run.ID, 1, 50, "", "")
	if err != nil || page.Total != 1 {
		test.Fatalf("迁移不幂等：%+v %v", page, err)
	}
}

func TestRunQueriesAreBoundedAndFiltersStayConjunctive(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	for index := 0; index < 24; index++ {
		run := Run{ID: fmt.Sprintf("list-%02d", index), TaskID: "query-task", Trigger: "manual", Status: "completed", QueuedAt: acceptanceNow(), Deliveries: []Delivery{{Kind: "dns", State: "failed"}, {Kind: "telegram", State: "unconfirmed"}}, Result: optimizer.Result{Candidates: []optimizer.Candidate{{IP: "192.0.2.1", Family: "ipv4", Eligible: true}}}}
		if index == 23 {
			run.TaskID = "another-task"
		}
		acceptancePut(test, fixture, "runs", run.ID, run)
	}
	request := httptest.NewRequest("GET", "/?page=2&page_size=20&status=completed&trigger=manual&dns_state=failed&notification_state=unconfirmed", nil)
	value, err := readRunPage(fixture.service.db, "query-task", false, request)
	if err != nil {
		test.Fatal(err)
	}
	page := value.(runPage)
	if page.Total != 23 || len(page.Items) != 3 || page.Items[0].Result.Candidates != nil {
		test.Fatalf("分页/归属/精简失败：%+v", page)
	}
	response := httptest.NewRecorder()
	fixture.service.readAPI(response, httptest.NewRequest("GET", "/api/v1/tasks/query-task/runs/list-00/progress", nil))
	var progress map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &progress); err != nil {
		test.Fatal(err)
	}
	if _, ok := progress["result"]; ok {
		test.Fatal("轻量进度仍包含完整result")
	}
	if progress["server_time"] == nil {
		test.Fatal("没有服务端时刻")
	}
	for _, target := range []string{"/api/v1/tasks/another-task/runs/list-00/candidates", "/api/v1/tasks/query-task/runs/missing/candidates"} {
		response := httptest.NewRecorder()
		fixture.service.readAPI(response, httptest.NewRequest("GET", target, nil))
		if response.Code != 404 {
			test.Fatalf("候选接口绕过归属/不存在检查：%s %d", target, response.Code)
		}
	}
	removed := acceptanceGet[Run](test, fixture, "runs", "list-00")
	removed.DeletedAt = timePointer(acceptanceNow())
	acceptancePut(test, fixture, "runs", removed.ID, removed)
	response = httptest.NewRecorder()
	fixture.service.readAPI(response, httptest.NewRequest("GET", "/api/v1/tasks/query-task/runs/list-00/candidates", nil))
	if response.Code != 404 {
		test.Fatal("已删除记录的候选仍可查询")
	}
}

func TestProgressKeepsBothFamiliesAndSeparateCounts(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	run := Run{}
	total := 3
	fixture.service.recordProgress(&run, (optimizer.Progress{Stage: "probe", Family: "ipv4", Unit: "IP", Done: 3, Total: &total}).WithCounts(optimizer.StageCounts{Succeeded: 2, Failed: 1}))
	fixture.service.recordProgress(&run, (optimizer.Progress{Stage: "probe", Family: "ipv6", Unit: "IP", Done: 1, Total: &total}).WithCounts(optimizer.StageCounts{Succeeded: 1}))
	if len(run.StageProgress) != 2 || run.StageProgress[0].Counts.Failed != 1 || run.StageProgress[0].Done != 3 || run.UpdatedAt == nil {
		test.Fatalf("分族计数被覆盖：%+v", run)
	}
}

func TestRunStorageMigrationRollsBackOnInvalidLegacyRecord(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	fixture.service.mu.Lock()
	defer fixture.service.mu.Unlock()
	run := Run{ID: "a-valid-legacy", Status: "completed", Result: optimizer.Result{Candidates: []optimizer.Candidate{{IP: "192.0.2.1", Family: "ipv4"}}}}
	if err := putRaw(fixture.service.db, "runs", run.ID, run); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.service.db.Exec("INSERT INTO objects(kind,id,body) VALUES('runs','z-invalid-legacy','[]')"); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.service.db.Exec("PRAGMA user_version=1"); err != nil {
		test.Fatal(err)
	}
	if err := migrateRunStorage(fixture.service.db); err == nil {
		test.Fatal("损坏旧记录应阻断迁移")
	}
	var version, candidates int
	if err := fixture.service.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		test.Fatal(err)
	}
	if err := fixture.service.db.QueryRow("SELECT COUNT(*) FROM run_candidates WHERE run_id=?", run.ID).Scan(&candidates); err != nil {
		test.Fatal(err)
	}
	original, err := getRaw[Run](fixture.service.db, "runs", run.ID)
	if err != nil || version != 1 || candidates != 0 || len(original.Result.Candidates) != 1 {
		test.Fatalf("迁移失败未完整回滚：版本=%d,候选=%d,原记录=%+v,error=%v", version, candidates, original, err)
	}
}

func TestRunStorageMaximumCandidateEnvelope(test *testing.T) {
	if os.Getenv("CFST_STORAGE_CAPACITY_TEST") != "1" {
		test.Skip("设置 CFST_STORAGE_CAPACITY_TEST=1 执行隔离存储容量验收，不联网测速")
	}
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	run := Run{ID: "capacity-run", TaskID: "capacity-task", Status: "completed", Result: optimizer.Result{Status: "completed", Candidates: make([]optimizer.Candidate, 0, 200000)}}
	for index := 0; index < 100000; index++ {
		for _, family := range []string{"ipv4", "ipv6"} {
			ip := fmt.Sprintf("10.%d.%d.%d", index/65536, index/256%256, index%256)
			if family == "ipv6" {
				ip = fmt.Sprintf("2001:db8::%x:%x", index/65536, index%65536)
			}
			run.Result.Candidates = append(run.Result.Candidates, optimizer.Candidate{IP: ip, Family: family, Eligible: index < 20, Rank: index + 1, State: "passed", Sources: []string{"capacity-source"}, Steps: []optimizer.Decision{{Stage: "latency", State: "passed", Reason: "虚构容量数据，未执行探测"}}})
		}
	}
	started := time.Now()
	acceptancePut(test, fixture, "runs", run.ID, run)
	savedIn := time.Since(started)
	fixture.reopen(test)
	started = time.Now()
	page, err := readCandidatePage(fixture.service.db, run.ID, 2000, 50, "ipv6", "")
	if err != nil || page.Total != 100000 || len(page.Items) != 50 {
		test.Fatalf("最大候选分页失败：%+v %v", page, err)
	}
	pageTime := time.Since(started)
	response := httptest.NewRecorder()
	fixture.service.readAPI(response, httptest.NewRequest("GET", "/api/v1/tasks/capacity-task/runs/capacity-run/progress", nil))
	if response.Code != 200 || response.Body.Len() > 16384 {
		test.Fatalf("进度响应未保持轻量：status=%d bytes=%d", response.Code, response.Body.Len())
	}
	info, err := os.Stat(filepath.Join(fixture.dataDir, "management.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	test.Logf("虚构候选200000（两族各100000）；写入=%s；重启后单页=%s；进度响应=%d字节；数据库=%d字节；不代表真实网络测速压测", savedIn, pageTime, response.Body.Len(), info.Size())
}
