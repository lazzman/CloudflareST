package optimizer

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig(sources ...Source) Config {
	config := DefaultConfig()
	config.DownloadEnabled = false
	config.ProbeCount = 1
	config.Sources = sources
	return config
}

func successfulEngine() *Engine {
	engine := New()
	engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) { return time.Millisecond, nil }
	return engine
}

func TestDirectedDedupAndFailureBudget(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "domain", Value: "test.invalid"}, Source{Kind: "cidr", Value: "192.0.2.0/28"})
	config.IPv4Limit = 16
	engine := successfulEngine()
	engine.Resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.1")}, nil
	}
	var lock sync.Mutex
	calls := make(map[string]int)
	engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
		lock.Lock()
		calls[request.IP]++
		lock.Unlock()
		return 0, errors.New("受控失败")
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil || result.Status != "no_results" {
		t.Fatalf("失败探测不应触发任务异常: %+v, %v", result, err)
	}
	budget := result.Budgets[0]
	if budget.Directed != 1 || budget.InitialLimit != 12 || budget.Sampled != 12 || budget.ExpansionLimit != 3 || budget.Expanded != 0 || budget.Used != 13 || len(result.Candidates) != 13 {
		t.Fatalf("预算计算错误: %+v", budget)
	}
	for _, candidate := range result.Candidates {
		if calls[candidate.IP] != 1 || candidate.LatencyMS != nil || candidate.Eligible {
			t.Fatalf("去重或失败指标错误: %+v, %v", candidate, calls)
		}
		if candidate.IP == "192.0.2.1" && len(candidate.Sources) != 4 {
			t.Fatalf("丢失来源关联: %+v", candidate)
		}
	}
}

func TestDirectedOverBudgetFailsBeforeAnyProbe(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "192.0.2.2"})
	config.IPv4Limit = 1
	engine := successfulEngine()
	var calls int32
	engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) {
		atomic.AddInt32(&calls, 1)
		return time.Millisecond, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err == nil || result.Status != "failed" || calls != 0 {
		t.Fatalf("定向超预算应在探测前失败: %+v, %v, %d", result, err, calls)
	}
}

func TestBalancedInitialAllocationAndRemainingOne(t *testing.T) {
	sources := []Source{{Kind: "cidr", Value: "192.0.2.1/32"}, {Kind: "cidr", Value: "192.0.3.0/24"}, {Kind: "cidr", Value: "192.0.4.0/24"}}
	config := testConfig(sources...)
	config.IPv4Limit = 10
	var before []string
	for iteration := 0; iteration < 2; iteration++ {
		pool, err := successfulEngine().collectSources(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		budget, err := planInitial(config, pool, "ipv4")
		if err != nil || budget.InitialLimit != 8 || budget.Sampled != 8 || budget.ExpansionLimit != 2 {
			t.Fatalf("初采预算错误: %+v, %v", budget, err)
		}
		counts := map[string]int{}
		var addresses []string
		for _, candidate := range pool.byIP {
			address := netip.MustParseAddr(candidate.IP)
			counts[netip.PrefixFrom(address, 24).Masked().String()]++
			addresses = append(addresses, candidate.IP)
		}
		if counts["192.0.2.0/24"] != 1 || counts["192.0.3.0/24"] != 4 || counts["192.0.4.0/24"] != 3 {
			t.Fatalf("未均衡缩减及回收小段配额: %v", counts)
		}
		sort.Strings(addresses)
		if iteration == 1 && !reflect.DeepEqual(before, addresses) {
			t.Fatalf("来源顺序改变了取样结果: %v / %v", before, addresses)
		}
		before = addresses
		config.Sources[0], config.Sources[2] = config.Sources[2], config.Sources[0]
	}
	config = testConfig(Source{Kind: "cidr", Value: "2001:db8::/32"})
	config.IPv4Enabled, config.IPv6Enabled, config.IPv6Limit = false, true, 1
	result, err := successfulEngine().Run(context.Background(), config, nil)
	if err != nil || len(result.Candidates) != 1 || result.Budgets[0].InitialLimit != 1 {
		t.Fatalf("剩一个名额或超大 IPv6 网段处理错误: %+v, %v", result, err)
	}
}

func TestExpansionStaysInCIDRAndOnlyOneRound(t *testing.T) {
	for _, cidr := range []string{"192.0.2.32/28", "2001:db8::40/124"} {
		t.Run(cidr, func(t *testing.T) {
			config := testConfig(Source{Kind: "cidr", Value: cidr, Count: 1})
			config.IPv4Limit, config.IPv6Limit = 300, 300
			config.IPv6Enabled = strings.Contains(cidr, ":")
			config.IPv4Enabled = !config.IPv6Enabled
			result, err := successfulEngine().Run(context.Background(), config, nil)
			if err != nil || len(result.Candidates) != 16 {
				t.Fatalf("小段应只补满 16 个地址: %d, %v", len(result.Candidates), err)
			}
			allowed := netip.MustParsePrefix(cidr)
			for _, candidate := range result.Candidates {
				if !allowed.Contains(netip.MustParseAddr(candidate.IP)) {
					t.Fatalf("扩采越界: %+v", candidate)
				}
			}
			if result.Budgets[0].Sampled != 1 || result.Budgets[0].Expanded != 15 || len(result.Budgets[0].Expansions) != 1 {
				t.Fatalf("不应递归扩采: %+v", result.Budgets[0])
			}
		})
	}
}

func TestPureDirectedAndDomainExpandFullNeighborhood(t *testing.T) {
	for _, item := range []struct{ kind, address, family string }{{"ip", "192.0.2.123", "ipv4"}, {"domain", "2001:db8::1234", "ipv6"}} {
		t.Run(item.kind+item.family, func(t *testing.T) {
			value := item.address
			if item.kind == "domain" {
				value = "test.invalid"
			}
			config := testConfig(Source{Kind: item.kind, Value: value})
			config.IPv4Enabled, config.IPv6Enabled = item.family == "ipv4", item.family == "ipv6"
			config.IPv4Limit, config.IPv6Limit = 300, 300
			engine := successfulEngine()
			engine.Resolve = func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr(item.address)}, nil
			}
			result, err := engine.Run(context.Background(), config, nil)
			if err != nil || len(result.Candidates) != 256 || result.Budgets[0].Expanded != 255 {
				t.Fatalf("纯定向来源应扩至完整邻近段: %d, %+v, %v", len(result.Candidates), result.Budgets, err)
			}
			for _, candidate := range result.Candidates {
				if candidate.IP != item.address && candidate.Origin != "expanded" {
					t.Fatalf("扩采不得冒充手动来源: %+v", candidate)
				}
			}
		})
	}
}

func TestExpansionBestSubnetFirstAndInitialTopTenPercent(t *testing.T) {
	var sources []Source
	for index := 1; index <= 20; index++ {
		sources = append(sources, Source{Kind: "ip", Value: fmt.Sprintf("192.0.%d.100", index)})
	}
	config := testConfig(sources...)
	config.IPv4Limit = 25
	engine := successfulEngine()
	engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
		address := netip.MustParseAddr(request.IP).As4()
		if address[3] != 100 {
			return time.Microsecond, nil
		}
		return time.Duration(address[2]) * time.Millisecond, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil || len(result.Candidates) != 25 {
		t.Fatalf("扩采预算错误: %d, %v", len(result.Candidates), err)
	}
	budget := result.Budgets[0]
	if budget.ExpansionLimit != 5 || budget.Expanded != 5 || len(budget.Expansions) != 2 || budget.Expansions[0].Added != 5 || budget.Expansions[1].Added != 0 {
		t.Fatalf("扩采应只触发前 10%% 并优先最佳子网: %+v", budget)
	}
	for _, candidate := range result.Candidates {
		if candidate.Origin == "expanded" && !strings.HasPrefix(candidate.IP, "192.0.1.") {
			t.Fatalf("慢子网不应先占预算: %+v", candidate)
		}
	}
}

func TestMedianLossAndAllFailed(t *testing.T) {
	for _, item := range []struct {
		name      string
		durations []time.Duration
		failures  map[int]bool
		median    *float64
		loss      float64
	}{
		{"奇数成功", []time.Duration{9, 1, 0, 7, 0}, map[int]bool{2: true, 4: true}, floatPointer(7), 40},
		{"偶数成功", []time.Duration{9, 1, 0, 7, 3}, map[int]bool{2: true}, floatPointer(5), 20},
		{"全失败", []time.Duration{0, 0, 0, 0, 0}, map[int]bool{0: true, 1: true, 2: true, 3: true, 4: true}, nil, 100},
	} {
		t.Run(item.name, func(t *testing.T) {
			config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"})
			config.IPv4Limit, config.ProbeCount, config.LossMaxPercent = 1, 5, 100
			engine := successfulEngine()
			index := 0
			engine.Probe = func(context.Context, ProbeRequest) (time.Duration, error) {
				current := index
				index++
				if item.failures[current] {
					return 0, errors.New("探测失败")
				}
				return item.durations[current] * time.Millisecond, nil
			}
			result, err := engine.Run(context.Background(), config, nil)
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Candidates[0]
			if !reflect.DeepEqual(candidate.LatencyMS, item.median) || *candidate.LossPercent != item.loss || candidate.ProbeAttempts != 5 {
				t.Fatalf("统计口径错误: %+v", candidate)
			}
			if item.median == nil && (candidate.Eligible || result.Status != "no_results") {
				t.Fatalf("全失败不能作为有效延迟: %+v", candidate)
			}
		})
	}
}

func TestFamilyIndependentRanksAndSorting(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.2"}, Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "2001:db8::2"}, Source{Kind: "ip", Value: "2001:db8::1"})
	config.IPv6Enabled, config.IPv4Limit, config.IPv6Limit = true, 2, 2
	config.DownloadEnabled, config.DownloadIPv4Count, config.DownloadIPv6Count = true, 2, 2
	engine := successfulEngine()
	engine.Download = func(_ context.Context, request DownloadRequest) (DownloadSample, error) {
		bytes := int64(6000000)
		if strings.HasSuffix(request.IP, "2") {
			bytes = 7000000
		}
		return DownloadSample{Bytes: bytes, Seconds: 1, End: "completed"}, nil
	}
	for _, mode := range []string{"latency", "speed"} {
		config.Sort = mode
		result, err := engine.Run(context.Background(), config, nil)
		if err != nil {
			t.Fatal(err)
		}
		ranks := map[string]int{}
		for _, candidate := range result.Candidates {
			if candidate.Rank == 1 {
				ranks[candidate.Family]++
				if !strings.HasSuffix(candidate.IP, "2") {
					t.Fatalf("同延迟时应速度降序: %+v", candidate)
				}
			}
		}
		if ranks["ipv4"] != 1 || ranks["ipv6"] != 1 {
			t.Fatalf("各族必须独立有 rank1: %+v", result.Candidates)
		}
	}
}

func TestDisabledIPv6NeverKeptOrProbed(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "2001:db8::1"}, Source{Kind: "cidr", Value: "2001:db8::/32"}, Source{Kind: "builtin", Value: "ipv6"}, Source{Kind: "domain", Value: "test.invalid"})
	config.IPv4Limit = 1
	engine := successfulEngine()
	engine.Resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("2001:db8::2")}, nil
	}
	engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
		if strings.Contains(request.IP, ":") {
			t.Errorf("不应探测 IPv6: %s", request.IP)
		}
		return time.Millisecond, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil || len(result.Candidates) != 1 || result.Candidates[0].Family != "ipv4" || len(result.Budgets) != 1 {
		t.Fatalf("关闭族仍进入候选: %+v, %v", result, err)
	}
}

func TestProgressArrivesBeforeOtherWorkersExit(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "192.0.2.2"})
	config.IPv4Limit = 2
	engine := successfulEngine()
	release := make(chan struct{})
	engine.Probe = func(ctx context.Context, request ProbeRequest) (time.Duration, error) {
		if strings.HasSuffix(request.IP, "2") {
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		return time.Millisecond, nil
	}
	var once sync.Once
	callback := func(progress Progress) {
		if progress.Stage == "probe" && progress.Done == 1 {
			once.Do(func() { close(release) })
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := engine.Run(ctx, config, callback)
	if err != nil || result.Status != "completed" {
		t.Fatalf("进度必须在其他工作协程结束前送达: %+v, %v", result, err)
	}
}

func TestCancellationWaitsForProbeCleanup(t *testing.T) {
	config := testConfig(Source{Kind: "cidr", Value: "192.0.2.0/24"})
	config.IPv4Limit = 20
	engine := successfulEngine()
	engine.Concurrency = 3
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	returned := make(chan error, 1)
	var active int32
	engine.Probe = func(ctx context.Context, _ ProbeRequest) (time.Duration, error) {
		atomic.AddInt32(&active, 1)
		defer atomic.AddInt32(&active, -1)
		entered <- struct{}{}
		<-ctx.Done()
		<-release
		return 0, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, err := engine.Run(ctx, config, nil); returned <- err }()
	for index := 0; index < 3; index++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("工作协程未开始")
		}
	}
	cancel()
	select {
	case err := <-returned:
		t.Fatalf("清理结束前提前返回: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) || atomic.LoadInt32(&active) != 0 {
			t.Fatalf("取消未完成回收: %v, %d", err, active)
		}
	case <-time.After(time.Second):
		t.Fatal("取消未退出")
	}
}

func TestICMPUnavailableFailsWithoutTCPFallback(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"})
	config.IPv4Limit = 1
	engine := successfulEngine()
	engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
		if request.Mode != "icmp" {
			t.Fatal("不允许回退 TCP")
		}
		return 0, ErrICMPUnavailable
	}
	result, err := engine.Run(context.Background(), config, nil)
	if !errors.Is(err, ErrICMPUnavailable) || result.Status != "failed" {
		t.Fatalf("ICMP 不可用应明确失败: %+v, %v", result, err)
	}
}

func TestSourcePartialFailureAndNoCandidateFailure(t *testing.T) {
	config := testConfig(Source{Kind: "domain", Value: "failed.invalid"}, Source{Kind: "ip", Value: "192.0.2.1"})
	config.IPv4Limit = 1
	engine := successfulEngine()
	engine.Resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("受控解析失败") }
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil || result.Status != "completed" || result.Sources[0].Status != "failed" {
		t.Fatalf("部分来源失败应继续: %+v,%v", result, err)
	}
	config.Sources = config.Sources[:1]
	result, err = engine.Run(context.Background(), config, nil)
	if err == nil || result.Status != "failed" || len(result.Candidates) != 0 {
		t.Fatalf("全部来源无候选必须失败: %+v,%v", result, err)
	}
}
