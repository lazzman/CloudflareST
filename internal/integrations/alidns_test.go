package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (transport roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func dnsConnection() Connection {
	return Connection{
		ID: "dns-connection", Kind: "alidns", AccountID: "1234567890123456",
		Secrets: map[string]string{"access_key_id": "test-id", "access_key_secret": "test-secret"},
	}
}

func selectedTarget() Target {
	return Target{
		ConnectionID: "dns-connection", AccountID: "1234567890123456",
		RecordID: "record-1", Domain: "example.com", RR: "www", Type: "A",
		TTL: 60, Line: "default", Value: "192.0.2.99",
	}
}

func liveRecord() dnsRecord {
	priority := 10
	return dnsRecord{
		RecordID: "record-1", Domain: "example.com", RR: "www", Type: "A",
		TTL: 900, Line: "telecom", Value: "192.0.2.1", Priority: &priority,
	}
}

func writeJSON(response http.ResponseWriter, value interface{}) {
	response.Header().Set("Content-Type", "application/json")
	json.NewEncoder(response).Encode(value)
}

func mockClient(t *testing.T, kind string, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := New()
	client.HTTPClient = server.Client()
	transport := &http.Transport{Proxy: nil}
	client.HTTPClient.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		address := net.ParseIP(request.URL.Hostname())
		if address == nil || !address.IsLoopback() {
			t.Error("离线测试禁止请求非回环地址")
			return nil, errors.New("离线测试禁止访问外网")
		}
		return transport.RoundTrip(request)
	})
	t.Cleanup(transport.CloseIdleConnections)
	client.Endpoints = map[string]string{kind: server.URL}
	return client
}

func parseRPC(t *testing.T, request *http.Request) url.Values {
	t.Helper()
	if request.Method != http.MethodPost || request.URL.RawQuery != "" {
		t.Errorf("RPC 应使用无查询参数的 POST 请求")
	}
	if err := request.ParseForm(); err != nil {
		t.Errorf("无法解析模拟请求")
	}
	parameters := request.PostForm
	if parameters.Get("Signature") != signRPC(parameters, "test-secret") {
		t.Errorf("RPC 请求签名不匹配")
	}
	if parameters.Get("AccessKeyId") != "test-id" || parameters.Get("SignatureMethod") != "HMAC-SHA1" || parameters.Get("SignatureVersion") != "1.0" || parameters.Get("Format") != "JSON" {
		t.Errorf("RPC 公共参数无效")
	}
	if _, err := time.Parse("2006-01-02T15:04:05Z", parameters.Get("Timestamp")); err != nil || parameters.Get("SignatureNonce") == "" {
		t.Errorf("RPC 时间戳或随机数无效")
	}
	return parameters
}

func assertSafe(t *testing.T, output string) {
	t.Helper()
	for _, secret := range []string{"test-secret", "private-token", "private-webhook", "private-response", "private-signature"} {
		if strings.Contains(output, secret) {
			t.Fatal("返回信息泄漏敏感数据")
		}
	}
}

func TestRPCSignatureVector(t *testing.T) {
	parameters := url.Values{
		"AccessKeyId": {"testid"}, "Action": {"DescribeDomains"}, "Format": {"JSON"},
		"RR": {"*.空 格+~"}, "SignatureMethod": {"HMAC-SHA1"}, "SignatureNonce": {"nonce-001"},
		"SignatureVersion": {"1.0"}, "Timestamp": {"2026-11-14T00:00:00Z"}, "Version": {"2015-01-09"},
	}
	if signature := signRPC(parameters, "testsecret"); signature != "QOswgbs/efWUIxXGUR/rrM5b0HM=" {
		t.Fatalf("签名与固定校验向量不一致：%s", signature)
	}
	parameters.Set("Signature", "忽略已有签名")
	if signRPC(parameters, "testsecret") != "QOswgbs/efWUIxXGUR/rrM5b0HM=" {
		t.Fatal("Signature 不应参与自身签名")
	}
}

func TestAccountIdentity(t *testing.T) {
	var calls int32
	client := mockClient(t, "sts", func(response http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&calls, 1)
		parameters := parseRPC(t, request)
		if parameters.Get("Action") != "GetCallerIdentity" || parameters.Get("Version") != "2015-04-01" {
			t.Error("身份核验只能调用 STS GetCallerIdentity")
		}
		writeJSON(response, map[string]string{"AccountId": "1234567890123456", "Arn": "private-response"})
	})
	connection := dnsConnection()
	connection.AccountID = "不可信的输入"
	identity, err := client.AccountIdentity(context.Background(), connection)
	if err != nil || identity != "1234567890123456" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("身份核验失败：账号=%q，错误=%v，调用=%d", identity, err, calls)
	}
	if connection.AccountID != "不可信的输入" {
		t.Fatal("只读身份核验不应修改连接")
	}
}

func TestAccountIdentityErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"明确拒绝", "{\"Code\":\"Denied\",\"Message\":\"test-secret private-response\"}"},
		{"缺失账号", "{}"},
		{"空账号", "{\"AccountId\":\"\"}"},
		{"空白账号", "{\"AccountId\":\" 123 \"}"},
		{"非数字账号", "{\"AccountId\":\"private-response\"}"},
		{"类型错误", "{\"AccountId\":123}"},
		{"无效响应", "private-response"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := mockClient(t, "sts", func(response http.ResponseWriter, request *http.Request) {
				parseRPC(t, request)
				io.WriteString(response, testCase.body)
			})
			identity, err := client.AccountIdentity(context.Background(), dnsConnection())
			if err == nil || identity != "" {
				t.Fatal("异常身份响应必须返回错误且不能接受配置账号")
			}
			assertSafe(t, err.Error())
		})
	}
	t.Run("传输错误", func(t *testing.T) {
		client := New()
		client.HTTPClient.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "sts.aliyuncs.com" {
				t.Error("默认身份核验地址不正确")
			}
			return nil, errors.New("test-secret private-response")
		})
		_, err := client.AccountIdentity(context.Background(), dnsConnection())
		if err == nil {
			t.Fatal("传输错误应被报告")
		}
		assertSafe(t, err.Error())
	})
}

func TestListAllPages(t *testing.T) {
	var actions []string
	client := mockClient(t, "alidns", func(response http.ResponseWriter, request *http.Request) {
		parameters := parseRPC(t, request)
		action, page := parameters.Get("Action"), parameters.Get("PageNumber")
		actions = append(actions, action+":"+page)
		if parameters.Get("Version") != "2015-01-09" || parameters.Get("PageSize") != "100" {
			t.Error("DNS 查询版本或分页参数错误")
		}
		pageNumber, _ := strconv.Atoi(page)
		switch action {
		case "DescribeDomains":
			writeJSON(response, map[string]interface{}{
				"TotalCount": 3, "PageNumber": pageNumber,
				"Domains": map[string]interface{}{"Domain": []map[string]string{{"DomainName": "example" + page + ".com"}}},
			})
		case "DescribeDomainRecords":
			if parameters.Get("DomainName") != "example.com" {
				t.Error("不应更换所选域名")
			}
			record := liveRecord()
			record.RecordID = "record-" + page
			if page == "2" {
				record.Type = "TXT"
			}
			if page == "3" {
				record.Type = "AAAA"
			}
			writeJSON(response, map[string]interface{}{
				"TotalCount": 3, "PageNumber": pageNumber,
				"DomainRecords": map[string]interface{}{"Record": []dnsRecord{record}},
			})
		default:
			t.Error("列表操作不能写入 DNS")
			response.WriteHeader(http.StatusBadRequest)
		}
	})
	domains, err := client.Domains(context.Background(), dnsConnection())
	if err != nil || !reflect.DeepEqual(domains, []string{"example1.com", "example2.com", "example3.com"}) {
		t.Fatalf("域名分页失败：%v，%v", domains, err)
	}
	records, err := client.Records(context.Background(), dnsConnection(), "example.com")
	if err != nil || len(records) != 2 || records[1].RecordID != "record-3" || records[1].Type != "AAAA" {
		t.Fatalf("记录分页或地址类型过滤失败：%v，%v", records, err)
	}
	if records[0].AccountID != dnsConnection().AccountID || records[0].ConnectionID != dnsConnection().ID || records[0].TTL != 900 || records[0].Line != "telecom" {
		t.Fatal("记录未保留连接身份或原有属性")
	}
	if len(actions) != 6 {
		t.Fatalf("应读完全部分页，实际请求：%v", actions)
	}
}

func TestUpdatePreservesLiveAttributes(t *testing.T) {
	var actions []string
	client := mockClient(t, "alidns", func(response http.ResponseWriter, request *http.Request) {
		parameters := parseRPC(t, request)
		action := parameters.Get("Action")
		actions = append(actions, action)
		switch action {
		case "DescribeDomainRecordInfo":
			if parameters.Get("RecordId") != "record-1" {
				t.Error("必须按选定 RecordId 查询")
			}
			writeJSON(response, liveRecord())
		case "UpdateDomainRecord":
			for key, expected := range map[string]string{
				"RecordId": "record-1", "RR": "www", "Type": "A", "Value": "192.0.2.44",
				"TTL": "900", "Line": "telecom", "Priority": "10",
			} {
				if parameters.Get(key) != expected {
					t.Errorf("更新参数 %s 不正确，实际 %q", key, parameters.Get(key))
				}
			}
			if parameters.Get("DomainName") != "" || parameters.Get("Status") != "" {
				t.Error("不能提交更换目标或无关设置")
			}
			writeJSON(response, map[string]string{"RecordId": "record-1"})
		default:
			t.Error("不能创建或更换记录")
		}
	})
	outcome := client.Update(context.Background(), dnsConnection(), selectedTarget(), "192.0.2.44")
	if outcome.State != "success" || outcome.Verified || !reflect.DeepEqual(actions, []string{"DescribeDomainRecordInfo", "UpdateDomainRecord"}) {
		t.Fatalf("更新结果或调用顺序错误：%+v，%v", outcome, actions)
	}
	if outcome.Before == nil || outcome.Before.Value != liveRecord().Value || outcome.Before.At.IsZero() || outcome.Readback != nil {
		t.Fatalf("缺少本次更新前读取事实或虚构了核对：%+v", outcome)
	}
}

func TestUpdateTimeoutWithNonMatchingReadbackRemainsUnconfirmed(t *testing.T) {
	for _, value := range []string{"192.0.2.1", "192.0.2.77"} {
		t.Run(value, func(t *testing.T) {
			var reads, writes int
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				parameters := parseRPC(t, request)
				switch parameters.Get("Action") {
				case "DescribeDomainRecordInfo":
					reads++
					record := liveRecord()
					record.Value = value
					writeJSON(response, record)
				case "UpdateDomainRecord":
					t.Error("断开连接后的写入请求不应到达模拟服务器")
				default:
					t.Error("未确认写入期间不能执行其他 DNS 写入")
				}
			}))
			t.Cleanup(server.Close)
			serverTransport := server.Client().Transport
			client := New()
			client.HTTPClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					return nil, err
				}
				request.Body = io.NopCloser(strings.NewReader(string(body)))
				parameters, err := url.ParseQuery(string(body))
				if err != nil {
					return nil, err
				}
				if parameters.Get("Action") == "UpdateDomainRecord" {
					writes++
					return nil, errors.New("private-signature：连接中断")
				}
				return serverTransport.RoundTrip(request)
			})}
			client.Endpoints = map[string]string{"alidns": server.URL}

			outcome := client.Update(context.Background(), dnsConnection(), selectedTarget(), "192.0.2.44")
			if outcome.State != "unconfirmed" || outcome.Verified || reads != 2 || writes != 1 {
				t.Fatalf("不匹配核对不应证明写入失败，或应只核对一次：%+v；读取=%d；写入=%d", outcome, reads, writes)
			}
			if !strings.Contains(outcome.Reason, "可能稍后到达") || strings.Contains(outcome.Reason, "确认目标记录不是") {
				t.Fatalf("未提示延迟到达风险：%s", outcome.Reason)
			}
			assertSafe(t, outcome.Reason)
		})
	}
}

func TestUpdateRejectsRecordIdentity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*dnsRecord)
	}{
		{"记录ID", func(record *dnsRecord) { record.RecordID = "other" }},
		{"域名", func(record *dnsRecord) { record.Domain = "other.com" }},
		{"主机记录", func(record *dnsRecord) { record.RR = "other" }},
		{"类型", func(record *dnsRecord) { record.Type = "AAAA" }},
		{"缺失TTL", func(record *dnsRecord) { record.TTL = 0 }},
		{"缺失线路", func(record *dnsRecord) { record.Line = "" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var calls int
			client := mockClient(t, "alidns", func(response http.ResponseWriter, request *http.Request) {
				calls++
				parameters := parseRPC(t, request)
				if parameters.Get("Action") != "DescribeDomainRecordInfo" {
					t.Error("身份不匹配不能写入、创建或改选记录")
				}
				record := liveRecord()
				testCase.mutate(&record)
				writeJSON(response, record)
			})
			outcome := client.Update(context.Background(), dnsConnection(), selectedTarget(), "192.0.2.44")
			if outcome.State != "failed" || calls != 1 {
				t.Fatalf("未拒绝不匹配记录：%+v，调用=%d", outcome, calls)
			}
		})
	}
}

func TestUpdateUncertainResponseReadsExactlyOnce(t *testing.T) {
	cases := []struct {
		name     string
		mode     string
		readback string
		state    string
		verified bool
	}{
		{"超时后已观察到新值", "timeout", "new", "success", true},
		{"断连后已观察到新值", "disconnect", "new", "success", true},
		{"核对读取失败", "timeout", "error", "unconfirmed", false},
		{"核对记录身份变更", "timeout", "identity", "unconfirmed", false},
		{"空确认回执", "invalid", "new", "success", true},
		{"错误记录回执", "wrong-id", "new", "success", true},
		{"网关超时旧值", "gateway", "old", "unconfirmed", false},
		{"网关502旧值", "bad-gateway", "old", "unconfirmed", false},
		{"服务500已观察到新值", "server-error", "new", "success", true},
		{"调用方取消后核对", "cancel", "new", "success", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var reads, writes int32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := mockClient(t, "alidns", func(response http.ResponseWriter, request *http.Request) {
				parameters := parseRPC(t, request)
				switch parameters.Get("Action") {
				case "DescribeDomainRecordInfo":
					readCount := atomic.AddInt32(&reads, 1)
					record := liveRecord()
					if readCount > 1 {
						switch testCase.readback {
						case "new":
							record.Value = "192.0.2.44"
						case "error":
							writeJSON(response, map[string]string{"Code": "Denied", "Message": "private-response"})
							return
						case "identity":
							record.RR = "another"
						}
					}
					writeJSON(response, record)
				case "UpdateDomainRecord":
					atomic.AddInt32(&writes, 1)
					switch testCase.mode {
					case "timeout":
						<-request.Context().Done()
					case "disconnect":
						connection, _, err := response.(http.Hijacker).Hijack()
						if err != nil {
							t.Error("无法模拟断连")
							return
						}
						connection.Close()
					case "invalid":
						io.WriteString(response, "{}")
					case "wrong-id":
						writeJSON(response, map[string]string{"RecordId": "another"})
					case "gateway":
						response.WriteHeader(http.StatusGatewayTimeout)
						io.WriteString(response, "请求超时")
					case "bad-gateway":
						response.WriteHeader(http.StatusBadGateway)
						io.WriteString(response, "网关异常，执行结果未知")
					case "server-error":
						response.WriteHeader(http.StatusInternalServerError)
						writeJSON(response, map[string]string{"Code": "InternalError"})
					case "cancel":
						cancel()
						<-request.Context().Done()
					}
				default:
					t.Error("未确认后只能读取原 RecordId，不能新建或改选记录")
				}
			})
			client.HTTPClient.Timeout = 150 * time.Millisecond
			outcome := client.Update(ctx, dnsConnection(), selectedTarget(), "192.0.2.44")
			if outcome.State != testCase.state || outcome.Verified != testCase.verified || atomic.LoadInt32(&reads) != 2 || atomic.LoadInt32(&writes) != 1 {
				t.Fatalf("未确认处理或单次核对错误：%+v，读取=%d，写入=%d", outcome, reads, writes)
			}
			if outcome.Before == nil || outcome.Before.At.IsZero() {
				t.Fatal("超时尝试丢失更新前读取事实")
			}
			if testCase.readback == "new" || testCase.readback == "old" {
				expected := "192.0.2.44"
				if testCase.readback == "old" {
					expected = liveRecord().Value
				}
				if outcome.Readback == nil || outcome.Readback.Value != expected || outcome.Readback.At.Before(outcome.Before.At) {
					t.Fatalf("核对事实未保留实际值与时刻：%+v", outcome.Readback)
				}
			} else if outcome.Readback != nil {
				t.Fatal("读取失败或身份不符时不能伪造核对值")
			}
			assertSafe(t, outcome.Reason)
		})
	}
}

func TestUpdateExplicitFailureDoesNotReadback(t *testing.T) {
	var reads, writes int
	client := mockClient(t, "alidns", func(response http.ResponseWriter, request *http.Request) {
		parameters := parseRPC(t, request)
		switch parameters.Get("Action") {
		case "DescribeDomainRecordInfo":
			reads++
			writeJSON(response, liveRecord())
		case "UpdateDomainRecord":
			writes++
			writeJSON(response, map[string]string{"Code": "Denied", "Message": "test-secret private-response"})
		default:
			t.Error("不能新建、改选或回滚记录")
		}
	})
	outcome := client.Update(context.Background(), dnsConnection(), selectedTarget(), "192.0.2.44")
	if outcome.State != "failed" || reads != 1 || writes != 1 {
		t.Fatalf("明确拒绝不应自动重试或事后核对：%+v，读取=%d，写入=%d", outcome, reads, writes)
	}
	assertSafe(t, outcome.Reason)
}

func TestUpdateSameIPSkipsWrite(t *testing.T) {
	for _, recordType := range []string{"A", "AAAA"} {
		t.Run(recordType, func(t *testing.T) {
			var reads int
			target := selectedTarget()
			target.Type = recordType
			value := "192.0.2.1"
			if recordType == "AAAA" {
				value = "2001:0db8:0:0:0:0:0:1"
			}
			client := mockClient(t, "alidns", func(response http.ResponseWriter, request *http.Request) {
				reads++
				parameters := parseRPC(t, request)
				if parameters.Get("Action") != "DescribeDomainRecordInfo" {
					t.Error("相同 IP 不应重复写入")
				}
				record := liveRecord()
				record.Type = recordType
				if recordType == "AAAA" {
					record.Value = "2001:db8::1"
				}
				writeJSON(response, record)
			})
			outcome := client.Update(context.Background(), dnsConnection(), target, value)
			if outcome.State != "success" || !outcome.Verified || reads != 1 {
				t.Fatalf("同 IP 应只核验、不写入：%+v，读取=%d", outcome, reads)
			}
		})
	}
}

func TestUpdateRejectsTargetBeforeRequest(t *testing.T) {
	for _, mutate := range []func(*Target){
		func(target *Target) { target.AccountID = "another" },
		func(target *Target) { target.ConnectionID = "another" },
		func(target *Target) { target.RecordID = "" },
		func(target *Target) { target.Domain = "" },
		func(target *Target) { target.RR = "" },
		func(target *Target) { target.Type = "AAAA" },
		func(target *Target) { target.Type = "TXT" },
	} {
		target := selectedTarget()
		mutate(&target)
		client := New()
		client.HTTPClient.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			t.Error("本地校验失败不应发送任何请求")
			return nil, errors.New("离线测试禁止外网")
		})
		if outcome := client.Update(context.Background(), dnsConnection(), target, "192.0.2.44"); outcome.State != "failed" {
			t.Fatalf("应拒绝无效或不匹配的目标：%+v", outcome)
		}
	}
}
