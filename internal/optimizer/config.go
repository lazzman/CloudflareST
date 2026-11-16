package optimizer

import (
	"embed"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"strings"

	"github.com/masgzy/CloudflareST/internal/safedata"
)

//go:embed pkg/data/ipv4.txt pkg/data/ipv6.txt
var builtinFiles embed.FS

func Validate(config Config) error {
	if !config.IPv4Enabled && !config.IPv6Enabled {
		return fmt.Errorf("IPv4 和 IPv6 至少启用一个地址族")
	}
	for _, field := range []struct {
		name  string
		value int
	}{
		{"每段初采数量", config.SampleSize},
		{"IPv4 累计上限", config.IPv4Limit},
		{"IPv6 累计上限", config.IPv6Limit},
	} {
		if field.value < 1 || field.value > 100000 {
			return fmt.Errorf("%s 必须介于 1 和 100000 之间", field.name)
		}
	}
	if config.ProbeMode != "icmp" && config.ProbeMode != "tcp" {
		return fmt.Errorf("探测方式必须为 icmp 或 tcp")
	}
	if config.TCPPort < 1 || config.TCPPort > 65535 {
		return fmt.Errorf("TCP 端口必须介于 1 和 65535 之间")
	}
	if config.ProbeCount < 1 || config.ProbeCount > 100 {
		return fmt.Errorf("探测次数必须介于 1 和 100 之间")
	}
	if config.ProbeTimeoutMS < 100 || config.ProbeTimeoutMS > 10000 {
		return fmt.Errorf("单次探测等待时间必须介于 100 和 10000 毫秒之间")
	}
	if !finite(config.LatencyMaxMS) || config.LatencyMaxMS <= 0 {
		return fmt.Errorf("延迟门槛必须为大于零的有限数值")
	}
	if !finite(config.LossMaxPercent) || config.LossMaxPercent < 0 || config.LossMaxPercent > 100 {
		return fmt.Errorf("丢包率门槛必须介于 0 和 100 之间")
	}
	if !finite(config.SpeedMinMBS) || config.SpeedMinMBS < 0 {
		return fmt.Errorf("最低下载速度必须为非负有限数值")
	}
	if config.DownloadIPv4Count < 1 || config.DownloadIPv4Count > 100000 || config.DownloadIPv6Count < 1 || config.DownloadIPv6Count > 100000 {
		return fmt.Errorf("每个地址族的下载候选数量必须介于 1 和 100000 之间")
	}
	if config.DownloadEnabled && (config.IPv4Enabled && config.DownloadIPv4Count > config.IPv4Limit || config.IPv6Enabled && config.DownloadIPv6Count > config.IPv6Limit) {
		return fmt.Errorf("下载候选数量不能超过相应地址族的累计预算")
	}
	if config.DownloadWindowSeconds < 1 || config.DownloadWindowSeconds > 60 {
		return fmt.Errorf("正文下载窗口必须介于 1 和 60 秒之间")
	}
	if config.PrebodyTimeoutSeconds < 1 || config.PrebodyTimeoutSeconds > 60 {
		return fmt.Errorf("响应头等待时间必须介于 1 和 60 秒之间")
	}
	if config.Sort != "latency" && config.Sort != "speed" {
		return fmt.Errorf("排序方式必须为 latency 或 speed")
	}
	if !config.DownloadEnabled && config.Sort == "speed" {
		return fmt.Errorf("下载速度关闭时只能按延迟排序")
	}
	if err := validateFilterOrder(config); err != nil {
		return err
	}
	if _, err := parseDownloadURL(config.DownloadURL); err != nil {
		return err
	}
	for index, source := range config.Sources {
		if source.Kind != "builtin" && source.Kind != "cidr" && source.Kind != "ip" && source.Kind != "domain" {
			return fmt.Errorf("来源 %d 类型必须为 builtin、cidr、ip 或 domain", index+1)
		}
		if strings.TrimSpace(source.Value) == "" {
			return fmt.Errorf("来源 %d 的值不能为空", index+1)
		}
		if source.Count < 0 || source.Count > 100000 {
			return fmt.Errorf("来源 %d 的每段初采数量必须介于 0 和 100000 之间", index+1)
		}
		switch source.Kind {
		case "builtin":
			if source.Value != "ipv4" && source.Value != "ipv6" {
				return fmt.Errorf("来源 %d 的内置清单必须为 ipv4 或 ipv6", index+1)
			}
		case "cidr":
			if _, err := netip.ParsePrefix(source.Value); err != nil {
				return fmt.Errorf("来源 %d 的网段无效: %w", index+1, err)
			}
		case "ip":
			address, err := netip.ParseAddr(source.Value)
			if err != nil || address.Zone() != "" {
				return fmt.Errorf("来源 %d 的 IP 无效", index+1)
			}
		case "domain":
			if !validDomain(strings.TrimSuffix(source.Value, ".")) {
				return fmt.Errorf("来源 %d 的域名无效", index+1)
			}
		}
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validateFilterOrder(config Config) error {
	want := make(map[string]bool, 3)
	if config.LatencyEnabled {
		want["latency"] = true
	}
	if config.LossEnabled {
		want["loss"] = true
	}
	if config.DownloadEnabled {
		want["download"] = true
	}
	seen := make(map[string]bool, len(want))
	for index, stage := range config.FilterOrder {
		if stage != "latency" && stage != "loss" && stage != "download" || seen[stage] {
			return fmt.Errorf("过滤顺序包含未知或重复的项目")
		}
		if stage == "download" && index != len(config.FilterOrder)-1 {
			return fmt.Errorf("下载过滤必须排在最后")
		}
		seen[stage] = true
	}
	for stage := range want {
		if !seen[stage] {
			return fmt.Errorf("过滤顺序必须包含全部已启用的过滤项目")
		}
	}
	return nil
}

func validDomain(domain string) bool {
	if len(domain) == 0 || len(domain) > 253 {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func parseDownloadURL(value string) (*url.URL, error) {
	if safedata.URLHasSecrets(value) {
		return nil, fmt.Errorf("测速文件 URL 不允许包含认证 Token、密钥或签名参数；请使用不含秘密的文件地址")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("下载地址必须为无凭据、无片段的 HTTP 或 HTTPS 文件 URL")
	}
	if port := parsed.Port(); port != "" {
		for _, char := range port {
			if char < '0' || char > '9' {
				return nil, fmt.Errorf("下载地址端口无效")
			}
		}
		var number int
		if _, err := fmt.Sscan(port, &number); err != nil || number < 1 || number > 65535 {
			return nil, fmt.Errorf("下载地址端口必须介于 1 和 65535 之间")
		}
	}
	return parsed, nil
}
