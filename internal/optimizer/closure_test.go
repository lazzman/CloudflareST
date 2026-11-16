package optimizer

import (
	"context"
	"errors"
	"testing"
)

func TestClosureBudgetDetailsAndStageCounters(t *testing.T) {
	config := testConfig(Source{Kind: "cidr", Value: "192.0.2.8/30", Count: 8})
	config.IPv4Limit = 10
	stages := map[string]bool{}
	result, err := successfulEngine().Run(context.Background(), config, func(progress Progress) {
		stages[progress.Stage] = true
		if progress.Total != nil && (progress.Done < 0 || progress.Done > *progress.Total) {
			t.Errorf("实际计数越界: %+v", progress)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Budgets) != 1 {
		t.Fatal("必须按启用地址族返回预算")
	}
	budget := result.Budgets[0]
	if budget.RequestedInitial != 8 || budget.Sampled != 4 || budget.ValidInitial != 4 || budget.TriggerCount != 1 || budget.Remaining != 6 || budget.InitialReason == "" {
		t.Fatalf("详细预算不符合实际来源容量: %+v", budget)
	}
	if budget.InitialAllocated != 4 {
		t.Fatalf("初始分配数应与有限CIDR实际可取容量一致: %+v", budget)
	}
	for _, stage := range []string{"sources", "sampling", "probe", "expansion", "filtering", "sorting"} {
		if !stages[stage] {
			t.Errorf("缺少真实阶段进度 %s", stage)
		}
	}
}

func TestClosureCancelFromFinalProgressCannotReturnCompleted(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.25"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := successfulEngine().Run(ctx, config, func(progress Progress) {
		if progress.Stage == "sorting" && progress.Done > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || result.Status != "cancelled" {
		t.Fatalf("排序阶段取消未传回事实: %+v %v", result, err)
	}
}

func TestClosureCompletedFamilyCountersRemainTrueWhenNextFamilyCancelled(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.25"}, Source{Kind: "ip", Value: "2001:db8::25"})
	config.IPv6Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := successfulEngine().Run(ctx, config, func(progress Progress) {
		if progress.Stage == "probe" && progress.Family == "ipv6" && progress.Done == 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("预期在IPv6探测前取消")
	}
	if result.Budgets[0].Family != "ipv4" || result.Budgets[0].ValidInitial != 1 {
		t.Fatalf("已经测出的IPv4有效延迟不得回退为0: %+v", result.Budgets)
	}
}
