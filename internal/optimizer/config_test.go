package optimizer

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestDefaultConfigAndJSON(t *testing.T) {
	config := DefaultConfig()
	if err := Validate(config); err != nil {
		t.Fatal(err)
	}
	if !config.IPv4Enabled || config.IPv6Enabled || config.ProbeMode != "icmp" || config.SampleSize != 256 || config.IPv4Limit != 10000 || config.IPv6Limit != 10000 || config.DownloadURL != DefaultDownloadURL {
		t.Fatalf("默认配置不符合约定: %+v", config)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ipv4_enabled", "ipv6_enabled", "probe_timeout_ms", "download_ipv4_count", "download_ipv6_count", "speed_min_mb_s", "prebody_timeout_seconds"} {
		if !strings.Contains(string(encoded), name) {
			t.Fatalf("缺少 JSON 字段 %s", name)
		}
	}
	for _, family := range []string{"ipv4", "ipv6"} {
		content, err := builtinFiles.ReadFile("pkg/data/" + family + ".txt")
		if err != nil || len(strings.Fields(string(content))) == 0 {
			t.Fatalf("内置清单不可用: %s, %v", family, err)
		}
	}
}

func TestValidateBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Config)
		valid  bool
	}{
		{"等待不足", func(config *Config) { config.ProbeTimeoutMS = 99 }, false},
		{"等待下界", func(config *Config) { config.ProbeTimeoutMS = 100 }, true},
		{"等待上界", func(config *Config) { config.ProbeTimeoutMS = 10000 }, true},
		{"等待超限", func(config *Config) { config.ProbeTimeoutMS = 10001 }, false},
		{"下载为零", func(config *Config) { config.DownloadIPv4Count = 0 }, false},
		{"IPv4下载超预算", func(config *Config) { config.IPv4Limit = 19 }, false},
		{"IPv6下载超预算", func(config *Config) { config.IPv6Enabled = true; config.IPv6Limit = 19 }, false},
		{"下载等于预算", func(config *Config) { config.IPv4Limit = 20 }, true},
		{"关闭族不占预算", func(config *Config) { config.IPv6Limit = 1 }, true},
		{"关闭下载保留数量", func(config *Config) { config.DownloadEnabled = false; config.IPv4Limit = 1 }, true},
		{"关闭下载禁速度排序", func(config *Config) { config.DownloadEnabled = false; config.Sort = "speed" }, false},
		{"关闭过滤保留顺序", func(config *Config) {
			config.LatencyEnabled = false
			config.LossEnabled = false
			config.DownloadEnabled = false
		}, true},
		{"下载不能前移", func(config *Config) { config.FilterOrder = []string{"download", "loss", "latency"} }, false},
		{"无地址族", func(config *Config) { config.IPv4Enabled = false }, false},
		{"数量为零", func(config *Config) { config.SampleSize = 0 }, false},
		{"预算超上限", func(config *Config) { config.IPv4Limit = 100001 }, false},
		{"延迟为零", func(config *Config) { config.LatencyMaxMS = 0 }, false},
		{"延迟负数", func(config *Config) { config.LatencyMaxMS = -1 }, false},
		{"非数延迟", func(config *Config) { config.LatencyMaxMS = math.NaN() }, false},
		{"负无限延迟", func(config *Config) { config.LatencyMaxMS = math.Inf(-1) }, false},
		{"延迟原页面上限", func(config *Config) { config.LatencyMaxMS = 60000 }, true},
		{"延迟超过原页面上限", func(config *Config) { config.LatencyMaxMS = 60001 }, true},
		{"最大有限延迟", func(config *Config) { config.LatencyMaxMS = math.MaxFloat64 }, true},
		{"非数速度", func(config *Config) { config.SpeedMinMBS = math.NaN() }, false},
		{"无限延迟", func(config *Config) { config.LatencyMaxMS = math.Inf(1) }, false},
		{"丢包超限", func(config *Config) { config.LossMaxPercent = 100.01 }, false},
		{"正文窗口超限", func(config *Config) { config.DownloadWindowSeconds = 61 }, false},
		{"前置窗口超限", func(config *Config) { config.PrebodyTimeoutSeconds = 61 }, false},
		{"无效下载协议", func(config *Config) { config.DownloadURL = "file:///tmp/file" }, false},
		{"无效下载端口", func(config *Config) { config.DownloadURL = "https://example.invalid:65536/file" }, false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			config := DefaultConfig()
			item.change(&config)
			if err := Validate(config); (err == nil) != item.valid {
				t.Fatalf("有效性应为 %v，实际错误: %v", item.valid, err)
			}
		})
	}
}
