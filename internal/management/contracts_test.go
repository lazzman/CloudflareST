package management

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/masgzy/CloudflareST/internal/integrations"
	"github.com/masgzy/CloudflareST/internal/optimizer"
)

func TestContractForwardedSourceOnlyTrustsConfiguredChain(t *testing.T) {
	service := &Service{trustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	for _, test := range []struct{ peer, forwarded, want string }{
		{"198.51.100.3:4500", "203.0.113.8", "198.51.100.3"},
		{"10.2.3.4:4500", "203.0.113.8", "203.0.113.8"},
		{"10.2.3.4:4500", "192.0.2.99, 203.0.113.8", "203.0.113.8"},
		{"10.2.3.4:4500", "203.0.113.8, 10.1.2.3", "203.0.113.8"},
		{"10.2.3.4:4500", "malformed", "10.2.3.4"},
		{"10.2.3.4:4500", "203.0.113.9", "203.0.113.9"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = test.peer
		request.Header.Set("X-Forwarded-For", test.forwarded)
		if got := service.loginSource(request); got != test.want {
			t.Fatalf("代理链 %q %q 得到 %s，预期 %s", test.peer, test.forwarded, got, test.want)
		}
	}
}

func TestContractDataDirectoryPermissions(test *testing.T) {
	if runtime.GOOS == "windows" {
		test.Skip("Windows 不使用 POSIX 权限位")
	}
	for _, scenario := range []struct {
		name     string
		existing bool
		mode     os.FileMode
	}{
		{"保留已有目录权限", true, 0770},
		{"新建私有目录", false, 0700},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			dataDir := filepath.Join(test.TempDir(), "data")
			if scenario.existing {
				if err := os.Mkdir(dataDir, scenario.mode); err != nil {
					test.Fatal(err)
				}
				if err := os.Chmod(dataDir, scenario.mode); err != nil {
					test.Fatal(err)
				}
			}
			service, err := Open(Config{DataDir: dataDir, Password: acceptancePassword, Engine: &acceptanceEngine{}, Provider: newAcceptanceProvider()})
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() {
				if err := service.Close(); err != nil {
					test.Error(err)
				}
			})
			for filename, mode := range map[string]os.FileMode{dataDir: scenario.mode, filepath.Join(dataDir, "master.key"): 0600, filepath.Join(dataDir, "management.sqlite"): 0600} {
				info, err := os.Stat(filename)
				if err != nil {
					test.Fatal(err)
				}
				if info.Mode().Perm() != mode {
					test.Errorf("%s 权限为 %04o，期望 %04o", filename, info.Mode().Perm(), mode)
				}
			}
		})
	}
}

func TestContractRandomPasswordIsEphemeralAndRotates(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	config := Config{DataDir: dir, RuntimeDir: runtimeDir, Engine: &acceptanceEngine{}, Provider: newAcceptanceProvider()}
	first, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if first != nil {
			_ = first.Close()
		}
	})
	firstFile := first.PasswordFile()
	password, err := os.ReadFile(firstFile)
	if err != nil {
		t.Fatal(err)
	}
	for filename, mode := range map[string]os.FileMode{runtimeDir: 0700, firstFile: 0600, filepath.Join(dir, "master.key"): 0600} {
		info, err := os.Stat(filename)
		if err != nil {
			t.Fatalf("无法读取受保护路径 %s：%v", filename, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != mode {
			t.Fatalf("权限不符 %s：得到 %04o，期望 %04o", filename, info.Mode().Perm(), mode)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "admin-password")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("随机密码不应进入业务数据目录")
	}
	if duplicate, err := Open(config); err == nil {
		_ = duplicate.Close()
		t.Fatal("同一卷不得由两个实例获取执行权")
	}
	err = first.Close()
	first = nil
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("关闭后临时密码文件未清理")
	}
	second, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	changed, err := os.ReadFile(second.PasswordFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(password) == string(changed) {
		t.Fatal("随机密码必须每次启动更新")
	}
}

func TestContractAcceptedReceiptsSettleAfterDeliveryConclusions(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	fixture.service.cancel()
	fixture.service.wg.Wait()
	run := Run{ID: "contract-run", Status: "running", SlotBusy: true, Deliveries: []Delivery{{ID: "contract-delivery", OperationID: "contract-delivery-op", State: "running"}}}
	acceptancePut(t, fixture, "runs", run.ID, run)
	for id, action := range map[string]string{"contract-run-op": "POST /api/v1/tasks/task/runs", "contract-delivery-op": "POST /api/v1/runs/contract-run/notifications"} {
		acceptancePut(t, fixture, "operations", id, Operation{ID: id, State: "accepted", ObjectID: run.ID, Action: action, CreatedAt: acceptanceNow()})
	}
	if err := fixture.service.settleOperations(); err != nil {
		t.Fatal(err)
	}
	if value := acceptanceGet[Operation](t, fixture, "operations", "contract-run-op"); value.State != "accepted" {
		t.Fatal("收尾中不能提前定稿")
	}
	run.Status = "completed"
	run.SlotBusy = false
	run.Deliveries[0].State = "unconfirmed"
	acceptancePut(t, fixture, "runs", run.ID, run)
	if err := fixture.service.settleOperations(); err != nil {
		t.Fatal(err)
	}
	if value := acceptanceGet[Operation](t, fixture, "operations", "contract-run-op"); value.State != "completed" {
		t.Fatal("有未确认投递结论不应无限占席位")
	}
	if value := acceptanceGet[Operation](t, fixture, "operations", "contract-delivery-op"); value.State != "unconfirmed" {
		t.Fatal("必须保留投递未确认事实")
	}
}

func TestContractRestartDoesNotInventActualEndTime(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	fixture.service.cancel()
	fixture.service.wg.Wait()
	run := Run{ID: "contract-interrupted", Status: "running", SlotBusy: true, Deliveries: []Delivery{}}
	acceptancePut(t, fixture, "runs", run.ID, run)
	fixture.reopen(t)
	recovered := acceptanceGet[Run](t, fixture, "runs", run.ID)
	if recovered.Status != "interrupted" || recovered.EndedAt != nil || recovered.RetentionAt == nil {
		t.Fatal("异常重启应记中断与保留锚点，不得伪造实际结束时间")
	}
}

func TestContractSummaryContainsBestMetricsAndDNSOutcome(t *testing.T) {
	latency, loss := 25.25, 20.0
	run := Run{ID: "contract-summary", TaskID: "task", TaskName: "摘要测试", Snapshot: Task{Config: optimizer.DefaultConfig()}, Result: optimizer.Result{Status: "completed", Candidates: []optimizer.Candidate{{IP: "192.0.2.5", Family: "ipv4", Eligible: true, Rank: 1, LatencyMS: &latency, LossPercent: &loss}}}, Deliveries: []Delivery{{Kind: "dns", State: "unconfirmed", Target: &Target{TargetID: "target"}, Reason: "结果未确认"}}}
	text := summary(run, false)
	for _, part := range []string{"192.0.2.5", "25.25 ms", "20.00%", "下载 未测", "未启用", "unconfirmed", "结果未确认"} {
		if !strings.Contains(text, part) {
			t.Fatalf("摘要缺少 %s", part)
		}
	}
}

func TestContractRetiredReceiptCannotBeReused(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	id := "contract-expired-operation"
	request := []byte("fixed")
	operation, _, err := fixture.service.mutate(id, "contract", request, func(tx *sql.Tx) (string, any, error) { return "object", nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	operation.State = "expired"
	operation.Result = nil
	acceptancePut(t, fixture, "operations", id, operation)
	_, _, err = fixture.service.mutate(id, "contract", request, func(tx *sql.Tx) (string, any, error) { t.Fatal("过期旧号被重新执行"); return "", nil, nil })
	acceptanceAPIError(t, err, http.StatusGone)
}

type contractIdentityProvider struct {
	*acceptanceProvider
	mu    sync.Mutex
	fail  bool
	calls int
}

func (provider *contractIdentityProvider) AccountIdentity(context.Context, integrations.Connection) (string, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	if provider.fail {
		return "", errors.New("模拟身份接口暂时不可用")
	}
	return "fake-account-001", nil
}

func TestContractConnectionRetryReturnsReceiptBeforeIdentityCheck(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	provider := &contractIdentityProvider{acceptanceProvider: fixture.provider}
	fixture.service.provider = provider
	server := httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	client := acceptanceLogin(t, server)
	body := []byte(`{"operation_id":"contract-connection-op","connection":{"kind":"alidns","name":"仅测试","account_id":"fake-account-001","secrets":{"access_key_id":"FAKE_id","access_key_secret":"FAKE_secret"}}}`)
	first := acceptanceDo(t, client.client, http.MethodPost, client.base+"/api/v1/connections", body, client.csrf, client.base)
	acceptanceStatus(t, first, http.StatusAccepted)
	provider.mu.Lock()
	provider.fail = true
	provider.mu.Unlock()
	retried := acceptanceDo(t, client.client, http.MethodPost, client.base+"/api/v1/connections", body, client.csrf, client.base)
	acceptanceStatus(t, retried, http.StatusAccepted)
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("重复操作不应再次核验外部账号，实际 %d 次", calls)
	}
}

func TestContractNameOnlyConnectionEditDoesNotExposeOrReplaceSecrets(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	acceptanceConnection(t, fixture, "contract-notify-connection", "telegram", "")
	server := httptest.NewServer(fixture.service.Handler())
	defer server.Close()
	client := acceptanceLogin(t, server)
	old := acceptanceGet[Connection](t, fixture, "connections", "contract-notify-connection")
	old.Ciphertext, _ = fixture.service.encrypt(map[string]string{"bot_token": "100000:FAKE_token_only", "chat_id": "FAKE_chat"})
	acceptancePut(t, fixture, "connections", old.ID, old)
	request := httptest.NewRequest(http.MethodPatch, "http://management.test/api/v1/connections/contract-notify-connection", strings.NewReader(`{"operation_id":"contract-rename-op","connection":{"kind":"telegram","name":"更名","account_id":""}}`))
	session := sessionRecord{ID: "contract-test-session", CSRF: "contract-test-csrf", PasswordVersion: fixture.service.passwordVersion, ExpiresAt: acceptanceNow().AddDate(0, 0, 1)}
	token := "contract-test-token"
	session.ID = fixture.service.digest([]byte(token))
	acceptancePut(t, fixture, "sessions", session.ID, session)
	request.AddCookie(&http.Cookie{Name: "cfst_session", Value: token})
	request.Header.Set("X-CSRF-Token", session.CSRF)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", "1")
	response := httptest.NewRecorder()
	fixture.service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("仅改名应可省略凭据: %s", response.Body.String())
	}
	current := acceptanceGet[Connection](t, fixture, "connections", old.ID)
	previous, _ := fixture.service.credentials(old)
	now, _ := fixture.service.credentials(current)
	if previous.Secrets["bot_token"] != now.Secrets["bot_token"] || current.Name != "更名" {
		t.Fatal("仅改名不应替换原凭据")
	}
	public := acceptanceDo(t, client.client, http.MethodGet, client.base+"/api/v1/connections/"+old.ID, nil, "", "")
	defer public.Body.Close()
	publicBody, err := io.ReadAll(public.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicBody), "ciphertext") || strings.Contains(string(publicBody), "bot_token") {
		t.Fatal("连接读接口不得泄露秘密")
	}
}

func TestContractCorruptProtectionFailsBeforeWritingAndSettlesDelivery(t *testing.T) {
	fixture := newAcceptanceFixture(t, acceptanceNow(), nil, nil)
	fixture.service.cancel()
	fixture.service.wg.Wait()
	acceptanceConnection(t, fixture, "contract-corrupt-connection", "alidns", "fake-account-001")
	target := acceptanceTarget("contract-corrupt-connection", "fake-account-001", "corrupt-record")
	run := Run{ID: "contract-corrupt-run", Status: "completed", Deliveries: []Delivery{{ID: "contract-corrupt-delivery", Kind: "dns", State: "pending", Automatic: true, ConnectionID: target.ConnectionID, Target: &target, IP: "192.0.2.80"}}}
	acceptancePut(t, fixture, "runs", run.ID, run)
	if _, err := fixture.service.db.Exec("INSERT INTO objects(kind,id,body) VALUES(?,?,?)", "dns_protection", target.TargetID, []byte("invalid-json")); err != nil {
		t.Fatal(err)
	}
	fixture.service.performDelivery(context.Background(), run.ID, "contract-corrupt-delivery")
	result := acceptanceGet[Run](t, fixture, "runs", run.ID)
	if result.Deliveries[0].State != "failed" || !strings.Contains(result.Deliveries[0].Reason, "未发起写入") {
		t.Fatal("保护读取失败必须明确终止未发起的自动写入")
	}
	if updates, _ := fixture.provider.callCounts(); updates != 0 {
		t.Fatal("损坏保护状态不能尝试写DNS")
	}
}
