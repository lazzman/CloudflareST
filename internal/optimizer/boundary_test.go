package optimizer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInitialAllocationReusesUnavailableQuota(t *testing.T) {
	for _, sources := range [][]Source{
		{{Kind: "cidr", Value: "192.0.2.1/32", Count: 1}, {Kind: "cidr", Value: "192.0.2.1/32", Count: 100}, {Kind: "cidr", Value: "192.0.3.0/24"}},
		{{Kind: "ip", Value: "192.0.2.1"}, {Kind: "cidr", Value: "192.0.2.1/32"}, {Kind: "cidr", Value: "192.0.3.0/24"}},
		{{Kind: "cidr", Value: "192.0.2.0/31"}, {Kind: "cidr", Value: "192.0.2.1/32"}, {Kind: "cidr", Value: "192.0.3.0/24"}},
	} {
		config := testConfig(sources...)
		config.IPv4Limit = 10
		pool, err := successfulEngine().collectSources(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		budget, err := planInitial(config, pool, "ipv4")
		if err != nil || budget.Sampled != budget.InitialLimit || budget.Used > budget.Limit {
			t.Fatalf("可用网段未接收无法使用的初采额度: %+v，来源=%+v，错误=%v", budget, sources, err)
		}
	}
}

func TestInitialSingleQuotaAndFullDirectedBudget(t *testing.T) {
	for _, limit := range []int{1, 2, 10} {
		config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "cidr", Value: "192.0.3.0/24"})
		config.IPv4Limit = limit
		pool, err := successfulEngine().collectSources(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		budget, err := planInitial(config, pool, "ipv4")
		want := map[int]int{1: 0, 2: 1, 10: 7}[limit]
		if err != nil || budget.InitialLimit != want || budget.Sampled != want || budget.ExpansionLimit != limit-1-want {
			t.Fatalf("初采取整边界错误: %+v，错误=%v", budget, err)
		}
	}
}

func TestInitialQuotaReassignedWhenFirstPassAddsNothing(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "cidr", Value: "192.0.2.1/32"}, Source{Kind: "cidr", Value: "192.0.3.0/24"})
	config.IPv4Limit = 2
	pool, err := successfulEngine().collectSources(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := planInitial(config, pool, "ipv4")
	if err != nil || budget.Sampled != 1 || budget.Used != 2 {
		t.Fatalf("首轮无新增不应阻止其他网段接收剩余额度: %+v，%v", budget, err)
	}
}

func TestAllDirectedBudgetsCheckedBeforeInitialSampling(t *testing.T) {
	config := testConfig(Source{Kind: "cidr", Value: "192.0.2.0/24"}, Source{Kind: "ip", Value: "2001:db8::1"}, Source{Kind: "ip", Value: "2001:db8::2"})
	config.IPv6Enabled, config.IPv4Limit, config.IPv6Limit = true, 10, 1
	engine := successfulEngine()
	engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) {
		t.Error("任一族定向超限时不得启动任何探测")
		return time.Millisecond, nil
	}
	engine.ICMPCheck = func(string) error {
		t.Error("定向超限须早于 ICMP 预检")
		return nil
	}
	result, err := engine.Run(context.Background(), config, func(event Progress) {
		if event.Stage == "sampling" {
			t.Error("所有族预算检查通过前不得开始初采分配")
		}
	})
	if err == nil || result.Status != "failed" || len(result.Candidates) != 2 || len(result.Budgets) != 2 {
		t.Fatalf("应在任何初采前明确报告 IPv6 定向超限: %+v，%v", result, err)
	}
	for _, candidate := range result.Candidates {
		if candidate.Origin != "direct" || candidate.State != "untested" || candidate.ProbeAttempts != 0 {
			t.Fatalf("超限不能增加网段候选或伪造探测结果: %+v", candidate)
		}
	}
}

func TestCancellationBeforeInitialSamplingDoesNotStartProbe(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "cidr", Value: "192.0.3.0/24"})
	config.IPv4Limit = 10
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := successfulEngine()
	engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) {
		t.Error("分配前取消不得启动探测")
		return time.Millisecond, nil
	}
	result, err := engine.Run(ctx, config, func(event Progress) {
		if event.Stage == "sampling" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || result.Status != "cancelled" || len(result.Candidates) != 1 || result.Candidates[0].State != "cancelled" || result.Budgets[0].Sampled != 0 {
		t.Fatalf("初采分配前取消应保留已有定向候选并终止后续处理: %+v，%v", result, err)
	}
}

func TestExpansionUsesRawLatencyAndIndependentFamilyCeiling(t *testing.T) {
	config := testConfig()
	config.IPv6Enabled = true
	config.IPv4Limit, config.IPv6Limit = 12, 12
	config.LatencyEnabled, config.LossEnabled = false, false
	for index := 1; index <= 11; index++ {
		config.Sources = append(config.Sources, Source{Kind: "ip", Value: fmt.Sprintf("192.0.%d.100", index)}, Source{Kind: "ip", Value: fmt.Sprintf("2001:db8:%x::100", index)})
	}
	engine := successfulEngine()
	engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
		address := netip.MustParseAddr(request.IP)
		index := int(address.As16()[5])
		if address.Is4() {
			index = int(address.As4()[2])
		}
		if index == 3 {
			return time.Millisecond, nil
		}
		if index == 2 {
			return time.Millisecond + time.Nanosecond, nil
		}
		return time.Millisecond + 2*time.Nanosecond, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range result.Budgets {
		if budget.Expanded != 1 || budget.Used != 12 || len(budget.Expansions) != 2 {
			t.Fatalf("两族必须分别对 11 个有效初始结果取前 2 个，且不返还预算: %+v", budget)
		}
		if budget.Expansions[0].BestLatencyMS != 1 || budget.Expansions[1].BestLatencyMS <= 1 || budget.Expansions[0].Added != 1 || budget.Expansions[1].Added != 0 {
			t.Fatalf("扩采不得按显示四舍五入值排序或借用另一族预算: %+v", budget.Expansions)
		}
	}
}

func TestDirectSourceRangeIsNotRestrictedByOverlappingCIDR(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.5"}, Source{Kind: "cidr", Value: "192.0.2.0/28", Count: 1})
	config.IPv4Limit = 300
	engine := successfulEngine()
	engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
		if request.IP == "192.0.2.5" {
			return time.Millisecond, nil
		}
		return 2 * time.Millisecond, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 256 || result.Budgets[0].Expanded != 254 || result.Budgets[0].Used != 256 {
		t.Fatalf("定向来源应保留完整 /24 扩采范围，CIDR 来源仍受 /28 约束: 候选=%d，预算=%+v", len(result.Candidates), result.Budgets[0])
	}
	for _, candidate := range result.Candidates {
		address := netip.MustParseAddr(candidate.IP)
		if candidate.Origin == "expanded" && !netip.MustParsePrefix("192.0.2.0/24").Contains(address) {
			t.Fatalf("扩采超出定向邻近范围: %+v", candidate)
		}
		for _, sourceID := range candidate.Sources {
			if sourceID == "source-002" && !netip.MustParsePrefix("192.0.2.0/28").Contains(address) {
				t.Fatalf("定向范围扩张不能使 CIDR 来源越界: %+v", candidate)
			}
		}
	}
}

func TestExpansionCoalescesConstrainedRangesWithoutRepeatedProbes(t *testing.T) {
	config := testConfig(Source{Kind: "cidr", Value: "192.0.2.0/28", Count: 1}, Source{Kind: "cidr", Value: "192.0.2.32/28", Count: 1})
	for index := 1; index <= 9; index++ {
		config.Sources = append(config.Sources, Source{Kind: "ip", Value: fmt.Sprintf("198.51.%d.1", index)})
	}
	config.IPv4Limit = 50
	engine := successfulEngine()
	var callsLock sync.Mutex
	calls := make(map[string]int)
	engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
		callsLock.Lock()
		calls[request.IP]++
		callsLock.Unlock()
		if strings.HasPrefix(request.IP, "192.0.2.") {
			return time.Millisecond, nil
		}
		return 4 * time.Millisecond, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil {
		t.Fatal(err)
	}
	budget := result.Budgets[0]
	if budget.Sampled != 2 || budget.Expanded != 30 || budget.Used != 41 || len(budget.Expansions) != 1 || len(budget.Expansions[0].Ranges) != 2 || len(budget.Expansions[0].Triggers) != 2 {
		t.Fatalf("两个受限范围应合并为一个邻近单元，未用初采名额可参与扩采: %+v", budget)
	}
	for _, candidate := range result.Candidates {
		if calls[candidate.IP] != 1 {
			t.Fatalf("单轮扩采不得重复测量: %+v，次数=%d", candidate, calls[candidate.IP])
		}
		if candidate.Origin == "expanded" {
			address := netip.MustParseAddr(candidate.IP)
			if !netip.MustParsePrefix("192.0.2.0/28").Contains(address) && !netip.MustParsePrefix("192.0.2.32/28").Contains(address) {
				t.Fatalf("合并单元不能突破各原始 CIDR: %+v", candidate)
			}
		}
	}
}

func TestFilterDecisionsExplainDisabledAndSkippedStages(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"})
		config.IPv4Limit = 1
		config.DownloadEnabled, config.DownloadIPv4Count = true, 1
		if disabled {
			config.LatencyEnabled = false
			config.FilterOrder = []string{"loss", "download"}
		} else {
			config.LatencyMaxMS = 0.5
		}
		engine := successfulEngine()
		engine.Download = func(context.Context, DownloadRequest) (DownloadSample, error) {
			if !disabled {
				t.Error("前置延迟过滤未通过，不得下载")
			}
			return DownloadSample{Bytes: 5000000, Seconds: 1, End: "completed"}, nil
		}
		result, err := engine.Run(context.Background(), config, nil)
		if err != nil {
			t.Fatal(err)
		}
		decisions := map[string]Decision{}
		for _, decision := range result.Candidates[0].Steps {
			decisions[decision.Stage] = decision
		}
		if disabled {
			if decisions["latency"].State != "disabled" || !result.Candidates[0].Eligible {
				t.Fatalf("关闭的过滤必须记为未启用，不得记为通过: %+v", result.Candidates[0])
			}
		} else if decisions["loss"].State != "skipped" || decisions["download"].State != "skipped" || decisions["download"].Reason == "" || result.Candidates[0].SpeedMBS != nil || result.Candidates[0].Rank != 0 {
			t.Fatalf("后续未执行步骤须解释原因且不得伪造指标或名次: %+v", result.Candidates[0])
		}
	}
}

func TestProgressReflectsExpansionAndActualDownloadSet(t *testing.T) {
	config := testConfig(Source{Kind: "cidr", Value: "192.0.2.0/28", Count: 1})
	config.IPv4Limit, config.DownloadEnabled, config.DownloadIPv4Count = 20, true, 2
	engine := successfulEngine()
	engine.Concurrency = 1
	var events []Progress
	var current Progress
	var progressLock sync.Mutex
	engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) {
		progressLock.Lock()
		defer progressLock.Unlock()
		if current.Stage != "probe" && current.Stage != "expansion" || current.Total == nil {
			t.Errorf("探测开始前未发出本阶段真实总量: %+v", current)
		}
		return time.Millisecond, nil
	}
	downloads := 0
	engine.Download = func(context.Context, DownloadRequest) (DownloadSample, error) {
		if current.Stage != "download" || current.Total == nil || *current.Total != 2 || current.Done != downloads {
			t.Errorf("下载开始前进度不对应实际下载集合: %+v", current)
		}
		downloads++
		if downloads == 1 {
			return DownloadSample{}, errors.New("受控下载失败")
		}
		return DownloadSample{Bytes: 5000000, Seconds: 1, End: "completed"}, nil
	}
	result, err := engine.Run(context.Background(), config, func(event Progress) {
		progressLock.Lock()
		defer progressLock.Unlock()
		current = event
		events = append(events, event)
	})
	if err != nil || result.Status != "completed" {
		t.Fatalf("受控运行失败: %+v，%v", result, err)
	}
	initialDone, expansionDone, downloadDone := false, false, false
	for _, event := range events {
		if event.Stage == "sampling" && event.Total != nil && *event.Total != 1 {
			t.Fatalf("初采阶段只允许报告已确定的实际新增数量，不能报告预算: %+v", event)
		}
		if event.Total == nil {
			continue
		}
		if event.Done > *event.Total {
			t.Fatalf("已处理数量不能超过阶段实际总量: %+v", event)
		}
		initialDone = initialDone || event.Stage == "probe" && event.Done == 1 && *event.Total == 1
		expansionDone = expansionDone || event.Stage == "expansion" && event.Done == 15 && *event.Total == 15
		downloadDone = downloadDone || event.Stage == "download" && event.Done == 2 && *event.Total == 2
	}
	if !initialDone || !expansionDone || !downloadDone {
		t.Fatalf("缺少真实初采、扩采或下载完成进度: %+v", events)
	}
}

func TestCancellationKeepsFactsAndFinalizesPendingCandidates(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "192.0.2.2"}, Source{Kind: "ip", Value: "192.0.2.3"})
	config.IPv4Limit, config.DownloadEnabled, config.DownloadIPv4Count = 3, true, 3
	engine := successfulEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	engine.Download = func(context.Context, DownloadRequest) (DownloadSample, error) {
		calls++
		if calls == 2 {
			cancel()
			return DownloadSample{Bytes: 1234, Seconds: 0.1}, ctx.Err()
		}
		return DownloadSample{Bytes: 5000000, Seconds: 1, End: "completed"}, nil
	}
	result, err := engine.Run(ctx, config, nil)
	if !errors.Is(err, context.Canceled) || result.Status != "cancelled" || calls != 2 {
		t.Fatalf("用户取消不应报告运行失败或继续下载: %+v，错误=%v，调用=%d", result, err, calls)
	}
	for _, candidate := range result.Candidates {
		switch candidate.IP {
		case "192.0.2.1":
			if !candidate.Eligible || candidate.DownloadBytes != 5000000 || candidate.DownloadEnd != "completed" {
				t.Fatalf("取消不得抹去已完成测量事实: %+v", candidate)
			}
		default:
			if candidate.State != "cancelled" || candidate.Eligible || candidate.Rank != 0 || candidate.SpeedMBS != nil {
				t.Fatalf("取消后不能残留运行中状态或虚构合格指标: %+v", candidate)
			}
		}
	}
}

func TestProbeCancellationFinalizesQueuedCandidates(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "192.0.2.2"}, Source{Kind: "ip", Value: "192.0.2.3"})
	config.IPv4Limit, config.ProbeCount = 3, 5
	engine := successfulEngine()
	engine.Concurrency = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) {
		calls++
		if calls == 2 {
			cancel()
			return 0, ctx.Err()
		}
		return time.Millisecond, nil
	}
	result, err := engine.Run(ctx, config, nil)
	if !errors.Is(err, context.Canceled) || result.Status != "cancelled" || calls != 2 {
		t.Fatalf("取消应停止正在探测的批次，且不再消费后续探测次数: %+v，%v，次数=%d", result, err, calls)
	}
	for _, candidate := range result.Candidates {
		if candidate.State != "cancelled" || candidate.Eligible || candidate.LatencyMS != nil || candidate.Rank != 0 {
			t.Fatalf("取消后不得残留排队状态或伪造未完成批次的指标: %+v", candidate)
		}
	}
}

func TestDownloadRejectsNonFiniteDerivedSpeed(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"})
	config.IPv4Limit, config.DownloadEnabled, config.DownloadIPv4Count = 1, true, 1
	engine := successfulEngine()
	engine.Download = func(context.Context, DownloadRequest) (DownloadSample, error) {
		return DownloadSample{Bytes: math.MaxInt64, Seconds: math.SmallestNonzeroFloat64, End: "completed"}, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil || result.Status != "no_results" || result.Candidates[0].SpeedMBS != nil || result.Candidates[0].State != "failed" {
		t.Fatalf("无穷大速度不能作为有效下载结果: %+v，%v", result, err)
	}
}

func TestICMPPreflightChecksAllCandidateFamiliesBeforeProbing(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "2001:db8::1"})
	config.IPv6Enabled = true
	config.IPv4Limit, config.IPv6Limit = 1, 1
	engine := successfulEngine()
	var checked []string
	engine.ICMPCheck = func(family string) error {
		checked = append(checked, family)
		if family == "ipv6" {
			return fmt.Errorf("受控 IPv6 权限不足: %w", ErrICMPUnavailable)
		}
		return nil
	}
	engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) {
		t.Error("任一需要的地址族 ICMP 不可用时，不得开始其他族探测")
		return time.Millisecond, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if !errors.Is(err, ErrICMPUnavailable) || result.Status != "failed" || !reflect.DeepEqual(checked, []string{"ipv4", "ipv6"}) {
		t.Fatalf("ICMP 应按需要的地址族预检并保留权限原因: %+v，%v，%v", result, err, checked)
	}
	for _, candidate := range result.Candidates {
		if candidate.State != "untested" || candidate.ProbeAttempts != 0 || !strings.Contains(candidate.Reason, "ICMP") {
			t.Fatalf("预检失败不得伪造成测试失败或残留排队状态: %+v", candidate)
		}
	}
}
