package optimizer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func localDownloadRequest(t *testing.T, handler http.HandlerFunc) DownloadRequest {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	address := server.Listener.Addr().(*net.TCPAddr)
	return DownloadRequest{IP: address.IP.String(), URL: "http://download.invalid:" + fmt.Sprint(address.Port) + "/file", Window: 100 * time.Millisecond, PrebodyTimeout: time.Second}
}

func TestDownloadEarlyCompletionAndPinnedHost(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	var calls int32
	request := localDownloadRequest(t, func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&calls, 1)
		if !strings.HasPrefix(request.Host, "download.invalid:") || request.URL.Path != "/file" || request.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("Host、路径或编码错误: %+v", request)
		}
		writer.Write([]byte(strings.Repeat("a", 10000)))
	})
	sample, err := downloadCandidate(context.Background(), request)
	if err != nil || sample.End != "completed" || sample.Bytes != 10000 || sample.Seconds <= 0 || sample.Seconds >= request.Window.Seconds() || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("提前完成样本错误: %+v, %v, %d", sample, err, atomic.LoadInt32(&calls))
	}
}

func TestDownloadIndependentWindowsAndBodyStall(t *testing.T) {
	request := localDownloadRequest(t, func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(80 * time.Millisecond)
		writer.WriteHeader(http.StatusOK)
		writer.Write([]byte(strings.Repeat("a", 1024)))
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	})
	request.PrebodyTimeout = 300 * time.Millisecond
	request.Window = 80 * time.Millisecond
	started := time.Now()
	sample, err := downloadCandidate(context.Background(), request)
	wall := time.Since(started)
	if err != nil || sample.End != "window" || sample.Bytes != 1024 || sample.Seconds < 0.07 || sample.Seconds > 0.3 || wall < 150*time.Millisecond || wall.Seconds()-sample.Seconds < 0.06 {
		t.Fatalf("前置时间不应占正文窗口，正文停滞应计入分母: %+v, 总耗时%s, %v", sample, wall, err)
	}
}

func TestDownloadPrebodyTimeout(t *testing.T) {
	request := localDownloadRequest(t, func(_ http.ResponseWriter, request *http.Request) { <-request.Context().Done() })
	request.PrebodyTimeout = 30 * time.Millisecond
	started := time.Now()
	sample, err := downloadCandidate(context.Background(), request)
	if err == nil || sample.End != "failed" || sample.Bytes != 0 || !strings.Contains(err.Error(), "前置等待超时") || time.Since(started) > time.Second {
		t.Fatalf("前置超时没有正确结束: %+v,%v", sample, err)
	}
}

func TestDownloadRedirectsSharePrebodyBudget(t *testing.T) {
	var calls int32
	request := localDownloadRequest(t, func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&calls, 1)
		timer := time.NewTimer(35 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-request.Context().Done():
			return
		}
		if request.URL.Path == "/file" {
			http.Redirect(writer, request, "/next", http.StatusFound)
			return
		}
		writer.Write([]byte("finished"))
	})
	request.PrebodyTimeout = 60 * time.Millisecond
	sample, err := downloadCandidate(context.Background(), request)
	if err == nil || sample.End != "failed" || !strings.Contains(err.Error(), "前置等待超时") || atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("重定向链重新领取了前置预算: %+v,%v,%d", sample, err, atomic.LoadInt32(&calls))
	}
}

func TestDownloadZeroBytesAndRealReadError(t *testing.T) {
	for _, kind := range []string{"empty", "zero-window", "truncated", "http-error"} {
		t.Run(kind, func(t *testing.T) {
			request := localDownloadRequest(t, func(writer http.ResponseWriter, request *http.Request) {
				switch kind {
				case "empty":
					writer.WriteHeader(http.StatusOK)
				case "zero-window":
					writer.WriteHeader(http.StatusOK)
					writer.(http.Flusher).Flush()
					<-request.Context().Done()
				case "truncated":
					writer.Header().Set("Content-Length", "10000")
					writer.Write([]byte("data"))
				case "http-error":
					writer.WriteHeader(http.StatusForbidden)
				}
			})
			request.Window = 30 * time.Millisecond
			sample, err := downloadCandidate(context.Background(), request)
			if err == nil || sample.End != "failed" {
				t.Fatalf("无有效正文或真实错误必须失败: %+v,%v", sample, err)
			}
			if kind == "truncated" && (sample.Bytes != 4 || !errors.Is(err, io.ErrUnexpectedEOF)) {
				t.Fatalf("部分数据不得挽救真实读取错误: %+v,%v", sample, err)
			}
		})
	}
}

func TestDownloadSameOriginRedirectAndCrossOriginRefusal(t *testing.T) {
	for _, cross := range []bool{false, true} {
		t.Run(fmt.Sprint(cross), func(t *testing.T) {
			var calls int32
			request := localDownloadRequest(t, func(writer http.ResponseWriter, request *http.Request) {
				atomic.AddInt32(&calls, 1)
				if request.URL.Path == "/file" {
					target := "/final"
					if cross {
						target = "http://forbidden.invalid/file"
					}
					http.Redirect(writer, request, target, http.StatusFound)
					return
				}
				writer.Write([]byte("payload"))
				writer.(http.Flusher).Flush()
				<-request.Context().Done()
			})
			sample, err := downloadCandidate(context.Background(), request)
			if cross {
				if err == nil || atomic.LoadInt32(&calls) != 1 || sample.End != "failed" {
					t.Fatalf("跨源跳转未拒绝: %+v,%v,%d", sample, err, atomic.LoadInt32(&calls))
				}
			} else if err != nil || sample.End != "window" || sample.Bytes != 7 || sample.Seconds <= 0 || atomic.LoadInt32(&calls) != 2 {
				t.Fatalf("同源重定向应保持候选 IP: %+v,%v,%d", sample, err, atomic.LoadInt32(&calls))
			}
		})
	}
}

func TestDownloadTLSUsesURLServerNameAndVerifiesCertificate(t *testing.T) {
	sni := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.Write([]byte("payload")) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { sni <- hello.ServerName; return nil, nil }}
	server.StartTLS()
	defer server.Close()
	address := server.Listener.Addr().(*net.TCPAddr)
	request := DownloadRequest{IP: address.IP.String(), URL: "https://download.invalid:" + fmt.Sprint(address.Port) + "/file", Window: time.Second, PrebodyTimeout: time.Second}
	sample, err := downloadCandidate(context.Background(), request)
	if err == nil || sample.End != "failed" {
		t.Fatalf("不应关闭证书校验: %+v,%v", sample, err)
	}
	select {
	case value := <-sni:
		if value != "download.invalid" {
			t.Fatalf("SNI 不来自 URL: %s", value)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到 TLS ClientHello")
	}
}

func TestDownloadCancellationInterruptsBlockedRead(t *testing.T) {
	entered := make(chan struct{})
	request := localDownloadRequest(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Write([]byte("payload"))
		writer.(http.Flusher).Flush()
		close(entered)
		<-request.Context().Done()
	})
	request.Window = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { _, err := downloadCandidate(ctx, request); returned <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("下载未开始")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消错误丢失: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消未中断正文读取")
	}
}

func TestEngineDownloadPreselectionAfterExpansionAndDecimalSpeed(t *testing.T) {
	config := testConfig(Source{Kind: "cidr", Value: "192.0.2.0/28", Count: 1})
	config.IPv4Limit = 16
	config.DownloadEnabled = true
	config.DownloadIPv4Count = 2
	engine := successfulEngine()
	var lock sync.Mutex
	measured := make(map[string]bool)
	engine.Probe = func(_ context.Context, request ProbeRequest) (time.Duration, error) {
		lock.Lock()
		defer lock.Unlock()
		measured[request.IP] = true
		return time.Duration(netip.MustParseAddr(request.IP).As4()[3]+1) * time.Millisecond, nil
	}
	var downloaded []string
	engine.Download = func(_ context.Context, request DownloadRequest) (DownloadSample, error) {
		lock.Lock()
		count := len(measured)
		lock.Unlock()
		if count != 16 {
			t.Errorf("初采/扩采未合并完成就下载: %d", count)
		}
		downloaded = append(downloaded, request.IP)
		return DownloadSample{Bytes: 10000000, Seconds: 2, End: "completed"}, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil || !reflect.DeepEqual(downloaded, []string{"192.0.2.0", "192.0.2.1"}) {
		t.Fatalf("下载未按统一延迟预选: %v,%v", downloaded, err)
	}
	passed, untested := 0, 0
	for _, candidate := range result.Candidates {
		if candidate.Eligible {
			passed++
			if candidate.SpeedMBS == nil || *candidate.SpeedMBS != 5 {
				t.Fatalf("应为十进制 5 MB/s: %+v", candidate)
			}
		}
		if candidate.State == "untested" {
			untested++
			if candidate.SpeedMBS != nil || candidate.Eligible {
				t.Fatalf("未下载不能记为零速度或合格: %+v", candidate)
			}
		}
	}
	if passed != 2 || untested != 14 {
		t.Fatalf("下载名额错误: %d,%d", passed, untested)
	}
}

func TestEngineRealDownloadErrorNotZeroAndContinues(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"}, Source{Kind: "ip", Value: "192.0.2.2"})
	config.IPv4Limit = 2
	config.DownloadEnabled = true
	config.DownloadIPv4Count = 2
	engine := successfulEngine()
	engine.Download = func(_ context.Context, request DownloadRequest) (DownloadSample, error) {
		if strings.HasSuffix(request.IP, "1") {
			return DownloadSample{Bytes: 4, Seconds: 0.01, End: "failed"}, io.ErrUnexpectedEOF
		}
		return DownloadSample{Bytes: 6000000, Seconds: 1, End: "window"}, nil
	}
	result, err := engine.Run(context.Background(), config, nil)
	if err != nil || result.Status != "completed" {
		t.Fatalf("单候选下载失败不应终止任务: %+v,%v", result, err)
	}
	for _, candidate := range result.Candidates {
		if strings.HasSuffix(candidate.IP, "1") && (candidate.SpeedMBS != nil || candidate.Eligible || candidate.State != "failed" || candidate.DownloadBytes != 4 || candidate.DownloadEnd != "failed") {
			t.Fatalf("真实错误被伪造成有效或零速度: %+v", candidate)
		}
	}
}

func TestEngineCancellationWaitsForDownloadCleanup(t *testing.T) {
	config := testConfig(Source{Kind: "ip", Value: "192.0.2.1"})
	config.IPv4Limit = 1
	config.DownloadEnabled = true
	config.DownloadIPv4Count = 1
	engine := successfulEngine()
	entered := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan error, 1)
	engine.Download = func(ctx context.Context, _ DownloadRequest) (DownloadSample, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return DownloadSample{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, err := engine.Run(ctx, config, nil); returned <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("下载未开始")
	}
	cancel()
	select {
	case err := <-returned:
		t.Fatalf("下载清理前提前返回: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消错误丢失: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("下载取消未完成")
	}
}
