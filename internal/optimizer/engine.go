package optimizer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/netip"
	"sort"
	"time"
)

func (engine *Engine) Run(ctx context.Context, config Config, progress func(Progress)) (result Result, failure error) {
	result = Result{Candidates: []Candidate{}, Sources: []SourceResult{}, Budgets: []Budget{}}
	var pool *candidatePool
	defer func() {
		if pool == nil {
			return
		}
		for index := range result.Budgets {
			budget := &result.Budgets[index]
			budget.ValidInitial = 0
			for _, candidate := range pool.byIP {
				if candidate.Family == budget.Family && candidate.Origin != "expanded" && candidate.LatencyMS != nil {
					budget.ValidInitial++
				}
			}
		}
	}()
	if ctx == nil {
		result.Status = "failed"
		result.Error = "运行上下文不能为空"
		return result, fmt.Errorf("%s", result.Error)
	}
	if err := Validate(config); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return failedRun(result, err)
	}
	local := *engine
	engine = &local
	config.Sources = append([]Source(nil), config.Sources...)
	config.FilterOrder = append([]string(nil), config.FilterOrder...)
	pool, err := engine.collectSourcesWithProgress(ctx, config, progress)
	if err != nil {
		if pool != nil {
			if ctx.Err() != nil {
				markCancelledCandidates(pool)
			}
			result.Sources = sourceResults(pool)
			result.Candidates = copyCandidates(pool)
		}
		return failedRun(result, err)
	}
	families := []string{}
	if config.IPv4Enabled {
		families = append(families, "ipv4")
	}
	if config.IPv6Enabled {
		families = append(families, "ipv6")
	}
	for _, family := range families {
		budget, budgetErr := directedBudget(config, pool, family)
		result.Budgets = append(result.Budgets, budget)
		if budgetErr != nil {
			for _, candidate := range pool.byIP {
				candidate.State = "untested"
				candidate.Reason = "定向候选超过地址族预算，本轮未执行测速"
			}
			result.Sources = sourceResults(pool)
			result.Candidates = copyCandidates(pool)
			return failedRun(result, budgetErr)
		}
	}
	for index, family := range families {
		if progress != nil {
			progress(Progress{Stage: "sampling", Family: family, Unit: "IP", Message: "正在分配初采名额；累计候选总量待扩采结束后确定"})
		}
		if err := ctx.Err(); err != nil {
			markCancelledCandidates(pool)
			result.Sources = sourceResults(pool)
			result.Candidates = copyCandidates(pool)
			return failedRun(result, err)
		}
		budget, planErr := planInitial(config, pool, family)
		result.Budgets[index] = budget
		if planErr != nil {
			result.Sources = sourceResults(pool)
			result.Candidates = copyCandidates(pool)
			return failedRun(result, planErr)
		}
		if progress != nil {
			sampled := budget.Sampled
			progress(Progress{Stage: "sampling", Family: family, Done: sampled, Total: &sampled, Unit: "IP", Message: fmt.Sprintf("初采阶段完成，实际新增 %d 个候选；累计总量待扩采结束后确定", sampled)})
		}
	}
	initial := make([]*Candidate, 0, len(pool.byIP))
	for _, candidate := range pool.byIP {
		initial = append(initial, candidate)
	}
	sort.Slice(initial, func(i, j int) bool { return compareCandidateIP(initial[i], initial[j]) < 0 })
	for _, family := range families {
		familyCandidates := filterFamily(initial, family)
		if err = engine.checkICMP(config, family, len(familyCandidates) > 0); err != nil {
			result.Sources = sourceResults(pool)
			markUnstartedCandidates(pool, err.Error())
			result.Candidates = copyCandidates(pool)
			return failedRun(result, err)
		}
	}
	if len(initial) == 0 {
		result.Sources = sourceResults(pool)
		return failedRun(result, fmt.Errorf("本轮全部来源均未提供已启用地址族的候选"))
	}
	if engine.Probe == nil {
		engine.Probe = probeCandidate
	}
	for _, family := range families {
		familyCandidates := filterFamily(initial, family)
		if err = probeCandidates(ctx, engine, config, familyCandidates, family, "probe", progress); err != nil {
			result.Sources = sourceResults(pool)
			if ctx.Err() != nil {
				markCancelledCandidates(pool)
			} else if errors.Is(err, ErrICMPUnavailable) {
				markUnstartedCandidates(pool, err.Error())
			}
			result.Candidates = copyCandidates(pool)
			return failedRun(result, err)
		}
	}
	for index := range result.Budgets {
		budget := &result.Budgets[index]
		for _, candidate := range pool.byIP {
			if candidate.Family == budget.Family && candidate.Origin != "expanded" && candidate.LatencyMS != nil {
				budget.ValidInitial++
			}
		}
		if budget.ExpansionLimit > 0 {
			budget.TriggerCount = (budget.ValidInitial + 9) / 10
		}
		expandFamilyWithProgress(config, pool, budget, progress)
		budget.Used = budget.Directed + budget.Sampled + budget.Expanded
		budget.Remaining = budget.Limit - budget.Used
		if len(budget.Expansions) == 0 && budget.Reason == "" {
			switch {
			case budget.ExpansionLimit == 0:
				budget.Reason = "累计预算已用完"
			case noSuccessfulInitial(pool, budget.Family):
				budget.Reason = "没有成功的初始延迟结果，未触发扩采"
			case !hasCIDRSource(pool, budget.Family):
				budget.Reason = "没有可用的网段来源可扩采"
			default:
				budget.Reason = "已检查命中网段；没有未测地址可追加"
			}
		}
	}
	all := make([]*Candidate, 0, len(pool.byIP))
	for _, candidate := range pool.byIP {
		all = append(all, candidate)
	}
	sort.Slice(all, func(i, j int) bool { return compareCandidateIP(all[i], all[j]) < 0 })
	for _, family := range families {
		expanded := make([]*Candidate, 0)
		for _, candidate := range all {
			if candidate.Family == family && candidate.Origin == "expanded" {
				expanded = append(expanded, candidate)
			}
		}
		if err = probeCandidates(ctx, engine, config, expanded, family, "expansion", progress); err != nil {
			result.Sources = sourceResults(pool)
			if ctx.Err() != nil {
				markCancelledCandidates(pool)
			} else if errors.Is(err, ErrICMPUnavailable) {
				markUnstartedCandidates(pool, err.Error())
			}
			result.Candidates = copyCandidates(pool)
			return failedRun(result, err)
		}
	}
	preselectDownloadsWithProgress(config, all, progress)
	sort.Slice(all, func(i, j int) bool { return compareFinal("latency", all[i], all[j]) < 0 })
	if config.DownloadEnabled {
		if engine.Download == nil {
			engine.Download = downloadCandidate
		}
		totals := make(map[string]int)
		done := make(map[string]int)
		counts := make(map[string]StageCounts)
		for _, candidate := range all {
			if candidate.State == "preselected" {
				totals[candidate.Family]++
			}
		}
		for _, candidate := range all {
			if candidate.State != "preselected" {
				continue
			}
			if err = ctx.Err(); err != nil {
				result.Sources = sourceResults(pool)
				markCancelledCandidates(pool)
				result.Candidates = copyCandidates(pool)
				return failedRun(result, err)
			}
			total := totals[candidate.Family]
			if progress != nil {
				progress(Progress{Stage: "download", Family: candidate.Family, Done: done[candidate.Family], Total: &total, Unit: "IP", Message: "正在下载测速：" + candidate.IP})
			}
			downloadErr := measureDownload(ctx, engine, config, candidate)
			done[candidate.Family]++
			counts[candidate.Family] = candidateCounts(counts[candidate.Family], candidate)
			if progress != nil {
				progress((Progress{Stage: "download", Family: candidate.Family, Done: done[candidate.Family], Total: &total, Unit: "IP", Message: "下载测速已处理：" + candidate.State + "；已处理不等于最终合格"}).WithCounts(counts[candidate.Family]))
			}
			if downloadErr != nil {
				result.Sources = sourceResults(pool)
				markCancelledCandidates(pool)
				result.Candidates = copyCandidates(pool)
				return failedRun(result, downloadErr)
			}
		}
	}
	for _, candidate := range all {
		candidate.Eligible = candidate.State == "passed"
	}
	if err := ctx.Err(); err != nil {
		markCancelledCandidates(pool)
		result.Candidates = copyCandidates(pool)
		result.Sources = sourceResults(pool)
		return failedRun(result, err)
	}
	if progress != nil {
		progress(Progress{Stage: "sorting", Unit: "IP", Message: "全部测量结束，按本次规则生成各地址族最终名次"})
	}
	sort.SliceStable(all, func(i, j int) bool { return compareFinal(config.Sort, all[i], all[j]) < 0 })
	ranks := make(map[string]int, 2)
	for _, candidate := range all {
		if candidate.Eligible {
			ranks[candidate.Family]++
			candidate.Rank = ranks[candidate.Family]
		}
	}
	if progress != nil {
		for _, family := range families {
			count := len(filterFamily(all, family))
			qualified := ranks[family]
			counts := StageCounts{}
			for _, candidate := range filterFamily(all, family) {
				counts = candidateCounts(counts, candidate)
			}
			progress((Progress{Stage: "sorting", Family: family, Done: count, Total: &count, Unit: "IP", Qualified: &qualified, Message: "最终排序已结束；只有合格候选拥有名次"}).WithCounts(counts))
		}
	}
	if err := ctx.Err(); err != nil {
		markCancelledCandidates(pool)
		result.Candidates = copyCandidates(pool)
		result.Sources = sourceResults(pool)
		return failedRun(result, err)
	}
	result.Sources = sourceResults(pool)
	result.Candidates = make([]Candidate, len(all))
	for index, candidate := range all {
		result.Candidates[index] = *candidate
	}
	if countEligible(all) == 0 {
		result.Status = "no_results"
		return result, nil
	}
	result.Status = "completed"
	return result, nil
}

func failedRun(result Result, err error) (Result, error) {
	result.Status = "failed"
	if errors.Is(err, context.Canceled) {
		result.Status = "cancelled"
		result.Error = "运行已取消"
		return result, fmt.Errorf("运行已取消: %w", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.Error = "运行上下文已超时"
		return result, fmt.Errorf("运行上下文已超时: %w", err)
	}
	result.Error = err.Error()
	return result, err
}

func (engine *Engine) checkICMP(config Config, family string, needed bool) error {
	if !needed || config.ProbeMode != "icmp" {
		return nil
	}
	if engine.ICMPCheck != nil {
		return engine.ICMPCheck(family)
	}
	if engine.Probe != nil {
		return nil
	}
	connection, _, _, _, err := openICMP(family)
	if err != nil {
		return err
	}
	if err := connection.Close(); err != nil {
		return fmt.Errorf("关闭 ICMP 检查 socket 失败: %w", err)
	}
	return nil
}

func sourceResults(pool *candidatePool) []SourceResult {
	results := make([]SourceResult, 0, len(pool.sources))
	ids := make([]string, 0, len(pool.sources))
	for id := range pool.sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		result := pool.sources[id].result
		result.CandidateCount = 0
		for _, candidate := range pool.byIP {
			for _, candidateSource := range candidate.Sources {
				if candidateSource == id {
					result.CandidateCount++
					break
				}
			}
		}
		results = append(results, result)
	}
	return results
}

func copyCandidates(pool *candidatePool) []Candidate {
	candidates := make([]Candidate, 0, len(pool.byIP))
	for _, candidate := range pool.byIP {
		copy := *candidate
		copy.Sources = append([]string(nil), candidate.Sources...)
		copy.Steps = append([]Decision(nil), candidate.Steps...)
		candidates = append(candidates, copy)
	}
	sort.Slice(candidates, func(i, j int) bool { return compareCandidateIP(&candidates[i], &candidates[j]) < 0 })
	return candidates
}

func filterFamily(candidates []*Candidate, family string) []*Candidate {
	filtered := make([]*Candidate, 0)
	for _, candidate := range candidates {
		if candidate.Family == family {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func compareCandidateIP(left, right *Candidate) int {
	leftIP, _ := netip.ParseAddr(left.IP)
	rightIP, _ := netip.ParseAddr(right.IP)
	return leftIP.Compare(rightIP)
}

func timeSeconds(seconds int) time.Duration { return time.Duration(seconds) * time.Second }

func countEligible(candidates []*Candidate) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.Eligible {
			count++
		}
	}
	return count
}

func noSuccessfulInitial(pool *candidatePool, family string) bool {
	for _, candidate := range pool.byIP {
		if candidate.Family == family && candidate.Origin != "expanded" && candidate.LatencyMS != nil {
			return false
		}
	}
	return true
}

func hasCIDRSource(pool *candidatePool, family string) bool {
	for _, source := range pool.sources {
		for _, prefix := range source.prefixes {
			if prefixFamily(prefix) == family {
				return true
			}
		}
	}
	return false
}

func expandFamily(config Config, pool *candidatePool, budget *Budget) {
	expandFamilyWithProgress(config, pool, budget, nil)
}

func expandFamilyWithProgress(config Config, pool *candidatePool, budget *Budget, progress func(Progress)) {
	if budget.ExpansionLimit <= 0 {
		if progress != nil {
			progress(Progress{Stage: "expansion", Family: budget.Family, Unit: "邻近范围", Message: "累计候选预算已用完，未执行扩采"})
		}
		return
	}
	type unit struct {
		prefix   netip.Prefix
		best     float64
		triggers []string
		sources  []string
		ranges   []netip.Prefix
		allowed  map[string][]netip.Prefix
	}
	triggered := make([]*Candidate, 0)
	for _, candidate := range pool.byIP {
		if candidate.Family == budget.Family && candidate.Origin != "expanded" && candidate.LatencyMS != nil {
			triggered = append(triggered, candidate)
		}
	}
	sort.Slice(triggered, func(i, j int) bool {
		if *triggered[i].LatencyMS != *triggered[j].LatencyMS {
			return *triggered[i].LatencyMS < *triggered[j].LatencyMS
		}
		return compareCandidateIP(triggered[i], triggered[j]) < 0
	})
	if len(triggered) == 0 {
		if progress != nil {
			total := 0
			progress(Progress{Stage: "expansion", Family: budget.Family, Done: 0, Total: &total, Unit: "邻近范围", Message: "没有有效初始延迟结果，未生成扩采范围"})
		}
		return
	}
	triggerCount := (len(triggered) + 9) / 10
	units := make(map[netip.Prefix]*unit)
	for _, candidate := range triggered[:triggerCount] {
		address, _ := netip.ParseAddr(candidate.IP)
		bits := 120
		if address.Is4() {
			bits = 24
		}
		neighborhood := netip.PrefixFrom(address, bits).Masked()
		current := units[neighborhood]
		if current == nil {
			current = &unit{prefix: neighborhood, best: *candidate.LatencyMS, allowed: make(map[string][]netip.Prefix)}
			units[neighborhood] = current
		}
		current.triggers = append(current.triggers, candidate.IP)
		for _, sourceID := range candidate.Sources {
			info := pool.sources[sourceID]
			if info.result.Source.Kind == "ip" || info.result.Source.Kind == "domain" {
				current.allowed[sourceID] = append(current.allowed[sourceID], neighborhood)
				current.ranges = append(current.ranges, neighborhood)
				appendUnique(&current.sources, sourceID)
				continue
			}
			for _, prefix := range info.prefixes {
				if prefix.Contains(address) {
					allowed := intersectPrefix(prefix, neighborhood)
					current.allowed[sourceID] = append(current.allowed[sourceID], allowed)
					current.ranges = append(current.ranges, allowed)
					appendUnique(&current.sources, sourceID)
				}
			}
		}
	}
	ordered := make([]*unit, 0, len(units))
	for _, current := range units {
		ordered = append(ordered, current)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].best != ordered[j].best {
			return ordered[i].best < ordered[j].best
		}
		return comparePrefix(ordered[i].prefix, ordered[j].prefix) < 0
	})
	unitTotal := len(ordered)
	if progress != nil {
		progress(Progress{Stage: "expansion", Family: budget.Family, Done: 0, Total: &unitTotal, Unit: "邻近范围", Message: fmt.Sprintf("已确定 %d 个扩采范围，按最佳初始延迟排序", unitTotal)})
	}
	for unitIndex, current := range ordered {
		current.ranges = uniquePrefixes(current.ranges)
		expansion := Expansion{
			Subnet:        current.prefix.String(),
			Triggers:      append([]string(nil), current.triggers...),
			Sources:       append([]string(nil), current.sources...),
			BestLatencyMS: current.best,
			Ranges:        prefixStrings(current.ranges),
		}
		if len(current.ranges) == 0 {
			expansion.Reason = "没有可用来源范围"
			budget.Expansions = append(budget.Expansions, expansion)
			if progress != nil {
				progress(Progress{Stage: "expansion", Family: budget.Family, Done: unitIndex + 1, Total: &unitTotal, Unit: "邻近范围", Message: current.prefix.String() + "：" + expansion.Reason})
			}
			continue
		}
		remaining := budget.ExpansionLimit - budget.Expanded
		if remaining == 0 {
			expansion.Reason = "累计预算不足；已优先扩采最佳延迟子网"
			budget.Expansions = append(budget.Expansions, expansion)
			if progress != nil {
				progress(Progress{Stage: "expansion", Family: budget.Family, Done: unitIndex + 1, Total: &unitTotal, Unit: "邻近范围", Message: current.prefix.String() + "：" + expansion.Reason})
			}
			continue
		}
		for _, allowed := range current.ranges {
			capacity := min(256, prefixCapacity(allowed, 256))
			for index := 0; index < capacity && expansion.Added < remaining; index++ {
				address, valid := addressAt(allowed, index)
				if !valid {
					continue
				}
				sources := []string{}
				for sourceID, ranges := range current.allowed {
					for _, sourceRange := range ranges {
						if sourceRange.Contains(address) {
							appendUnique(&sources, sourceID)
						}
					}
				}
				for sourceID, info := range pool.sources {
					if sourceContains(info, address) {
						appendUnique(&sources, sourceID)
					}
				}
				if existing := pool.byIP[address.String()]; existing != nil {
					for _, sourceID := range sources {
						appendUnique(&existing.Sources, sourceID)
					}
					continue
				}
				candidate := &Candidate{
					IP: address.String(), Family: budget.Family, Sources: sources,
					Origin: "expanded", State: "queued", Steps: []Decision{{Stage: "sampling", State: "expanded", Reason: fmt.Sprintf("从触发地址 %v 扩采，范围 %s；不是手动指定 IP", current.triggers, allowed)}},
				}
				pool.byIP[candidate.IP] = candidate
				expansion.Added++
			}
		}
		budget.Expanded += expansion.Added
		if expansion.Added == 0 {
			expansion.Reason = "命中范围中的地址均已测量或不可用"
		} else if budget.ExpansionLimit == budget.Expanded {
			expansion.Reason = "本轮剩余预算已用完"
		} else {
			expansion.Reason = "已扩采范围内未测地址"
		}
		budget.Expansions = append(budget.Expansions, expansion)
		if progress != nil {
			progress(Progress{Stage: "expansion", Family: budget.Family, Done: unitIndex + 1, Total: &unitTotal, Unit: "邻近范围", Message: current.prefix.String() + "：" + expansion.Reason})
		}
	}
	if len(ordered) == 0 {
		budget.Reason = "没有符合延迟排名的扩采触发地址"
	}
}

func addressAt(prefix netip.Prefix, offset int) (netip.Addr, bool) {
	base := prefix.Masked().Addr()
	bytes := new(big.Int).Add(new(big.Int).SetBytes(base.AsSlice()), big.NewInt(int64(offset))).Bytes()
	result := make([]byte, len(base.AsSlice()))
	if len(bytes) > len(result) {
		return netip.Addr{}, false
	}
	copy(result[len(result)-len(bytes):], bytes)
	if base.Is4() {
		var value [4]byte
		copy(value[:], result)
		address := netip.AddrFrom4(value)
		return address, prefix.Contains(address)
	}
	var value [16]byte
	copy(value[:], result)
	address := netip.AddrFrom16(value)
	return address, prefix.Contains(address)
}

func intersectPrefix(left, right netip.Prefix) netip.Prefix {
	if left.Bits() > right.Bits() {
		return left.Masked()
	}
	return right.Masked()
}

func uniquePrefixes(prefixes []netip.Prefix) []netip.Prefix {
	seen := make(map[netip.Prefix]bool)
	result := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		prefix = prefix.Masked()
		if !seen[prefix] {
			seen[prefix] = true
			result = append(result, prefix)
		}
	}
	sort.Slice(result, func(i, j int) bool { return comparePrefix(result[i], result[j]) < 0 })
	return result
}

func prefixStrings(prefixes []netip.Prefix) []string {
	values := make([]string, len(prefixes))
	for index, prefix := range prefixes {
		values[index] = prefix.String()
	}
	return values
}

func preselectDownloads(config Config, candidates []*Candidate) {
	preselectDownloadsWithProgress(config, candidates, nil)
}

func preselectDownloadsWithProgress(config Config, candidates []*Candidate, progress func(Progress)) {
	totals := map[string]int{}
	done := map[string]int{}
	counts := map[string]StageCounts{}
	for _, candidate := range candidates {
		totals[candidate.Family]++
	}
	for _, candidate := range candidates {
		if isPrefilterEligible(config, candidate) {
			candidate.Eligible = true
		}
		done[candidate.Family]++
		counts[candidate.Family] = candidateCounts(counts[candidate.Family], candidate)
		if progress != nil {
			total := totals[candidate.Family]
			progress((Progress{Stage: "filtering", Family: candidate.Family, Done: done[candidate.Family], Total: &total, Unit: "IP", Message: "已按快照完成本候选前置过滤判定；通过不代表最终合格"}).WithCounts(counts[candidate.Family]))
		}
	}
	if !config.DownloadEnabled {
		return
	}
	limits := map[string]int{"ipv4": config.DownloadIPv4Count, "ipv6": config.DownloadIPv6Count}
	for _, family := range []string{"ipv4", "ipv6"} {
		eligible := make([]*Candidate, 0)
		for _, candidate := range candidates {
			if candidate.Family == family && candidate.Eligible {
				eligible = append(eligible, candidate)
			}
		}
		sort.Slice(eligible, func(i, j int) bool {
			if *eligible[i].LatencyMS != *eligible[j].LatencyMS {
				return *eligible[i].LatencyMS < *eligible[j].LatencyMS
			}
			return compareCandidateIP(eligible[i], eligible[j]) < 0
		})
		selected := min(limits[family], len(eligible))
		for index, candidate := range eligible {
			if index < selected {
				candidate.State = "preselected"
				candidate.Eligible = false
				candidate.Steps = append(candidate.Steps, Decision{Stage: "download", State: "selected", Reason: "按延迟和 IP 数值顺序统一预选"})
				continue
			}
			candidate.Eligible = false
			candidate.State = "untested"
			candidate.Reason = "超过地址族下载测速名额，未测试"
			candidate.Steps = append(candidate.Steps, Decision{Stage: "download", State: "untested", Reason: candidate.Reason})
		}
	}
}

func isPrefilterEligible(config Config, candidate *Candidate) bool {
	passed := candidate.LatencyMS != nil
	if !passed && candidate.State != "failed" {
		candidate.State = "failed"
		candidate.Reason = "没有成功的延迟探测结果"
	}
	stages := append([]string(nil), config.FilterOrder...)
	for _, required := range []string{"latency", "loss", "download"} {
		found := false
		for _, stage := range stages {
			found = found || stage == required
		}
		if !found {
			stages = append(stages, required)
		}
	}
	for _, stage := range stages {
		if stage == "latency" && !config.LatencyEnabled || stage == "loss" && !config.LossEnabled || stage == "download" && !config.DownloadEnabled {
			candidate.Steps = append(candidate.Steps, Decision{Stage: stage, State: "disabled", Reason: "本次配置未启用此项过滤"})
			continue
		}
		if stage == "download" {
			if !passed {
				candidate.Steps = append(candidate.Steps, Decision{Stage: stage, State: "skipped", Reason: "前置测量或过滤未通过，未执行下载测速"})
			}
			continue
		}
		if !passed {
			candidate.Steps = append(candidate.Steps, Decision{Stage: stage, State: "skipped", Reason: "前置测量未通过，跳过后续筛选"})
			continue
		}
		var ok bool
		reason := ""
		switch stage {
		case "latency":
			ok = candidate.LatencyMS != nil && *candidate.LatencyMS <= config.LatencyMaxMS
			reason = "延迟超过配置门槛"
		case "loss":
			ok = candidate.LossPercent != nil && *candidate.LossPercent <= config.LossMaxPercent
			reason = "探测失败比例超过配置门槛"
		}
		if !ok {
			passed = false
			candidate.State = "filtered"
			candidate.Reason = reason
			candidate.Steps = append(candidate.Steps, Decision{Stage: stage, State: "filtered", Reason: reason})
		} else {
			candidate.Steps = append(candidate.Steps, Decision{Stage: stage, State: "passed", Reason: "达到配置门槛"})
		}
	}
	if passed {
		candidate.State = "passed"
		return true
	}
	return false
}

func compareFinal(sortMode string, left, right *Candidate) int {
	if left.Eligible != right.Eligible {
		if left.Eligible {
			return -1
		}
		return 1
	}
	if sortMode == "speed" && left.SpeedMBS != nil && right.SpeedMBS != nil && *left.SpeedMBS != *right.SpeedMBS {
		if *left.SpeedMBS > *right.SpeedMBS {
			return -1
		}
		return 1
	}
	if left.LatencyMS != nil && right.LatencyMS != nil && *left.LatencyMS != *right.LatencyMS {
		if *left.LatencyMS < *right.LatencyMS {
			return -1
		}
		return 1
	}
	if left.LatencyMS == nil && right.LatencyMS != nil {
		return 1
	}
	if left.LatencyMS != nil && right.LatencyMS == nil {
		return -1
	}
	if left.SpeedMBS != nil && right.SpeedMBS != nil && *left.SpeedMBS != *right.SpeedMBS {
		if *left.SpeedMBS > *right.SpeedMBS {
			return -1
		}
		return 1
	}
	if sortMode == "speed" && left.SpeedMBS != nil && right.SpeedMBS == nil {
		return -1
	}
	if sortMode == "speed" && left.SpeedMBS == nil && right.SpeedMBS != nil {
		return 1
	}
	return compareCandidateIP(left, right)
}

func validateSample(sample DownloadSample) error {
	if sample.Bytes <= 0 || sample.Seconds <= 0 || math.IsNaN(sample.Seconds) || math.IsInf(sample.Seconds, 0) {
		return fmt.Errorf("下载未取得有效正文数据或测量时长")
	}
	if sample.End != "completed" && sample.End != "window" {
		return fmt.Errorf("下载样本没有明确的正常结束状态")
	}
	if !finite(float64(sample.Bytes) / sample.Seconds / 1000000) {
		return fmt.Errorf("下载样本无法形成有限的平均速度")
	}
	return nil
}

func min(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
