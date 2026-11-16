package optimizer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

func downloadCandidate(parent context.Context, request DownloadRequest) (DownloadSample, error) {
	sample := DownloadSample{End: "failed"}
	targetURL, err := parseDownloadURL(request.URL)
	if err != nil {
		return sample, err
	}
	address, err := netip.ParseAddr(request.IP)
	if err != nil || address.Zone() != "" {
		return sample, fmt.Errorf("下载候选 IP 无效")
	}
	if request.Window <= 0 || request.PrebodyTimeout <= 0 {
		return sample, fmt.Errorf("正文窗口和响应头等待时间必须大于零")
	}
	if err := parent.Err(); err != nil {
		return sample, err
	}
	port := effectivePort(targetURL)
	target := net.JoinHostPort(address.Unmap().String(), port)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, ignored string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", target)
		},
		TLSClientConfig:    &tls.Config{ServerName: targetURL.Hostname(), MinVersion: tls.VersionTLS12},
		DisableCompression: true,
		DisableKeepAlives:  true,
		MaxConnsPerHost:    1,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(next *http.Request, previous []*http.Request) error {
			if len(previous) >= 10 {
				return fmt.Errorf("下载重定向超过 10 跳")
			}
			if next.URL.User != nil || !sameOrigin(targetURL, next.URL) {
				return fmt.Errorf("拒绝跨源、降级或带凭据的下载重定向")
			}
			return nil
		},
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return sample, fmt.Errorf("创建下载请求失败: %w", err)
	}
	httpRequest.Header.Set("Accept-Encoding", "identity")
	var headerExpired int32
	stopHeaderTimer := startTimer(request.PrebodyTimeout, func() {
		atomic.StoreInt32(&headerExpired, 1)
		cancel()
	})
	response, err := client.Do(httpRequest)
	stopHeaderTimer()
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if parent.Err() != nil {
			return sample, parent.Err()
		}
		if atomic.LoadInt32(&headerExpired) != 0 {
			return sample, fmt.Errorf("下载前置等待超时，未取得最终响应头")
		}
		return sample, fmt.Errorf("下载连接或响应头读取失败: %w", err)
	}
	defer response.Body.Close()
	if parent.Err() != nil {
		return sample, parent.Err()
	}
	if atomic.LoadInt32(&headerExpired) != 0 {
		return sample, fmt.Errorf("下载前置等待超时，未取得最终响应头")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return sample, fmt.Errorf("下载服务器返回 HTTP %d", response.StatusCode)
	}
	var windowExpired int32
	started := time.Now()
	stopBodyTimer := startTimer(request.Window, func() {
		atomic.StoreInt32(&windowExpired, 1)
		cancel()
	})
	defer stopBodyTimer()
	buffer := make([]byte, 64*1024)
	for {
		length, readErr := response.Body.Read(buffer)
		sample.Bytes += int64(length)
		sample.Seconds = time.Since(started).Seconds()
		if parent.Err() != nil {
			return sample, parent.Err()
		}
		if readErr != nil {
			switch {
			case errors.Is(readErr, io.EOF):
				if atomic.LoadInt32(&windowExpired) != 0 {
					sample.End = "window"
				} else {
					sample.End = "completed"
				}
			case atomic.LoadInt32(&windowExpired) != 0 && errors.Is(readErr, context.Canceled):
				sample.End = "window"
			default:
				return sample, fmt.Errorf("下载正文读取失败: %w", readErr)
			}
			break
		}
		if atomic.LoadInt32(&windowExpired) != 0 {
			sample.End = "window"
			break
		}
	}
	if err := validateSample(sample); err != nil {
		sample.End = "failed"
		return sample, err
	}
	return sample, nil
}

func startTimer(duration time.Duration, callback func()) func() {
	done := make(chan struct{})
	timer := time.AfterFunc(duration, func() {
		defer close(done)
		callback()
	})
	return func() {
		if !timer.Stop() {
			<-done
		}
	}
}

func effectivePort(target *url.URL) string {
	if port := target.Port(); port != "" {
		return port
	}
	if target.Scheme == "https" {
		return "443"
	}
	return "80"
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Hostname(), right.Hostname()) && effectivePort(left) == effectivePort(right)
}
