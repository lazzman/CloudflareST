package optimizer

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sourceInfo struct {
	result   SourceResult
	prefixes []netip.Prefix
}

type candidatePool struct {
	byIP    map[string]*Candidate
	sources map[string]*sourceInfo
}

func (engine *Engine) collectSources(ctx context.Context, config Config) (*candidatePool, error) {
	return engine.collectSourcesWithProgress(ctx, config, nil)
}

func (engine *Engine) collectSourcesWithProgress(ctx context.Context, config Config, progress func(Progress)) (*candidatePool, error) {
	pool := &candidatePool{byIP: make(map[string]*Candidate), sources: make(map[string]*sourceInfo)}
	for index, source := range config.Sources {
		id := fmt.Sprintf("source-%03d", index+1)
		pool.sources[id] = &sourceInfo{result: SourceResult{ID: id, Source: source, Status: "pending"}}
	}
	defer func() {
		for id, info := range pool.sources {
			if ctx.Err() != nil {
				switch info.result.Status {
				case "pending":
					info.result.Status = "cancelled"
					info.result.Error = "运行已取消，未开始处理来源"
				case "processing":
					info.result.Status = "cancelled"
					info.result.Error = "运行已取消，来源处理未完成"
				}
			}
			for addressText, candidate := range pool.byIP {
				address, _ := netip.ParseAddr(addressText)
				if sourceContains(info, address) {
					appendUnique(&candidate.Sources, id)
				}
			}
		}
	}()
	total := len(config.Sources)
	counts := StageCounts{}
	if progress != nil {
		progress(Progress{Stage: "sources", Done: 0, Total: &total, Unit: "来源项", Message: "按本轮实际来源解析，不读取历史IP作回退"})
	}
	for index, source := range config.Sources {
		if err := ctx.Err(); err != nil {
			return pool, err
		}
		id := fmt.Sprintf("source-%03d", index+1)
		info := pool.sources[id]
		info.result.Status = "processing"
		switch source.Kind {
		case "builtin":
			if source.Value == "ipv4" && !config.IPv4Enabled || source.Value == "ipv6" && !config.IPv6Enabled {
				info.result.Status = "skipped"
				info.result.Error = "来源地址族未启用"
				break
			}
			data, err := builtinFiles.ReadFile("pkg/data/" + source.Value + ".txt")
			if err != nil {
				info.result.Status = "failed"
				info.result.Error = "读取内置地址清单失败: " + err.Error()
				return pool, fmt.Errorf("读取内置地址清单失败: %w", err)
			}
			for _, line := range strings.Fields(string(data)) {
				prefix, err := netip.ParsePrefix(line)
				if err == nil {
					info.prefixes = append(info.prefixes, normalizePrefix(prefix))
				}
			}
		case "cidr":
			prefix, _ := netip.ParsePrefix(source.Value)
			info.prefixes = append(info.prefixes, normalizePrefix(prefix))
		case "ip":
			address, _ := netip.ParseAddr(source.Value)
			if familyEnabled(address.Unmap(), config) {
				engine.addCandidate(pool, address, id, "direct")
			} else {
				info.result.Status = "skipped"
				info.result.Error = "来源地址族未启用"
			}
		case "domain":
			resolver := engine.Resolve
			if resolver == nil {
				resolver = func(ctx context.Context, domain string) ([]netip.Addr, error) {
					return net.DefaultResolver.LookupNetIP(ctx, "ip", domain)
				}
			}
			addresses, err := resolver(ctx, source.Value)
			if err != nil && ctx.Err() == nil {
				info.result.Status = "failed"
				info.result.Error = "域名解析失败: " + err.Error()
				break
			}
			for _, address := range addresses {
				if address.IsValid() && address.Zone() == "" {
					address = address.Unmap()
					info.result.ResolvedIPs = append(info.result.ResolvedIPs, address.String())
					if familyEnabled(address, config) {
						engine.addCandidate(pool, address, id, "direct")
					}
				}
			}
		}
		for _, prefix := range info.prefixes {
			if familyEnabled(prefix.Addr(), config) {
				info.result.CIDRs = append(info.result.CIDRs, prefix.String())
			}
		}
		if err := ctx.Err(); err != nil {
			return pool, err
		}
		if len(info.prefixes) > 0 && len(info.result.CIDRs) == 0 {
			info.result.Status = "skipped"
			info.result.Error = "来源地址族未启用"
		}
		if info.result.Status == "processing" {
			info.result.Status = "completed"
		}
		if progress != nil {
			switch info.result.Status {
			case "completed":
				counts.Succeeded++
			case "failed":
				counts.Failed++
			default:
				counts.Skipped++
			}
			progress((Progress{Stage: "sources", Done: index + 1, Total: &total, Unit: "来源项", Message: "本来源处理结论：" + info.result.Status}).WithCounts(counts))
		}
	}
	return pool, ctx.Err()
}

func normalizePrefix(prefix netip.Prefix) netip.Prefix {
	if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	return prefix.Masked()
}

func (engine *Engine) addCandidate(pool *candidatePool, address netip.Addr, sourceID, origin string) *Candidate {
	address = address.Unmap()
	key := address.String()
	if existing, found := pool.byIP[key]; found {
		appendUnique(&existing.Sources, sourceID)
		if origin == "direct" {
			existing.Origin = "direct"
		}
		return existing
	}
	family := "ipv6"
	if address.Is4() {
		family = "ipv4"
	}
	candidate := &Candidate{IP: key, Family: family, Sources: []string{sourceID}, Origin: origin, State: "queued", Steps: []Decision{}}
	pool.byIP[key] = candidate
	return candidate
}

func familyEnabled(address netip.Addr, config Config) bool {
	address = address.Unmap()
	return address.Is4() && config.IPv4Enabled || address.Is6() && config.IPv6Enabled
}

func sourceContains(info *sourceInfo, address netip.Addr) bool {
	address = address.Unmap()
	if info.result.Source.Kind == "ip" {
		sourceAddress, err := netip.ParseAddr(info.result.Source.Value)
		return err == nil && sourceAddress.Unmap() == address
	}
	for _, resolved := range info.result.ResolvedIPs {
		if resolved == address.String() {
			return true
		}
	}
	for _, prefix := range info.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func appendUnique(values *[]string, value string) {
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
	sort.Strings(*values)
}

func directedBudget(config Config, pool *candidatePool, family string) (Budget, error) {
	budget := Budget{Family: family, Limit: familyLimit(config, family), Expansions: []Expansion{}}
	for _, candidate := range pool.byIP {
		if candidate.Family == family && candidate.Origin == "direct" {
			budget.Directed++
		}
	}
	if budget.Directed > budget.Limit {
		return budget, fmt.Errorf("%s 定向候选有 %d 个，超过累计上限 %d；已停止，不会截断候选", family, budget.Directed, budget.Limit)
	}
	return budget, nil
}

func planInitial(config Config, pool *candidatePool, family string) (Budget, error) {
	budget, err := directedBudget(config, pool, family)
	if err != nil {
		return budget, err
	}
	var prefixes []netip.Prefix
	capacity := make(map[netip.Prefix]int)
	requestedCounts := make(map[netip.Prefix]int)
	remaining := budget.Limit - budget.Directed
	if remaining > 0 {
		budget.InitialLimit = remaining * 4 / 5
		if budget.InitialLimit < 1 {
			budget.InitialLimit = 1
		}
		if budget.InitialLimit > remaining {
			budget.InitialLimit = remaining
		}
	}
	for _, info := range pool.sources {
		for _, prefix := range info.prefixes {
			if prefixFamily(prefix) != family {
				continue
			}
			prefix = prefix.Masked()
			requested := info.result.Source.Count
			if requested == 0 {
				requested = config.SampleSize
			}
			if requested > requestedCounts[prefix] {
				requestedCounts[prefix] = requested
			}
			if existing, found := capacity[prefix]; found {
				if requested > existing {
					capacity[prefix] = prefixCapacity(prefix, requested)
				}
				continue
			}
			capacity[prefix] = min(requested, prefixCapacity(prefix, requested))
			prefixes = append(prefixes, prefix)
		}
	}
	sort.Slice(prefixes, func(i, j int) bool { return comparePrefix(prefixes[i], prefixes[j]) < 0 })
	for _, prefix := range prefixes {
		budget.RequestedInitial += requestedCounts[prefix]
	}
	quotas := allocateQuotas(prefixes, capacity, budget.InitialLimit)
	for _, quota := range quotas {
		budget.InitialAllocated += quota
	}
	remaining = budget.InitialLimit
	for remaining > 0 {
		added := 0
		for _, prefix := range prefixes {
			want := quotas[prefix]
			addresses := samplePrefix(prefix, pool.byIP, want)
			for _, address := range addresses {
				for id, info := range pool.sources {
					if sourceContains(info, address) {
						for _, candidatePrefix := range info.prefixes {
							if candidatePrefix.Contains(address) {
								engineCandidateAdd(pool, address, id, "sampled")
								break
							}
						}
					}
				}
				engineCandidateAdd(pool, address, sourceIDForPrefix(pool, prefix), "sampled")
			}
			added += len(addresses)
			capacity[prefix] -= len(addresses)
			if len(addresses) < want {
				capacity[prefix] = 0
			}
		}
		remaining -= added
		quotas = allocateQuotas(prefixes, capacity, remaining)
		allocated := 0
		for _, quota := range quotas {
			allocated += quota
		}
		if allocated == 0 {
			break
		}
	}
	for _, candidate := range pool.byIP {
		if candidate.Family == family && candidate.Origin == "sampled" {
			budget.Sampled++
		}
	}
	budget.ExpansionLimit = budget.Limit - budget.Directed - budget.Sampled
	budget.Used = budget.Directed + budget.Sampled
	budget.Remaining = budget.Limit - budget.Used
	if budget.Sampled < budget.RequestedInitial {
		budget.InitialReason = "初采按预算、来源容量与去重缩减；未实际使用名额可供单轮扩采"
	} else {
		budget.InitialReason = "已按所选来源的有效请求数生成初采候选，不承诺用满预算"
	}
	return budget, nil
}

func engineCandidateAdd(pool *candidatePool, address netip.Addr, sourceID, origin string) {
	if !address.IsValid() || address.Is4In6() {
		return
	}
	key := address.String()
	if existing, found := pool.byIP[key]; found {
		appendUnique(&existing.Sources, sourceID)
		return
	}
	family := "ipv6"
	if address.Is4() {
		family = "ipv4"
	}
	pool.byIP[key] = &Candidate{IP: key, Family: family, Sources: []string{sourceID}, Origin: origin, State: "queued", Steps: []Decision{}}
}

func sourceIDForPrefix(pool *candidatePool, prefix netip.Prefix) string {
	ids := make([]string, 0)
	for id, info := range pool.sources {
		for _, candidate := range info.prefixes {
			if candidate.Masked() == prefix {
				ids = append(ids, id)
				break
			}
		}
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func familyLimit(config Config, family string) int {
	if family == "ipv4" {
		return config.IPv4Limit
	}
	return config.IPv6Limit
}

func prefixFamily(prefix netip.Prefix) string {
	if prefix.Addr().Is4() {
		return "ipv4"
	}
	return "ipv6"
}

func prefixCapacity(prefix netip.Prefix, cap int) int {
	bits := prefix.Addr().BitLen() - prefix.Bits()
	if bits >= strconv.IntSize-1 {
		return cap
	}
	return min(cap, 1<<bits)
}

func allocateQuotas(prefixes []netip.Prefix, capacities map[netip.Prefix]int, limit int) map[netip.Prefix]int {
	quotas := make(map[netip.Prefix]int, len(prefixes))
	remaining := limit
	for remaining > 0 {
		var active []netip.Prefix
		for _, prefix := range prefixes {
			if quotas[prefix] < capacities[prefix] {
				active = append(active, prefix)
			}
		}
		if len(active) == 0 {
			break
		}
		share := max(1, remaining/len(active))
		for _, prefix := range active {
			allocation := min(share, capacities[prefix]-quotas[prefix], remaining)
			quotas[prefix] += allocation
			remaining -= allocation
			if remaining == 0 {
				break
			}
		}
	}
	return quotas
}

func samplePrefix(prefix netip.Prefix, existing map[string]*Candidate, count int) []netip.Addr {
	if count <= 0 {
		return nil
	}
	prefix = normalizePrefix(prefix)
	base := prefix.Addr()
	baseValue := new(big.Int).SetBytes(base.AsSlice())
	totalBits := base.BitLen() - prefix.Bits()
	limit := new(big.Int).Lsh(big.NewInt(1), uint(totalBits))
	mappedStart := netip.MustParseAddr("::ffff:0.0.0.0")
	mappedSize := new(big.Int).Lsh(big.NewInt(1), 32)
	var mappedOffset *big.Int
	if base.Is6() && prefix.Contains(mappedStart) {
		mappedOffset = new(big.Int).Sub(new(big.Int).SetBytes(mappedStart.AsSlice()), baseValue)
		limit.Sub(limit, mappedSize)
	}
	result := make([]netip.Addr, 0, count)
	seed := int64(1469598103934665603)
	for _, value := range prefix.String() {
		seed = (seed ^ int64(value)) * 1099511628211
	}
	random := rand.New(rand.NewSource(seed))
	addOffset := func(offset *big.Int) {
		addressValue := new(big.Int).Add(baseValue, offset)
		if mappedOffset != nil && offset.Cmp(mappedOffset) >= 0 {
			addressValue.Add(addressValue, mappedSize)
		}
		bytes := addressValue.Bytes()
		size := len(base.AsSlice())
		addressBytes := make([]byte, size)
		copy(addressBytes[size-len(bytes):], bytes)
		var address netip.Addr
		if base.Is4() {
			var value [4]byte
			copy(value[:], addressBytes)
			address = netip.AddrFrom4(value)
		} else {
			var value [16]byte
			copy(value[:], addressBytes)
			address = netip.AddrFrom16(value)
		}
		key := address.String()
		if existing[key] == nil && !address.Is4In6() && prefix.Contains(address) {
			result = append(result, address)
		}
	}
	start := new(big.Int).Rand(random, limit)
	step := new(big.Int).Rand(random, limit)
	step.SetBit(step, 0, 1)
	if mappedOffset != nil {
		divisor := new(big.Int)
		for divisor.GCD(nil, nil, step, limit).Cmp(big.NewInt(1)) != 0 {
			step.Add(step, big.NewInt(2))
		}
	}
	offset := new(big.Int).Set(start)
	for attempt := 0; attempt < len(existing)+count && len(result) < count; attempt++ {
		addOffset(offset)
		offset.Add(offset, step).Mod(offset, limit)
		if offset.Cmp(start) == 0 {
			break
		}
	}
	return result
}

func comparePrefix(left, right netip.Prefix) int {
	if comparison := left.Addr().Compare(right.Addr()); comparison != 0 {
		return comparison
	}
	if left.Bits() < right.Bits() {
		return -1
	}
	if left.Bits() > right.Bits() {
		return 1
	}
	return 0
}

func probeCandidates(ctx context.Context, engine *Engine, config Config, candidates []*Candidate, family, stage string, progress func(Progress)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	total := len(candidates)
	phase := "初始候选"
	if stage == "expansion" {
		phase = "扩采候选"
	}
	if progress != nil {
		progress(Progress{Stage: stage, Family: family, Done: 0, Total: &total, Unit: "IP", Message: phase + "延迟探测开始；仅为本阶段已知总量"})
	}
	if total == 0 {
		return ctx.Err()
	}
	workers := engine.Concurrency
	if workers <= 0 {
		workers = 32
	}
	workers = min(workers, len(candidates), 128)
	jobs := make(chan *Candidate)
	completed := make(chan *Candidate, workers)
	fatal := make(chan error, 1)
	workerCtx, stop := context.WithCancel(ctx)
	defer stop()
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer group.Done()
			for candidate := range jobs {
				if workerCtx.Err() != nil {
					continue
				}
				if err := measureCandidate(workerCtx, engine, config, candidate); errors.Is(err, ErrICMPUnavailable) {
					select {
					case fatal <- err:
					default:
					}
					stop()
				}
				completed <- candidate
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, candidate := range candidates {
			select {
			case jobs <- candidate:
			case <-workerCtx.Done():
				return
			}
		}
	}()
	go func() {
		group.Wait()
		close(completed)
	}()
	done := 0
	counts := StageCounts{}
	for candidate := range completed {
		done++
		switch candidate.State {
		case "measured":
			counts.Succeeded++
		case "failed":
			counts.Failed++
		default:
			counts.Skipped++
		}
		if progress != nil {
			progress((Progress{Stage: stage, Family: family, Done: done, Total: &total, Unit: "IP", Message: phase + "延迟探测已处理：" + candidate.State + "；已处理不等于最终合格"}).WithCounts(counts))
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case err := <-fatal:
		return err
	default:
	}
	return nil
}

func measureCandidate(ctx context.Context, engine *Engine, config Config, candidate *Candidate) error {
	var samples []float64
	for attempt := 0; attempt < config.ProbeCount; attempt++ {
		if ctx.Err() != nil {
			candidate.State = "cancelled"
			candidate.Reason = "运行已取消"
			return ctx.Err()
		}
		candidate.ProbeAttempts++
		probeCtx, cancel := context.WithTimeout(ctx, time.Duration(config.ProbeTimeoutMS)*time.Millisecond)
		elapsed, err := engine.Probe(probeCtx, ProbeRequest{IP: candidate.IP, Mode: config.ProbeMode, Port: config.TCPPort, Timeout: time.Duration(config.ProbeTimeoutMS) * time.Millisecond})
		if probeCtx.Err() != nil {
			err = probeCtx.Err()
		}
		cancel()
		if errors.Is(err, ErrICMPUnavailable) {
			candidate.ProbeFailures++
			candidate.State = "failed"
			candidate.Reason = err.Error()
			candidate.Steps = append(candidate.Steps, Decision{Stage: "probe", State: "failed", Reason: candidate.Reason})
			return err
		}
		if err != nil || elapsed < 0 {
			candidate.ProbeFailures++
			if err != nil {
				candidate.Reason = "探测失败: " + err.Error()
			} else {
				candidate.Reason = "探测返回无效耗时"
			}
			continue
		}
		if elapsed == 0 {
			candidate.ProbeFailures++
			candidate.Reason = "探测返回非正耗时"
			continue
		}
		samples = append(samples, float64(elapsed)/float64(time.Millisecond))
		candidate.ProbeSuccesses++
	}
	if ctx.Err() != nil {
		candidate.State = "cancelled"
		candidate.Reason = "运行已取消"
		return ctx.Err()
	}
	candidate.LossPercent = floatPointer(float64(candidate.ProbeFailures) * 100 / float64(config.ProbeCount))
	if len(samples) == 0 {
		candidate.State = "failed"
		if candidate.Reason == "" {
			candidate.Reason = "全部延迟探测失败"
		}
		candidate.Steps = append(candidate.Steps, Decision{Stage: "probe", State: "failed", Reason: candidate.Reason})
		return nil
	}
	sort.Float64s(samples)
	median := samples[len(samples)/2]
	if len(samples)%2 == 0 {
		median = (samples[len(samples)/2-1] + samples[len(samples)/2]) / 2
	}
	candidate.LatencyMS = floatPointer(median)
	candidate.State = "measured"
	if candidate.ProbeFailures > 0 {
		candidate.Reason = fmt.Sprintf("延迟探测成功 %d 次，失败 %d 次，共 %d 次", candidate.ProbeSuccesses, candidate.ProbeFailures, candidate.ProbeAttempts)
	}
	return nil
}

func floatPointer(value float64) *float64 { return &value }
