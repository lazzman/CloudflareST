package optimizer

import "context"

func candidateCounts(counts StageCounts, candidate *Candidate) StageCounts {
	switch candidate.State {
	case "passed", "measured", "preselected":
		counts.Succeeded++
	case "filtered":
		counts.Filtered++
	case "failed":
		counts.Failed++
	default:
		counts.Skipped++
	}
	return counts
}

func measureDownload(ctx context.Context, engine *Engine, config Config, candidate *Candidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sample, downloadErr := engine.Download(ctx, DownloadRequest{
		IP:             candidate.IP,
		URL:            config.DownloadURL,
		Window:         timeSeconds(config.DownloadWindowSeconds),
		PrebodyTimeout: timeSeconds(config.PrebodyTimeoutSeconds),
	})
	candidate.DownloadBytes = sample.Bytes
	candidate.DownloadSeconds = sample.Seconds
	if ctx.Err() != nil {
		candidate.State = "cancelled"
		candidate.DownloadEnd = "cancelled"
		candidate.Reason = "运行已取消，下载测量未完成"
		candidate.Steps = append(candidate.Steps, Decision{Stage: "download", State: "cancelled", Reason: candidate.Reason})
		return ctx.Err()
	}
	if downloadErr == nil {
		downloadErr = validateSample(sample)
	}
	if downloadErr != nil {
		candidate.State = "failed"
		candidate.DownloadEnd = "failed"
		candidate.Reason = "下载测速失败: " + downloadErr.Error()
		candidate.Steps = append(candidate.Steps, Decision{Stage: "download", State: "failed", Reason: candidate.Reason})
		return nil
	}
	candidate.DownloadEnd = sample.End
	candidate.SpeedMBS = floatPointer(float64(sample.Bytes) / sample.Seconds / 1000000)
	if *candidate.SpeedMBS < config.SpeedMinMBS {
		candidate.State = "filtered"
		candidate.Reason = "下载速度低于配置门槛"
		candidate.Steps = append(candidate.Steps, Decision{Stage: "download", State: "filtered", Reason: candidate.Reason})
	} else {
		candidate.State = "passed"
		candidate.Eligible = true
		candidate.Steps = append(candidate.Steps, Decision{Stage: "download", State: "passed", Reason: "下载速度达到配置门槛"})
	}
	return nil
}

func markUnstartedCandidates(pool *candidatePool, reason string) {
	for _, candidate := range pool.byIP {
		switch candidate.State {
		case "queued":
			candidate.Reason = "ICMP 不可用，未执行延迟探测: " + reason
		case "cancelled":
			candidate.Reason = "ICMP 不可用，延迟探测已停止且未完成: " + reason
		case "measured":
			candidate.Reason = "ICMP 不可用，已完成基础探测但后续筛选未执行: " + reason
		default:
			continue
		}
		candidate.State = "untested"
		candidate.Steps = append(candidate.Steps, Decision{Stage: "probe", State: "skipped", Reason: candidate.Reason})
	}
}

func markCancelledCandidates(pool *candidatePool) {
	for _, candidate := range pool.byIP {
		stage := "probe"
		switch candidate.State {
		case "queued", "measured":
			candidate.Reason = "运行已取消，探测或后续筛选未完成"
		case "preselected":
			stage = "download"
			candidate.Reason = "运行已取消，未开始下载测量"
		default:
			continue
		}
		candidate.State = "cancelled"
		candidate.Eligible = false
		candidate.Steps = append(candidate.Steps, Decision{Stage: stage, State: "skipped", Reason: candidate.Reason})
	}
}
