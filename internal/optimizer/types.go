package optimizer

import (
	"context"
	"net/netip"
	"time"
)

const DefaultDownloadURL = "https://download.parallels.com/desktop/v15/15.1.5-47309/ParallelsDesktop-15.1.5-47309.dmg"

type Config struct {
	IPv4Enabled           bool     `json:"ipv4_enabled"`
	IPv6Enabled           bool     `json:"ipv6_enabled"`
	Sources               []Source `json:"sources"`
	SampleSize            int      `json:"sample_size"`
	IPv4Limit             int      `json:"ipv4_limit"`
	IPv6Limit             int      `json:"ipv6_limit"`
	ProbeMode             string   `json:"probe_mode"`
	TCPPort               int      `json:"tcp_port"`
	ProbeCount            int      `json:"probe_count"`
	ProbeTimeoutMS        int      `json:"probe_timeout_ms"`
	LatencyEnabled        bool     `json:"latency_enabled"`
	LossEnabled           bool     `json:"loss_enabled"`
	DownloadEnabled       bool     `json:"download_enabled"`
	LatencyMaxMS          float64  `json:"latency_max_ms"`
	LossMaxPercent        float64  `json:"loss_max_percent"`
	SpeedMinMBS           float64  `json:"speed_min_mb_s"`
	FilterOrder           []string `json:"filter_order"`
	Sort                  string   `json:"sort"`
	DownloadIPv4Count     int      `json:"download_ipv4_count"`
	DownloadIPv6Count     int      `json:"download_ipv6_count"`
	DownloadWindowSeconds int      `json:"download_window_seconds"`
	PrebodyTimeoutSeconds int      `json:"prebody_timeout_seconds"`
	DownloadURL           string   `json:"download_url"`
}

type Source struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Count int    `json:"count"`
}

type Result struct {
	Status     string         `json:"status"`
	Candidates []Candidate    `json:"candidates"`
	Sources    []SourceResult `json:"sources"`
	Budgets    []Budget       `json:"budgets"`
	Error      string         `json:"error"`
}

type Candidate struct {
	IP              string     `json:"ip"`
	Family          string     `json:"family"`
	Sources         []string   `json:"sources"`
	LatencyMS       *float64   `json:"latency_ms"`
	LossPercent     *float64   `json:"loss_percent"`
	SpeedMBS        *float64   `json:"speed_mb_s"`
	Eligible        bool       `json:"eligible"`
	Rank            int        `json:"rank"`
	State           string     `json:"state"`
	Reason          string     `json:"reason"`
	Steps           []Decision `json:"steps"`
	DownloadBytes   int64      `json:"download_bytes"`
	DownloadSeconds float64    `json:"download_seconds"`
	DownloadEnd     string     `json:"download_end"`
	Origin          string     `json:"origin"`
	ProbeAttempts   int        `json:"probe_attempts"`
	ProbeSuccesses  int        `json:"probe_successes"`
	ProbeFailures   int        `json:"probe_failures"`
}

type Decision struct {
	Stage  string `json:"stage"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type SourceResult struct {
	ID             string   `json:"id"`
	Source         Source   `json:"source"`
	Status         string   `json:"status"`
	Error          string   `json:"error"`
	ResolvedIPs    []string `json:"resolved_ips"`
	CIDRs          []string `json:"cidrs"`
	CandidateCount int      `json:"candidate_count"`
}

type Budget struct {
	RequestedInitial int         `json:"requested_initial"`
	ValidInitial     int         `json:"valid_initial"`
	TriggerCount     int         `json:"trigger_count"`
	Remaining        int         `json:"remaining"`
	InitialReason    string      `json:"initial_reason"`
	InitialAllocated int         `json:"initial_allocated"`
	Family           string      `json:"family"`
	Limit            int         `json:"limit"`
	Directed         int         `json:"directed"`
	InitialLimit     int         `json:"initial_limit"`
	Sampled          int         `json:"sampled"`
	ExpansionLimit   int         `json:"expansion_limit"`
	Expanded         int         `json:"expanded"`
	Used             int         `json:"used"`
	Reason           string      `json:"reason"`
	Expansions       []Expansion `json:"expansions"`
}

type Expansion struct {
	Subnet        string   `json:"subnet"`
	Ranges        []string `json:"ranges"`
	Triggers      []string `json:"triggers"`
	Sources       []string `json:"sources"`
	BestLatencyMS float64  `json:"best_latency_ms"`
	Added         int      `json:"added"`
	Reason        string   `json:"reason"`
}

type Progress struct {
	Stage     string       `json:"stage"`
	Family    string       `json:"family"`
	Done      int          `json:"done"`
	Total     *int         `json:"total"`
	Unit      string       `json:"unit"`
	Message   string       `json:"message"`
	Counts    *StageCounts `json:"counts,omitempty"`
	Qualified *int         `json:"qualified,omitempty"`
}

type StageCounts struct {
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
	Filtered    int `json:"filtered"`
	Unconfirmed int `json:"unconfirmed"`
}

func (progress Progress) WithCounts(counts StageCounts) Progress {
	progress.Counts = &counts
	return progress
}

type ProbeRequest struct {
	IP      string        `json:"ip"`
	Mode    string        `json:"mode"`
	Port    int           `json:"port"`
	Timeout time.Duration `json:"timeout"`
}

type DownloadRequest struct {
	IP             string        `json:"ip"`
	URL            string        `json:"url"`
	Window         time.Duration `json:"window"`
	PrebodyTimeout time.Duration `json:"prebody_timeout"`
}

type DownloadSample struct {
	Bytes   int64   `json:"bytes"`
	Seconds float64 `json:"seconds"`
	End     string  `json:"end"`
}

type ProbeFunc func(context.Context, ProbeRequest) (time.Duration, error)
type DownloadFunc func(context.Context, DownloadRequest) (DownloadSample, error)
type ResolveFunc func(context.Context, string) ([]netip.Addr, error)

type Engine struct {
	Probe       ProbeFunc
	Download    DownloadFunc
	Resolve     ResolveFunc
	ICMPCheck   func(string) error
	Concurrency int
}

func New() *Engine {
	return &Engine{Concurrency: 32}
}

func DefaultConfig() Config {
	return Config{
		IPv4Enabled:           true,
		Sources:               []Source{{Kind: "builtin", Value: "ipv4"}},
		SampleSize:            256,
		IPv4Limit:             10000,
		IPv6Limit:             10000,
		ProbeMode:             "icmp",
		TCPPort:               443,
		ProbeCount:            5,
		ProbeTimeoutMS:        1000,
		LatencyEnabled:        true,
		LossEnabled:           true,
		DownloadEnabled:       true,
		LatencyMaxMS:          200,
		LossMaxPercent:        20,
		SpeedMinMBS:           5,
		FilterOrder:           []string{"latency", "loss", "download"},
		Sort:                  "latency",
		DownloadIPv4Count:     20,
		DownloadIPv6Count:     20,
		DownloadWindowSeconds: 10,
		PrebodyTimeoutSeconds: 10,
		DownloadURL:           DefaultDownloadURL,
	}
}
