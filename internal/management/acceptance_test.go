package management

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/masgzy/CloudflareST/internal/integrations"
	"github.com/masgzy/CloudflareST/internal/optimizer"
)

const acceptancePassword = "FAKE_acceptance_password_only_not_a_real_secret"

type acceptanceClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *acceptanceClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *acceptanceClock) Set(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}

type acceptanceEngine struct {
	run func(context.Context, optimizer.Config, func(optimizer.Progress)) (optimizer.Result, error)
}

func (engine *acceptanceEngine) Run(ctx context.Context, config optimizer.Config, progress func(optimizer.Progress)) (optimizer.Result, error) {
	if engine.run != nil {
		return engine.run(ctx, config, progress)
	}
	return optimizer.Result{Status: "completed", Candidates: []optimizer.Candidate{}, Sources: []optimizer.SourceResult{}, Budgets: []optimizer.Budget{}}, nil
}

type acceptanceProvider struct {
	mu            sync.Mutex
	identity      string
	update        func(context.Context, integrations.Connection, integrations.Target, string) integrations.Outcome
	notify        func(context.Context, integrations.Connection, string) integrations.Outcome
	updates       []string
	notifications []string
}

func newAcceptanceProvider() *acceptanceProvider {
	return &acceptanceProvider{identity: "fake-account-001"}
}

func (provider *acceptanceProvider) Domains(context.Context, integrations.Connection) ([]string, error) {
	return nil, nil
}

func (provider *acceptanceProvider) Records(context.Context, integrations.Connection, string) ([]integrations.Target, error) {
	return nil, nil
}

func (provider *acceptanceProvider) Update(ctx context.Context, connection integrations.Connection, target integrations.Target, ip string) integrations.Outcome {
	provider.mu.Lock()
	provider.updates = append(provider.updates, target.RecordID+"="+ip)
	provider.mu.Unlock()
	if provider.update != nil {
		return provider.update(ctx, connection, target, ip)
	}
	return integrations.Outcome{State: "success", Verified: true}
}

func (provider *acceptanceProvider) Notify(ctx context.Context, connection integrations.Connection, message string) integrations.Outcome {
	provider.mu.Lock()
	provider.notifications = append(provider.notifications, message)
	provider.mu.Unlock()
	if provider.notify != nil {
		return provider.notify(ctx, connection, message)
	}
	return integrations.Outcome{State: "success", Verified: true}
}

func (provider *acceptanceProvider) AccountIdentity(context.Context, integrations.Connection) (string, error) {
	return provider.identity, nil
}

func (provider *acceptanceProvider) callCounts() (int, int) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.updates), len(provider.notifications)
}

func (provider *acceptanceProvider) notificationMessages() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]string(nil), provider.notifications...)
}

type acceptanceFixture struct {
	service  *Service
	dataDir  string
	clock    *acceptanceClock
	engine   Engine
	provider *acceptanceProvider
	closed   bool
}

func newAcceptanceFixture(t *testing.T, now time.Time, engine Engine, provider *acceptanceProvider) *acceptanceFixture {
	t.Helper()
	if engine == nil {
		engine = &acceptanceEngine{}
	}
	if provider == nil {
		provider = newAcceptanceProvider()
	}
	fixture := &acceptanceFixture{dataDir: t.TempDir(), clock: &acceptanceClock{now: now}, engine: engine, provider: provider}
	fixture.open(t)
	t.Cleanup(func() {
		if !fixture.closed {
			if err := fixture.service.Close(); err != nil {
				t.Errorf("关闭验收服务: %v", err)
			}
			fixture.closed = true
		}
	})
	return fixture
}

func (fixture *acceptanceFixture) open(t *testing.T) {
	t.Helper()
	service, err := Open(Config{DataDir: fixture.dataDir, Password: acceptancePassword, Engine: fixture.engine, Provider: fixture.provider, Now: fixture.clock.Now})
	if err != nil {
		t.Fatalf("打开验收服务: %v", err)
	}
	fixture.service = service
	fixture.closed = false
}

func (fixture *acceptanceFixture) close(t *testing.T) {
	t.Helper()
	if fixture.closed {
		return
	}
	if err := fixture.service.Close(); err != nil {
		t.Fatalf("关闭验收服务: %v", err)
	}
	fixture.closed = true
}

func (fixture *acceptanceFixture) reopen(t *testing.T) {
	t.Helper()
	fixture.close(t)
	fixture.open(t)
}

func acceptanceNow() time.Time {
	return time.Date(2026, time.November, 14, 0, 15, 0, 0, time.UTC)
}

func acceptanceConfig() optimizer.Config {
	config := optimizer.DefaultConfig()
	config.Sources = []optimizer.Source{{Kind: "ip", Value: "192.0.2.42"}}
	config.ProbeMode = "tcp"
	config.DownloadURL = "http://127.0.0.1:54321/acceptance-not-requested.bin"
	config.DownloadEnabled = false
	return config
}

func acceptanceTask(name string) Task {
	return Task{Name: name, Config: acceptanceConfig(), Schedule: Schedule{Kind: "interval", IntervalMinutes: 60}}
}

func acceptanceTarget(connectionID, accountID, recordID string) Target {
	target := Target{Target: integrations.Target{ConnectionID: connectionID, AccountID: accountID, RecordID: recordID, Domain: "example.invalid", RR: "www", Type: "A", Line: "default", TTL: 600, Value: "192.0.2.10"}}
	target.TargetID = targetKey(accountID, recordID)
	return target
}

func acceptancePut(t *testing.T, fixture *acceptanceFixture, kind, id string, value any) {
	t.Helper()
	fixture.service.mu.Lock()
	defer fixture.service.mu.Unlock()
	if err := put(fixture.service.db, kind, id, value); err != nil {
		t.Fatalf("保存 %s/%s: %v", kind, id, err)
	}
}

func acceptanceGet[Value any](t *testing.T, fixture *acceptanceFixture, kind, id string) Value {
	t.Helper()
	fixture.service.mu.Lock()
	defer fixture.service.mu.Unlock()
	value, err := get[Value](fixture.service.db, kind, id)
	if err != nil {
		t.Fatalf("读取 %s/%s: %v", kind, id, err)
	}
	return value
}

func acceptanceAll[Value any](t *testing.T, fixture *acceptanceFixture, kind string) []Value {
	t.Helper()
	fixture.service.mu.Lock()
	defer fixture.service.mu.Unlock()
	values, err := all[Value](fixture.service.db, kind)
	if err != nil {
		t.Fatalf("读取 %s 全集: %v", kind, err)
	}
	return values
}

func acceptanceTx(t *testing.T, fixture *acceptanceFixture, change func(*sql.Tx) error) {
	t.Helper()
	fixture.service.mu.Lock()
	defer fixture.service.mu.Unlock()
	tx, err := fixture.service.db.Begin()
	if err != nil {
		t.Fatalf("开启验收事务: %v", err)
	}
	defer tx.Rollback()
	if err = change(tx); err != nil {
		t.Fatalf("验收事务失败: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("提交验收事务: %v", err)
	}
}

func acceptanceSaveTask(fixture *acceptanceFixture, task Task, id string, revision int) (Task, error) {
	fixture.service.mu.Lock()
	defer fixture.service.mu.Unlock()
	tx, err := fixture.service.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	_, result, err := fixture.service.saveTask(tx, task, id, revision)
	if err != nil {
		return Task{}, err
	}
	if err = tx.Commit(); err != nil {
		return Task{}, err
	}
	saved, ok := result.(Task)
	if !ok {
		return Task{}, fmt.Errorf("保存任务返回类型 %T", result)
	}
	return saved, nil
}

func acceptanceConnection(t *testing.T, fixture *acceptanceFixture, id, kind, accountID string) {
	t.Helper()
	fixture.service.mu.Lock()
	ciphertext, err := fixture.service.encrypt(map[string]string{"fake_secret": "FAKE_ONLY_NOT_A_CREDENTIAL_" + id})
	fixture.service.mu.Unlock()
	if err != nil {
		t.Fatalf("加密虚构连接凭据: %v", err)
	}
	acceptancePut(t, fixture, "connections", id, Connection{ID: id, Kind: kind, Name: "FAKE acceptance connection", AccountID: accountID, Revision: 1, CredentialsSet: true, Ciphertext: ciphertext})
}

func acceptanceEnqueue(t *testing.T, fixture *acceptanceFixture, task Task) Run {
	t.Helper()
	var run Run
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		var err error
		run, err = enqueue(tx, task, "manual", "", fixture.service)
		return err
	})
	return run
}

func acceptanceAPIError(t *testing.T, err error, status int) {
	t.Helper()
	var failure *apiError
	if !errors.As(err, &failure) || failure.Status != status {
		t.Fatalf("期望 API 状态 %d，实际错误 %v", status, err)
	}
}

type acceptanceHTTPClient struct {
	client *http.Client
	base   string
	csrf   string
}

type acceptanceLocalTransport struct {
	base      string
	transport *http.Transport
}

func (transport acceptanceLocalTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	address, err := netip.ParseAddr(request.URL.Hostname())
	if err != nil || !address.IsLoopback() || request.URL.Scheme+"://"+request.URL.Host != transport.base {
		return nil, fmt.Errorf("验收禁止访问非本地测试服务器: %s", request.URL.Host)
	}
	return transport.transport.RoundTrip(request)
}

func acceptanceLogin(t *testing.T, server *httptest.Server) acceptanceHTTPClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	base := server.URL
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second, Transport: acceptanceLocalTransport{base: base, transport: transport}}
	response, err := client.Get(base + "/api/v1/session")
	if err != nil {
		t.Fatalf("读取登录 CSRF: %v", err)
	}
	var initial struct {
		CSRF string `json:"csrf_token"`
	}
	if err = json.NewDecoder(response.Body).Decode(&initial); err != nil {
		response.Body.Close()
		t.Fatalf("解析登录 CSRF: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || initial.CSRF == "" {
		t.Fatalf("登录 CSRF 响应异常: %d %+v", response.StatusCode, initial)
	}
	body, _ := json.Marshal(map[string]string{"password": acceptancePassword})
	response = acceptanceDo(t, client, http.MethodPost, base+"/api/v1/session", body, initial.CSRF, base)
	var authenticated struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrf_token"`
	}
	if err = json.NewDecoder(response.Body).Decode(&authenticated); err != nil {
		response.Body.Close()
		t.Fatalf("解析登录响应: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !authenticated.Authenticated || authenticated.CSRF == "" {
		t.Fatalf("登录失败: %d %+v", response.StatusCode, authenticated)
	}
	return acceptanceHTTPClient{client: client, base: base, csrf: authenticated.CSRF}
}

func acceptanceDo(t *testing.T, client *http.Client, method, url string, body []byte, csrf, origin string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return response
}

func acceptanceStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != want {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("期望 HTTP %d，实际 %d: %s", want, response.StatusCode, body)
	}
}

func acceptanceWaitRun(t *testing.T, fixture *acceptanceFixture, runID, status string) Run {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		run := acceptanceGet[Run](t, fixture, "runs", runID)
		if run.Status == status {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	run := acceptanceGet[Run](t, fixture, "runs", runID)
	t.Fatalf("等待运行 %s 达到 %s 超时，实际状态=%s 阶段=%s", runID, status, run.Status, run.Stage)
	return run
}

func acceptanceWaitDelivery(t *testing.T, fixture *acceptanceFixture, runID, deliveryID, state string) Delivery {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		run := acceptanceGet[Run](t, fixture, "runs", runID)
		for _, delivery := range run.Deliveries {
			if delivery.ID == deliveryID && delivery.State == state {
				return delivery
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待投递 %s 达到 %s 超时: %+v", deliveryID, state, acceptanceGet[Run](t, fixture, "runs", runID).Deliveries)
	return Delivery{}
}

func acceptanceWake(fixture *acceptanceFixture) {
	select {
	case fixture.service.wake <- struct{}{}:
	default:
	}
}

func TestAcceptanceSharedSessionsLogoutIsScopedAndCSRFIsEnforced(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	server := httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	first := acceptanceLogin(t, server)
	second := acceptanceLogin(t, server)

	body, _ := json.Marshal(map[string]any{"timezone": "UTC", "history_days": 30, "operation_id": "acceptance-session-csrf-0001"})
	acceptanceStatus(t, acceptanceDo(t, second.client, http.MethodPatch, second.base+"/api/v1/settings", body, "", second.base), http.StatusForbidden)
	acceptanceStatus(t, acceptanceDo(t, second.client, http.MethodPatch, second.base+"/api/v1/settings", body, second.csrf, "http://attacker.invalid"), http.StatusForbidden)
	acceptanceStatus(t, acceptanceDo(t, second.client, http.MethodPatch, second.base+"/api/v1/settings", body, first.csrf, second.base), http.StatusForbidden)
	crossSite := httptest.NewRequest(http.MethodPatch, second.base+"/api/v1/settings", strings.NewReader(string(body)))
	crossSite.Header.Set("Content-Type", "application/json")
	crossSite.Header.Set("X-CSRF-Token", second.csrf)
	crossSite.Header.Set("Origin", second.base)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	for _, cookie := range second.client.Jar.Cookies(crossSite.URL) {
		crossSite.AddCookie(cookie)
	}
	crossSiteResponse := httptest.NewRecorder()
	fixture.service.Handler().ServeHTTP(crossSiteResponse, crossSite)
	if crossSiteResponse.Code != http.StatusForbidden {
		t.Fatalf("跨站 Fetch 请求应拒绝，得到 %d", crossSiteResponse.Code)
	}
	acceptanceStatus(t, acceptanceDo(t, second.client, http.MethodPatch, second.base+"/api/v1/settings", body, second.csrf, second.base), http.StatusAccepted)
	settings := acceptanceGet[Settings](t, fixture, "settings", "system")
	if settings.Timezone != "UTC" {
		t.Fatalf("共享会话的合法写操作未生效: %+v", settings)
	}

	acceptanceStatus(t, acceptanceDo(t, first.client, http.MethodDelete, first.base+"/api/v1/session", nil, first.csrf, first.base), http.StatusOK)
	firstSession := acceptanceDo(t, first.client, http.MethodGet, first.base+"/api/v1/settings", nil, "", "")
	acceptanceStatus(t, firstSession, http.StatusUnauthorized)
	secondSession := acceptanceDo(t, second.client, http.MethodGet, second.base+"/api/v1/settings", nil, "", "")
	acceptanceStatus(t, secondSession, http.StatusOK)
}

func TestAcceptanceLoginSourceCooldownAfterFiveFailures(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	handler := fixture.service.Handler()
	login := func(address, password string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"password": password})
		request := httptest.NewRequest(http.MethodPost, "http://management.test/api/v1/session", strings.NewReader(string(body)))
		request.Host = "management.test"
		request.RemoteAddr = address
		request.AddCookie(&http.Cookie{Name: "cfst_login_csrf", Value: "FAKE_login_csrf_for_test"})
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", "FAKE_login_csrf_for_test")
		request.Header.Set("Origin", "http://management.test")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for attempt := 1; attempt <= 5; attempt++ {
		response := login(fmt.Sprintf("198.51.100.31:%d", 12000+attempt), "FAKE_wrong_acceptance_password")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应为401，得到 %d", attempt, response.Code)
		}
	}
	locked := login("198.51.100.31:12999", acceptancePassword)
	if locked.Code != http.StatusTooManyRequests || locked.Header().Get("Retry-After") != "900" {
		t.Fatalf("同一来源五次失败后应冷却15分钟，状态=%d Retry-After=%q", locked.Code, locked.Header().Get("Retry-After"))
	}
	otherSource := login("203.0.113.44:12000", acceptancePassword)
	if otherSource.Code != http.StatusOK {
		t.Fatalf("其他来源不应继承冷却状态，得到 %d", otherSource.Code)
	}
	fixture.clock.Set(acceptanceNow().Add(15*time.Minute - time.Second))
	if response := login("198.51.100.31:13000", acceptancePassword); response.Code != http.StatusTooManyRequests {
		t.Fatalf("冷却期结束前不得放行，得到 %d", response.Code)
	}
	fixture.clock.Set(acceptanceNow().Add(15 * time.Minute))
	if response := login("198.51.100.31:13001", acceptancePassword); response.Code != http.StatusOK {
		t.Fatalf("冷却期结束后正确密码应可登录，得到 %d", response.Code)
	}
}

func TestAcceptanceC01StableOperationIdempotencyConflictAndRestartReceipt(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	server := httptest.NewServer(fixture.service.Handler())
	t.Cleanup(server.Close)
	client := acceptanceLogin(t, server)
	task := acceptanceTask("FAKE operation task")
	requestBody, err := json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Task        Task   `json:"task"`
	}{"acceptance-c01-fixed-operation-0001", task})
	if err != nil {
		t.Fatal(err)
	}
	post := func(active acceptanceHTTPClient, body []byte) *http.Response {
		return acceptanceDo(t, active.client, http.MethodPost, active.base+"/api/v1/tasks", body, active.csrf, active.base)
	}
	firstResponse := post(client, requestBody)
	var first Operation
	if err = json.NewDecoder(firstResponse.Body).Decode(&first); err != nil {
		firstResponse.Body.Close()
		t.Fatal(err)
	}
	firstResponse.Body.Close()
	if firstResponse.StatusCode != http.StatusAccepted || first.ID != "acceptance-c01-fixed-operation-0001" {
		t.Fatalf("首次 C01 操作未受理: status=%d receipt=%+v", firstResponse.StatusCode, first)
	}
	duplicateResponse := post(client, requestBody)
	var duplicate Operation
	if err = json.NewDecoder(duplicateResponse.Body).Decode(&duplicate); err != nil {
		duplicateResponse.Body.Close()
		t.Fatal(err)
	}
	duplicateResponse.Body.Close()
	if duplicateResponse.StatusCode != http.StatusAccepted || duplicate.ObjectID != first.ObjectID {
		t.Fatalf("相同操作号和参数未返回原收据: status=%d receipt=%+v original=%+v", duplicateResponse.StatusCode, duplicate, first)
	}
	if tasks := acceptanceAll[Task](t, fixture, "tasks"); len(tasks) != 1 {
		t.Fatalf("同号重放重复创建任务，数量=%d", len(tasks))
	}

	task.Name = "FAKE changed request"
	differentBody, _ := json.Marshal(struct {
		OperationID string `json:"operation_id"`
		Task        Task   `json:"task"`
	}{"acceptance-c01-fixed-operation-0001", task})
	acceptanceStatus(t, post(client, differentBody), http.StatusConflict)
	server.Close()
	fixture.reopen(t)
	server = httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	client = acceptanceLogin(t, server)
	restartedResponse := post(client, requestBody)
	var restarted Operation
	if err = json.NewDecoder(restartedResponse.Body).Decode(&restarted); err != nil {
		restartedResponse.Body.Close()
		t.Fatal(err)
	}
	restartedResponse.Body.Close()
	if restartedResponse.StatusCode != http.StatusAccepted || restarted.ObjectID != first.ObjectID {
		t.Fatalf("重启后未返回持久化收据: status=%d receipt=%+v", restartedResponse.StatusCode, restarted)
	}
	if tasks := acceptanceAll[Task](t, fixture, "tasks"); len(tasks) != 1 {
		t.Fatalf("重启后同号重放重复创建任务，数量=%d", len(tasks))
	}
}

func TestAcceptanceRunSnapshotAndSameTaskCannotQueueTwice(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	task := acceptanceTask("FAKE snapshot task")
	task.ID = "fake-snapshot-task"
	task.Config.SampleSize = 211
	acceptancePut(t, fixture, "tasks", task.ID, task)
	run := acceptanceEnqueue(t, fixture, task)
	fixture.service.mu.Lock()
	tx, err := fixture.service.db.Begin()
	if err != nil {
		fixture.service.mu.Unlock()
		t.Fatal(err)
	}
	_, duplicateErr := enqueue(tx, task, "manual", "", fixture.service)
	_ = tx.Rollback()
	fixture.service.mu.Unlock()
	acceptanceAPIError(t, duplicateErr, http.StatusConflict)

	task.Name = "FAKE edited after enqueue"
	task.Config.SampleSize = 999
	acceptancePut(t, fixture, "tasks", task.ID, task)
	stored := acceptanceGet[Run](t, fixture, "runs", run.ID)
	if stored.Snapshot.Name != "FAKE snapshot task" || stored.Snapshot.Config.SampleSize != 211 {
		t.Fatalf("运行快照被后续任务编辑污染: %+v", stored.Snapshot)
	}
	stored.Status = "cancelled"
	stored.SlotBusy = false
	acceptancePut(t, fixture, "runs", stored.ID, stored)
}

func TestAcceptanceGlobalFIFOSlotIncludesDNSAndNotificationTail(t *testing.T) {
	engineStarted := make(chan int, 4)
	engine := &acceptanceEngine{run: func(_ context.Context, config optimizer.Config, _ func(optimizer.Progress)) (optimizer.Result, error) {
		engineStarted <- config.SampleSize
		return optimizer.Result{Status: "completed", Candidates: []optimizer.Candidate{{IP: "192.0.2.42", Family: "ipv4", Eligible: true, Rank: 1}}}, nil
	}}
	updateStarted := make(chan struct{}, 1)
	notifyStarted := make(chan struct{}, 1)
	releaseUpdate := make(chan struct{})
	releaseNotify := make(chan struct{})
	provider := newAcceptanceProvider()
	provider.update = func(ctx context.Context, _ integrations.Connection, _ integrations.Target, _ string) integrations.Outcome {
		updateStarted <- struct{}{}
		select {
		case <-releaseUpdate:
			return integrations.Outcome{State: "success", Verified: true}
		case <-ctx.Done():
			return integrations.Outcome{State: "unconfirmed", Reason: "FAKE canceled update"}
		}
	}
	provider.notify = func(ctx context.Context, _ integrations.Connection, _ string) integrations.Outcome {
		notifyStarted <- struct{}{}
		select {
		case <-releaseNotify:
			return integrations.Outcome{State: "success", Verified: true}
		case <-ctx.Done():
			return integrations.Outcome{State: "failed", Reason: "FAKE canceled notification"}
		}
	}
	fixture := newAcceptanceFixture(t, acceptanceNow(), engine, provider)
	acceptanceConnection(t, fixture, "fake-dns-connection", "alidns", "fake-account-001")
	acceptanceConnection(t, fixture, "fake-notify-connection", "wecom", "")
	firstTask := acceptanceTask("FAKE first queued task")
	firstTask.ID = "fake-first-task"
	firstTask.Config.SampleSize = 101
	firstTask.DNSAuto = true
	firstTask.Targets = []Target{acceptanceTarget("fake-dns-connection", "fake-account-001", "fake-record-001")}
	firstTask.Notifications.Wecom = Channel{Enabled: true, ConnectionID: "fake-notify-connection"}
	secondTask := acceptanceTask("FAKE second queued task")
	secondTask.ID = "fake-second-task"
	secondTask.Config.SampleSize = 202
	firstRun := acceptanceEnqueue(t, fixture, firstTask)
	secondRun := acceptanceEnqueue(t, fixture, secondTask)
	select {
	case got := <-engineStarted:
		if got != 101 {
			t.Fatalf("FIFO 首个运行错误，SampleSize=%d", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("首个排队运行未开始")
	}
	acceptanceWake(fixture)
	select {
	case <-updateStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("DNS 自动收尾未开始")
	}
	firstDuringDNS := acceptanceGet[Run](t, fixture, "runs", firstRun.ID)
	secondDuringDNS := acceptanceGet[Run](t, fixture, "runs", secondRun.ID)
	if !firstDuringDNS.SlotBusy || secondDuringDNS.Status != "queued" {
		t.Fatalf("DNS 收尾期间应保留全局槽: first=%+v second=%+v", firstDuringDNS, secondDuringDNS)
	}
	select {
	case got := <-engineStarted:
		t.Fatalf("DNS 收尾未完成就启动 FIFO 后项，SampleSize=%d", got)
	case <-time.After(80 * time.Millisecond):
	}
	close(releaseUpdate)
	select {
	case <-notifyStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("自动通知收尾未开始")
	}
	firstDuringNotify := acceptanceGet[Run](t, fixture, "runs", firstRun.ID)
	secondDuringNotify := acceptanceGet[Run](t, fixture, "runs", secondRun.ID)
	if !firstDuringNotify.SlotBusy || secondDuringNotify.Status != "queued" {
		t.Fatalf("通知收尾期间应保留全局槽: first=%+v second=%+v", firstDuringNotify, secondDuringNotify)
	}
	select {
	case got := <-engineStarted:
		t.Fatalf("通知收尾未完成就启动 FIFO 后项，SampleSize=%d", got)
	case <-time.After(80 * time.Millisecond):
	}
	close(releaseNotify)
	select {
	case got := <-engineStarted:
		if got != 202 {
			t.Fatalf("FIFO 后项配置错误，SampleSize=%d", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("前项 DNS／通知收尾完成后未启动后项")
	}
	acceptanceWaitRun(t, fixture, firstRun.ID, "completed")
	acceptanceWaitRun(t, fixture, secondRun.ID, "completed")
}

func TestAcceptanceCancellationAndRestartRecoveryNeverReplayExternalWork(t *testing.T) {
	engineCalls := 0
	engine := &acceptanceEngine{run: func(context.Context, optimizer.Config, func(optimizer.Progress)) (optimizer.Result, error) {
		engineCalls++
		return optimizer.Result{Status: "completed"}, nil
	}}
	provider := newAcceptanceProvider()
	fixture := newAcceptanceFixture(t, acceptanceNow(), engine, provider)
	fixture.service.cancel()
	fixture.service.wg.Wait()
	target := acceptanceTarget("fake-dns-connection", "fake-account-001", "fake-record-recovery")
	queued := Run{ID: "fake-cancel-queued", TaskID: "fake-task", Status: "queued", Stage: "queued", QueuedAt: acceptanceNow(), Snapshot: acceptanceTask("FAKE queued"), Deliveries: []Delivery{}}
	inFlight := Run{ID: "fake-recovery-running", TaskID: "fake-task", Status: "running", Stage: "dns", SlotBusy: true, QueuedAt: acceptanceNow(), Snapshot: acceptanceTask("FAKE in flight"), Deliveries: []Delivery{{ID: "fake-running-dns-attempt", Kind: "dns", ConnectionID: target.ConnectionID, Target: &target, IP: "192.0.2.20", State: "running"}, {ID: "fake-pending-notify-attempt", Kind: "telegram", ConnectionID: "fake-notify", State: "pending"}}}
	acceptancePut(t, fixture, "runs", queued.ID, queued)
	acceptancePut(t, fixture, "runs", inFlight.ID, inFlight)
	acceptancePut(t, fixture, "operations", "fake-receipt-to-keep", Operation{ID: "fake-receipt-to-keep", State: "completed", CreatedAt: acceptanceNow(), Action: "fake action"})
	acceptancePut(t, fixture, "logs", "fake-independent-log", Log{ID: "fake-independent-log", At: acceptanceNow(), Actor: "系统", Action: "fake prior action", Object: "fake object", Result: "accepted"})
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		_, _, err := fixture.service.cancelRun(tx, queued.ID)
		return err
	})
	cancelled := acceptanceGet[Run](t, fixture, "runs", queued.ID)
	if cancelled.Status != "cancelled" || cancelled.SlotBusy || cancelled.StartedAt != nil {
		t.Fatalf("取消排队任务不应占用运行槽: %+v", cancelled)
	}
	fixture.reopen(t)
	recovered := acceptanceGet[Run](t, fixture, "runs", inFlight.ID)
	if recovered.Status != "interrupted" || recovered.SlotBusy {
		t.Fatalf("重启未回收运行席位: %+v", recovered)
	}
	if recovered.Deliveries[0].State != "unconfirmed" || recovered.Deliveries[1].State != "skipped" {
		t.Fatalf("重启应保留未确认事实且跳过未发起投递: %+v", recovered.Deliveries)
	}
	protection := acceptanceGet[Protection](t, fixture, "dns_protection", targetKey(target.AccountID, target.RecordID))
	if !protection.Active || protection.AttemptID != "fake-running-dns-attempt" {
		t.Fatalf("运行中 DNS 在途操作重启后应建立保护: %+v", protection)
	}
	receipt := acceptanceGet[Operation](t, fixture, "operations", "fake-receipt-to-keep")
	if receipt.State != "completed" {
		t.Fatalf("重启恢复不应删除操作收据: %+v", receipt)
	}
	entry := acceptanceGet[Log](t, fixture, "logs", "fake-independent-log")
	if entry.Object != "fake object" {
		t.Fatalf("重启恢复不应删除独立操作日志: %+v", entry)
	}
	updates, notifications := provider.callCounts()
	if updates != 0 || notifications != 0 || engineCalls != 0 {
		t.Fatalf("恢复过程重放了旧外部工作: engine=%d DNS=%d 通知=%d", engineCalls, updates, notifications)
	}
}

func TestAcceptanceC02ProtectionAliasesRestartSkipRetryAndVersionedClear(t *testing.T) {
	provider := newAcceptanceProvider()
	provider.update = func(context.Context, integrations.Connection, integrations.Target, string) integrations.Outcome {
		if updates, _ := provider.callCounts(); updates == 1 {
			return integrations.Outcome{State: "unconfirmed", Reason: "FAKE timeout; remote write may still arrive"}
		}
		return integrations.Outcome{State: "success", Verified: true}
	}
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, provider)
	acceptanceConnection(t, fixture, "fake-alias-a", "alidns", "fake-account-shared")
	acceptanceConnection(t, fixture, "fake-alias-b", "alidns", "fake-account-shared")
	targetA := acceptanceTarget("fake-alias-a", "fake-account-shared", "fake-record-shared")
	targetB := acceptanceTarget("fake-alias-b", "fake-account-shared", "fake-record-shared")
	acceptancePut(t, fixture, "target_catalog", targetB.TargetID, targetB)
	original := Run{ID: "fake-original-unconfirmed-run", TaskID: "fake-original-task", Status: "completed", Deliveries: []Delivery{{ID: "fake-original-attempt", Kind: "dns", ConnectionID: targetA.ConnectionID, Target: &targetA, IP: "192.0.2.76", State: "pending", Automatic: true}}}
	acceptancePut(t, fixture, "runs", original.ID, original)
	fixture.service.performDelivery(fixture.service.ctx, original.ID, "fake-original-attempt")
	originalResult := acceptanceGet[Run](t, fixture, "runs", original.ID)
	if originalResult.Deliveries[0].State != "unconfirmed" {
		t.Fatalf("DNS 未确认结论必须保留: %+v", originalResult.Deliveries)
	}
	fixture.reopen(t)
	metadata := targetB
	fixture.service.mu.Lock()
	err := fixture.service.appendTargetMetadata(fixture.service.db, &metadata)
	fixture.service.mu.Unlock()
	if err != nil || metadata.Protection == nil || !metadata.Protection.Active || metadata.Protection.AttemptID != "fake-original-attempt" {
		t.Fatalf("同账号同记录的别名未命中持久保护: metadata=%+v err=%v", metadata, err)
	}

	run := Run{ID: "fake-protected-run", TaskID: "fake-task", TaskName: "FAKE protected", Status: "completed", Snapshot: acceptanceTask("FAKE protected"), Result: optimizer.Result{Candidates: []optimizer.Candidate{{IP: "192.0.2.77", Family: "ipv4", Eligible: true, Rank: 1}}}, Deliveries: []Delivery{{ID: "fake-auto-delivery", Kind: "dns", ConnectionID: targetB.ConnectionID, Target: &targetB, IP: "192.0.2.77", State: "pending", Automatic: true}}}
	acceptancePut(t, fixture, "runs", run.ID, run)
	fixture.service.performDelivery(fixture.service.ctx, run.ID, "fake-auto-delivery")
	afterSkip := acceptanceGet[Run](t, fixture, "runs", run.ID)
	if afterSkip.Deliveries[0].State != "skipped" || !strings.Contains(afterSkip.Deliveries[0].Reason, "保护") {
		t.Fatalf("保护目标的自动写入应有原因地跳过: %+v", afterSkip.Deliveries[0])
	}
	if updates, _ := provider.callCounts(); updates != 1 {
		t.Fatalf("自动保护跳过仍调用 DNS 写入，次数=%d", updates)
	}

	manual := []dnsItem{{Target: targetB, IP: "192.0.2.77"}}
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		_, _, err := fixture.service.manualDNS(tx, run.ID, "fake-manual-retry", manual, true)
		return err
	})
	updated := acceptanceGet[Run](t, fixture, "runs", run.ID)
	manualDelivery := updated.Deliveries[len(updated.Deliveries)-1]
	acceptanceWake(fixture)
	acceptanceWaitDelivery(t, fixture, run.ID, manualDelivery.ID, "success")
	afterManual := acceptanceGet[Run](t, fixture, "runs", run.ID)
	if afterManual.Deliveries[len(afterManual.Deliveries)-1].State != "success" {
		t.Fatalf("显式人工风险重试未执行: %+v", afterManual.Deliveries[len(afterManual.Deliveries)-1])
	}
	protection := acceptanceGet[Protection](t, fixture, "dns_protection", targetB.TargetID)
	if !protection.Active || protection.Revision != 1 {
		t.Fatalf("人工重试成功不得自动解除保护: %+v", protection)
	}
	fixture.service.mu.Lock()
	err = fixture.service.protect(fixture.service.db, targetA, "fake-newer-unconfirmed-attempt", "FAKE newer unconfirmed write")
	fixture.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.mu.Lock()
	tx, err := fixture.service.db.Begin()
	if err == nil {
		_, _, err = fixture.service.clearProtection(tx, targetB.TargetID, protection.Revision, true)
		_ = tx.Rollback()
	}
	fixture.service.mu.Unlock()
	acceptanceAPIError(t, err, http.StatusConflict)
	newerProtection := acceptanceGet[Protection](t, fixture, "dns_protection", targetB.TargetID)
	if !newerProtection.Active || newerProtection.AttemptID != "fake-newer-unconfirmed-attempt" || newerProtection.Revision != protection.Revision+1 {
		t.Fatalf("旧版本解除不得清掉更新的保护事实: %+v", newerProtection)
	}
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		_, _, err := fixture.service.clearProtection(tx, targetB.TargetID, newerProtection.Revision, true)
		return err
	})
	cleared := acceptanceGet[Protection](t, fixture, "dns_protection", targetB.TargetID)
	if cleared.Active || cleared.Revision != newerProtection.Revision+1 {
		t.Fatalf("按当前版本解除失败: %+v", cleared)
	}
	if updates, _ := provider.callCounts(); updates != 2 {
		t.Fatalf("自动跳过或解除保护不应补发；仅人工重试一次，写入次数=%d", updates)
	}
}

func TestAcceptanceDNSLastWriteTracksMostRecentConfirmedWrite(t *testing.T) {
	provider := newAcceptanceProvider()
	provider.update = func(context.Context, integrations.Connection, integrations.Target, string) integrations.Outcome {
		return integrations.Outcome{State: "success", Verified: true}
	}
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, provider)
	acceptanceConnection(t, fixture, "fake-dns-latest", "alidns", "fake-account-latest")
	target := acceptanceTarget("fake-dns-latest", "fake-account-latest", "fake-record-latest")
	for index, item := range []struct {
		id string
		ip string
	}{{"fake-first-write", "192.0.2.31"}, {"fake-second-write", "192.0.2.32"}} {
		fixture.clock.Set(acceptanceNow().Add(time.Duration(index) * time.Hour))
		run := Run{ID: item.id, TaskID: "fake-task", Status: "completed", Snapshot: acceptanceTask("FAKE latest write"), Deliveries: []Delivery{{ID: item.id + "-delivery", Kind: "dns", ConnectionID: target.ConnectionID, Target: &target, IP: item.ip, State: "pending", Automatic: true}}}
		acceptancePut(t, fixture, "runs", run.ID, run)
		fixture.service.performDelivery(fixture.service.ctx, run.ID, run.Deliveries[0].ID)
	}
	metadata := target
	fixture.service.mu.Lock()
	err := fixture.service.appendTargetMetadata(fixture.service.db, &metadata)
	fixture.service.mu.Unlock()
	if err != nil || metadata.LastKnownWrite == nil || metadata.LastKnownWrite.RunID != "fake-second-write" || metadata.LastKnownWrite.IP != "192.0.2.32" || !metadata.LastKnownWrite.WrittenAt.Equal(acceptanceNow().Add(time.Hour)) {
		t.Fatalf("最近确认写入未覆盖旧事实: last=%+v err=%v", metadata.LastKnownWrite, err)
	}
}

func TestAcceptanceManualSummaryAndFailedChannelRetryKeepSeparateAttempts(t *testing.T) {
	provider := newAcceptanceProvider()
	provider.notify = func(_ context.Context, _ integrations.Connection, message string) integrations.Outcome {
		if len(provider.notificationMessages()) == 1 {
			return integrations.Outcome{State: "failed", Reason: "FAKE notification failure"}
		}
		return integrations.Outcome{State: "success", Verified: true}
	}
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, provider)
	acceptanceConnection(t, fixture, "fake-telegram-channel", "telegram", "")
	run := Run{ID: "fake-summary-run", TaskID: "fake-summary-task", TaskName: "FAKE summary task", Status: "completed", Snapshot: acceptanceTask("FAKE summary task"), Result: optimizer.Result{Status: "completed", Candidates: []optimizer.Candidate{{IP: "192.0.2.80", Family: "ipv4", Eligible: true}}}, Deliveries: []Delivery{}}
	acceptancePut(t, fixture, "runs", run.ID, run)
	channels := []notificationItem{{Kind: "telegram", ConnectionID: "fake-telegram-channel"}}
	fixture.service.mu.Lock()
	tx, err := fixture.service.db.Begin()
	if err != nil {
		fixture.service.mu.Unlock()
		t.Fatal(err)
	}
	_, _, riskErr := fixture.service.manualNotifications(tx, run.ID, channels, false)
	_ = tx.Rollback()
	fixture.service.mu.Unlock()
	acceptanceAPIError(t, riskErr, http.StatusBadRequest)

	firstResult := acceptanceRunManualNotification(t, fixture, run.ID, channels, true)
	firstID := firstResult[0]
	acceptanceWake(fixture)
	acceptanceWaitDelivery(t, fixture, run.ID, firstID, "failed")
	afterFirst := acceptanceGet[Run](t, fixture, "runs", run.ID)
	if afterFirst.Deliveries[0].State != "failed" {
		t.Fatalf("首次手动摘要失败事实未保留: %+v", afterFirst.Deliveries[0])
	}
	messages := provider.notificationMessages()
	if len(messages) != 1 || !strings.Contains(messages[0], run.ID) || !strings.Contains(messages[0], run.TaskName) || !strings.Contains(messages[0], "IPv4 合格 1") {
		t.Fatalf("首次手动摘要内容不完整: %q", messages)
	}
	retry := []notificationItem{{Kind: "telegram", ConnectionID: "fake-telegram-channel", PriorAttemptID: firstID}}
	retryResult := acceptanceRunManualNotification(t, fixture, run.ID, retry, true)
	if len(retryResult) != 1 {
		t.Fatalf("手动重发未创建独立尝试: %v", retryResult)
	}
	acceptanceWake(fixture)
	acceptanceWaitDelivery(t, fixture, run.ID, retryResult[0], "success")
	afterRetry := acceptanceGet[Run](t, fixture, "runs", run.ID)
	if len(afterRetry.Deliveries) != 2 || afterRetry.Deliveries[0].State != "failed" || afterRetry.Deliveries[1].State != "success" || afterRetry.Deliveries[1].PriorAttemptID != firstID {
		t.Fatalf("渠道重发覆盖原失败事实或关联丢失: %+v", afterRetry.Deliveries)
	}
}

func acceptanceRunManualNotification(t *testing.T, fixture *acceptanceFixture, runID string, channels []notificationItem, risk bool) []string {
	t.Helper()
	var ids []string
	fixture.service.mu.Lock()
	tx, err := fixture.service.db.Begin()
	if err != nil {
		fixture.service.mu.Unlock()
		t.Fatal(err)
	}
	_, result, err := fixture.service.manualNotifications(tx, runID, channels, risk)
	if err != nil {
		_ = tx.Rollback()
		fixture.service.mu.Unlock()
		t.Fatalf("创建手动通知尝试: %v", err)
	}
	if err = tx.Commit(); err != nil {
		fixture.service.mu.Unlock()
		t.Fatal(err)
	}
	fixture.service.mu.Unlock()
	response, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("手动通知结果类型异常: %T", result)
	}
	values, ok := response["delivery_ids"].([]string)
	if !ok {
		t.Fatalf("手动通知尝试标识异常: %T", response["delivery_ids"])
	}
	ids = append(ids, values...)
	return ids
}

func TestAcceptanceArchiveRestoreLeavesScheduleDisabled(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	task := acceptanceTask("FAKE archivable task")
	task.Schedule = Schedule{Enabled: true, Kind: "interval", IntervalMinutes: 90}
	saved, err := acceptanceSaveTask(fixture, task, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		_, _, err := fixture.service.taskState(tx, saved.ID, "archive", false)
		return err
	})
	archived := acceptanceGet[Task](t, fixture, "tasks", saved.ID)
	if !archived.Archived || archived.Schedule.Enabled || archived.Schedule.NextRunAt != nil {
		t.Fatalf("归档任务未关闭定时: %+v", archived)
	}
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		_, _, err := fixture.service.taskState(tx, saved.ID, "restore", false)
		return err
	})
	restored := acceptanceGet[Task](t, fixture, "tasks", saved.ID)
	if restored.Archived || restored.Schedule.Enabled || restored.Schedule.NextRunAt != nil {
		t.Fatalf("恢复任务应保持定时关闭: %+v", restored)
	}
}

func TestAcceptanceFiveFieldCronAndChangesRecomputeNextRun(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	task := acceptanceTask("FAKE cron task")
	task.Schedule = Schedule{Enabled: true, Kind: "cron", Expression: "0 9 * * *"}
	saved, err := acceptanceSaveTask(fixture, task, "", 0)
	if err != nil {
		t.Fatalf("五字段 Cron 应可保存: %v", err)
	}
	firstNext := *saved.Schedule.NextRunAt
	if !firstNext.Equal(time.Date(2026, time.November, 14, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("首次 Cron 下次执行时间错误: %s", firstNext)
	}
	invalid := saved
	invalid.Schedule.Expression = "0 0 9 * * *"
	_, err = acceptanceSaveTask(fixture, invalid, saved.ID, saved.Revision)
	acceptanceAPIError(t, err, http.StatusBadRequest)

	changed := saved
	changed.Schedule.Expression = "30 8 * * *"
	updated, err := acceptanceSaveTask(fixture, changed, saved.ID, saved.Revision)
	if err != nil {
		t.Fatalf("Cron 变更失败: %v", err)
	}
	if updated.Schedule.NextRunAt == nil || !updated.Schedule.NextRunAt.Equal(time.Date(2026, time.November, 14, 0, 30, 0, 0, time.UTC)) || !updated.Schedule.NextRunAt.After(fixture.clock.Now()) {
		t.Fatalf("Cron 变更未重算为严格未来时点: %+v", updated.Schedule.NextRunAt)
	}
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		_, _, err := fixture.service.updateSettings(tx, Settings{Timezone: "UTC", HistoryDays: 30})
		return err
	})
	afterTimezone := acceptanceGet[Task](t, fixture, "tasks", saved.ID)
	if afterTimezone.Revision != updated.Revision+1 || afterTimezone.Schedule.NextRunAt == nil || !afterTimezone.Schedule.NextRunAt.Equal(time.Date(2026, time.November, 14, 8, 30, 0, 0, time.UTC)) {
		t.Fatalf("系统时区变更未更新任务版本和下次执行时间: %+v", afterTimezone)
	}
}

func TestAcceptanceHistoryRecycleRestoreAndOperationLogRetentionAreIndependent(t *testing.T) {
	base := acceptanceNow()
	fixture := newAcceptanceFixture(t, base, nil, nil)
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		if err := put(tx, "settings", "system", Settings{Timezone: "Asia/Shanghai", HistoryDays: 1}); err != nil {
			return err
		}
		ended := base.Add(-48 * time.Hour)
		return put(tx, "runs", "fake-expiring-run", Run{ID: "fake-expiring-run", TaskID: "fake-task", Status: "completed", EndedAt: &ended, RetentionAt: &ended, QueuedAt: ended, Deliveries: []Delivery{}})
	})
	acceptancePut(t, fixture, "logs", "fake-retained-operation-log", Log{ID: "fake-retained-operation-log", At: base.Add(-48 * time.Hour), Actor: "共享管理身份", Action: "fake_operation", Object: "fake-expiring-run", Result: "accepted"})
	fixture.service.mu.Lock()
	err := fixture.service.cleanup()
	fixture.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	recycled := acceptanceGet[Run](t, fixture, "runs", "fake-expiring-run")
	if recycled.DeletedAt == nil {
		t.Fatal("超期运行未进入回收站")
	}
	acceptanceTx(t, fixture, func(tx *sql.Tx) error {
		_, _, err := fixture.service.historyAction(tx, recycled.ID, true)
		return err
	})
	restored := acceptanceGet[Run](t, fixture, "runs", recycled.ID)
	if restored.DeletedAt != nil || restored.RetentionAt == nil || !restored.RetentionAt.Equal(base) {
		t.Fatalf("回收记录恢复状态错误: %+v", restored)
	}
	fixture.clock.Set(base.Add(2 * 24 * time.Hour))
	fixture.service.mu.Lock()
	err = fixture.service.cleanup()
	fixture.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(base.Add(10 * 24 * time.Hour))
	fixture.service.mu.Lock()
	err = fixture.service.cleanup()
	fixture.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.mu.Lock()
	_, runErr := get[Run](fixture.service.db, "runs", recycled.ID)
	log, logErr := get[Log](fixture.service.db, "logs", "fake-retained-operation-log")
	fixture.service.mu.Unlock()
	if !errors.Is(runErr, errNotFound) {
		t.Fatalf("超过回收期的运行记录应清理，错误=%v", runErr)
	}
	if logErr != nil || log.ID != "fake-retained-operation-log" {
		t.Fatalf("运行清理不应提前清除独立保留期内的操作日志: log=%+v err=%v", log, logErr)
	}
}
