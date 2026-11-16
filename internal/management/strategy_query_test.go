package management

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/masgzy/CloudflareST/internal/optimizer"
)

func TestStrategyResultSortsAllCandidatesBeforePagination(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	run := Run{ID: "sorted-run", TaskID: "sorted-task", Status: "completed", Result: optimizer.Result{Status: "completed"}}
	for index := 0; index < 105; index++ {
		latency, speed, loss := float64(105-index), float64(index), float64(index%5)
		run.Result.Candidates = append(run.Result.Candidates, optimizer.Candidate{IP: fmt.Sprintf("192.0.2.%d", index+1), Family: "ipv4", Eligible: true, LatencyMS: &latency, SpeedMBS: &speed, LossPercent: &loss})
	}
	run.Result.Candidates = append(run.Result.Candidates, optimizer.Candidate{IP: "2001:db8::1", Family: "ipv6", Eligible: false})
	acceptancePut(test, fixture, "runs", run.ID, run)
	for _, field := range []string{"latency_ms", "speed_mb_s", "loss_percent"} {
		for _, direction := range []string{"asc", "desc"} {
			test.Run(field+"/"+direction, func(test *testing.T) {
				first, err := readCandidatePage(fixture.service.db, run.ID, 1, 50, "", "", field, direction)
				if err != nil || first.Total != 106 || len(first.Items) != 50 {
					test.Fatalf("跨页排序失败：%+v %v", first, err)
				}
				if field == "latency_ms" && direction == "asc" && *first.Items[0].LatencyMS != 1 {
					test.Fatal("不是全量排序后分页")
				}
				if field == "speed_mb_s" && direction == "desc" && *first.Items[0].SpeedMBS != 104 {
					test.Fatal("速度应按全量候选降序")
				}
				last, err := readCandidatePage(fixture.service.db, run.ID, 3, 50, "", "", field, direction)
				if err != nil || len(last.Items) != 6 || last.Items[5].IP != "2001:db8::1" {
					test.Fatalf("升降序的未测指标均应后置：%+v %v", last, err)
				}
			})
		}
	}
	filtered, err := readCandidatePage(fixture.service.db, run.ID, 1, 50, "ipv4", "eligible", "speed_mb_s", "desc")
	if err != nil || filtered.Total != 105 {
		test.Fatalf("排序不应破坏来源范围或资格筛选：%+v %v", filtered, err)
	}
	original, err := readCandidatePage(fixture.service.db, run.ID, 1, 50, "", "")
	if err != nil || original.Items[0].IP != "192.0.2.1" {
		test.Fatal("查看排序不得改写原始存储次序")
	}
	for _, sort := range [][]string{{"ip; DROP TABLE run_candidates", "asc"}, {"latency_ms", "sideways"}} {
		_, err = readCandidatePage(fixture.service.db, run.ID, 1, 50, "", "", sort...)
		acceptanceAPIError(test, err, http.StatusBadRequest)
	}
	updates, notifications := fixture.provider.callCounts()
	if updates != 0 || notifications != 0 {
		test.Fatal("查看排序不得调用推送适配器")
	}
}

func TestStrategyLatestRunUsesLatestNotLatestSuccess(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	tasks := []Task{{ID: "task-a", Name: "策略A"}, {ID: "task-b", Name: "策略B"}, {ID: "never-run", Name: "尚未运行"}}
	for index, run := range []Run{
		{ID: "older-success", TaskID: "task-a", Status: "completed"},
		{ID: "latest-failure", TaskID: "task-a", Status: "failed"},
		{ID: "other-task", TaskID: "task-b", Status: "queued"},
		{ID: "deleted-latest", TaskID: "task-a", Status: "completed", DeletedAt: timePointer(acceptanceNow())},
	} {
		run.QueuedAt = acceptanceNow().Add(time.Duration(index) * time.Minute)
		acceptancePut(test, fixture, "runs", run.ID, run)
	}
	values, err := strategyTasks(fixture.service.db, tasks)
	if err != nil || len(values) != 3 {
		test.Fatalf("策略列表失败：%+v %v", values, err)
	}
	if values[0].LatestRun == nil || values[0].LatestRun.ID != "latest-failure" || values[0].LatestRun.Status != "failed" {
		test.Fatal("不得回退成功记录或误取回收站")
	}
	if values[1].LatestRun == nil || values[1].LatestRun.ID != "other-task" || values[2].LatestRun != nil {
		test.Fatal("不能混入其他策略或虚构运行")
	}
}

func TestStrategyHTTPReadContract(test *testing.T) {
	fixture := newAcceptanceFixture(test, acceptanceNow(), nil, nil)
	task := Task{ID: "http-strategy", Name: "接口策略"}
	acceptancePut(test, fixture, "tasks", task.ID, task)
	latency, slow, fast := 10.0, 1.0, 20.0
	run := Run{ID: "http-result", TaskID: task.ID, Status: "completed", QueuedAt: acceptanceNow(), Snapshot: task, Result: optimizer.Result{Status: "completed", Candidates: []optimizer.Candidate{
		{IP: "192.0.2.1", Family: "ipv4", Eligible: true, Rank: 1, LatencyMS: &latency, SpeedMBS: &slow},
		{IP: "192.0.2.2", Family: "ipv4", Eligible: true, Rank: 2, LatencyMS: &latency, SpeedMBS: &fast},
		{IP: "2001:db8::1", Family: "ipv6", Eligible: false},
	}}}
	acceptancePut(test, fixture, "runs", run.ID, run)
	latest := Run{ID: "http-failure", TaskID: task.ID, Status: "failed", QueuedAt: acceptanceNow().Add(time.Minute), Snapshot: task}
	acceptancePut(test, fixture, "runs", latest.ID, latest)
	server := httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	for _, path := range []string{"/api/v1/tasks?view=strategies", "/api/v1/tasks/" + task.ID + "/runs/" + run.ID + "/candidates?sort=latency_ms"} {
		response := httptest.NewRecorder()
		fixture.service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			test.Fatalf("读取接口不能绕过认证：%s %d", path, response.Code)
		}
	}
	client := acceptanceLogin(test, server)
	read := func(path string, status int) []byte {
		test.Helper()
		response := acceptanceDo(test, client.client, http.MethodGet, client.base+"/api/v1"+path, nil, "", "")
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			test.Fatalf("读取 %s：状态 %d，预期 %d；%s %v", path, response.StatusCode, status, body, err)
		}
		return body
	}
	var strategies []strategyTask
	body := read("/tasks?view=strategies", http.StatusOK)
	if err := json.Unmarshal(body, &strategies); err != nil || len(strategies) != 1 || strategies[0].LatestRun == nil || strategies[0].LatestRun.ID != latest.ID {
		test.Fatalf("策略摘要应指向最新失败记录：%s %v", body, err)
	}
	var raw []map[string]json.RawMessage
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		test.Fatal(err)
	}
	if err := json.Unmarshal(raw[0]["latest_run"], &summary); err != nil || len(summary) != 4 {
		test.Fatalf("最新运行只返回必要摘要：%s %v", raw[0]["latest_run"], err)
	}
	var originalTasks []map[string]json.RawMessage
	if err := json.Unmarshal(read("/tasks", http.StatusOK), &originalTasks); err != nil || len(originalTasks) != 1 || originalTasks[0]["latest_run"] != nil {
		test.Fatal("原任务列表契约不得改变")
	}
	base := "/tasks/" + task.ID + "/runs/" + run.ID + "/candidates"
	var candidates candidatePage
	body = read(base+"?page_size=1&sort=latency_ms&direction=asc&eligibility=eligible&family=ipv4", http.StatusOK)
	if err := json.Unmarshal(body, &candidates); err != nil || candidates.Total != 2 || len(candidates.Items) != 1 || candidates.Items[0].IP != "192.0.2.2" || candidates.Items[0].Rank != 2 {
		test.Fatalf("延迟相同按速度排序且保留原排名：%s %v", body, err)
	}
	body = read(base+"?page=2&page_size=1&sort=speed_mb_s&direction=desc&eligibility=eligible", http.StatusOK)
	if err := json.Unmarshal(body, &candidates); err != nil || candidates.Page != 2 || len(candidates.Items) != 1 || candidates.Items[0].IP != "192.0.2.1" {
		test.Fatalf("先排序再分页：%s %v", body, err)
	}
	body = read(base, http.StatusOK)
	if err := json.Unmarshal(body, &candidates); err != nil || len(candidates.Items) != 3 || candidates.Items[0].IP != "192.0.2.1" {
		test.Fatalf("查看排序不得改写原始顺序：%s %v", body, err)
	}
	read(base+"?sort=not_a_metric", http.StatusBadRequest)
	read(base+"?sort=latency_ms&direction=sideways", http.StatusBadRequest)
	read("/tasks/another-task/runs/"+run.ID+"/candidates?sort=speed_mb_s", http.StatusNotFound)
	run.DeletedAt = timePointer(acceptanceNow())
	acceptancePut(test, fixture, "runs", run.ID, run)
	read(base+"?sort=latency_ms", http.StatusNotFound)
	updates, notifications := fixture.provider.callCounts()
	if updates != 0 || notifications != 0 {
		test.Fatal("结果读取不得触发 DNS 或通知")
	}
}
