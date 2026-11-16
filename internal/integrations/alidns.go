package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type dnsRecord struct {
	RecordID string `json:"RecordId"`
	Domain   string `json:"DomainName"`
	RR       string `json:"RR"`
	Type     string `json:"Type"`
	Line     string `json:"Line"`
	TTL      int    `json:"TTL"`
	Value    string `json:"Value"`
	Priority *int   `json:"Priority"`
}

func rpcEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func signRPC(parameters url.Values, secret string) string {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		if key != "Signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	canonical := make([]string, 0, len(keys))
	for _, key := range keys {
		canonical = append(canonical, rpcEscape(key)+"="+rpcEscape(parameters.Get(key)))
	}
	message := "POST&%2F&" + rpcEscape(strings.Join(canonical, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (client *Client) rpc(ctx context.Context, connection Connection, action string, parameters url.Values, result interface{}) *requestError {
	return client.serviceRPC(ctx, connection, "alidns", action, parameters, result)
}

func (client *Client) serviceRPC(ctx context.Context, connection Connection, service, action string, parameters url.Values, result interface{}) *requestError {
	endpoint, err := client.endpoint(service)
	if err != nil {
		return &requestError{reason: err.Error()}
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return &requestError{reason: "无法生成阿里云请求随机标识"}
	}
	parameters.Set("Action", action)
	parameters.Set("Format", "JSON")
	parameters.Set("Version", "2015-01-09")
	if service == "sts" {
		parameters.Set("Version", "2015-04-01")
	}
	parameters.Set("AccessKeyId", connection.Secrets["access_key_id"])
	parameters.Set("SignatureMethod", "HMAC-SHA1")
	parameters.Set("SignatureVersion", "1.0")
	parameters.Set("SignatureNonce", hex.EncodeToString(nonce))
	parameters.Set("Timestamp", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	parameters.Set("Signature", signRPC(parameters, connection.Secrets["access_key_secret"]))
	body, status, failure := client.exchange(ctx, endpoint, "application/x-www-form-urlencoded", []byte(parameters.Encode()))
	if failure != nil {
		return failure
	}
	if status == http.StatusRequestTimeout || status >= 500 {
		return &requestError{reason: "阿里云超时或服务端／网关异常，无法证明请求未执行，结果未确认", uncertain: true}
	}
	var envelope struct {
		Code string `json:"Code"`
	}
	if validJSONObject(body) && json.Unmarshal(body, &envelope) == nil && envelope.Code != "" {
		return &requestError{reason: "阿里云接口明确拒绝请求"}
	}
	if status < 200 || status >= 300 {
		return &requestError{reason: "阿里云接口返回失败状态"}
	}
	if !validJSONObject(body) || json.Unmarshal(body, result) != nil {
		return &requestError{reason: "阿里云响应无效，结果未确认", uncertain: true}
	}
	return nil
}

func (client *Client) AccountIdentity(ctx context.Context, connection Connection) (string, error) {
	if err := checkDNSConnection(connection); err != nil {
		return "", err
	}
	var response struct {
		AccountID string `json:"AccountId"`
	}
	if failure := client.serviceRPC(ctx, connection, "sts", "GetCallerIdentity", url.Values{}, &response); failure != nil {
		return "", failure
	}
	if strings.TrimSpace(response.AccountID) == "" || strings.TrimSpace(response.AccountID) != response.AccountID {
		return "", errors.New("阿里云身份响应缺少有效账号标识")
	}
	for _, character := range response.AccountID {
		if character < '0' || character > '9' {
			return "", errors.New("阿里云身份响应的账号标识格式无效")
		}
	}
	return response.AccountID, nil
}

func checkDNSConnection(connection Connection) error {
	if err := ValidateConnection(connection); err != nil {
		return err
	}
	if connection.Kind != "alidns" {
		return errors.New("此操作需要阿里云 DNS 连接")
	}
	return nil
}

func nextPage(total *int, page, reportedPage, count, received int) (bool, error) {
	if total == nil || *total < 0 || (reportedPage != 0 && reportedPage != page) || count > *total || (received == 0 && count < *total) {
		return false, errors.New("阿里云 DNS 分页响应无效，未能完整读取列表")
	}
	return count < *total, nil
}

func (client *Client) Domains(ctx context.Context, connection Connection) ([]string, error) {
	if err := checkDNSConnection(connection); err != nil {
		return nil, err
	}
	domains := make([]string, 0)
	seen := make(map[string]bool)
	for page := 1; ; page++ {
		var response struct {
			TotalCount *int `json:"TotalCount"`
			PageNumber int  `json:"PageNumber"`
			Domains    struct {
				Domain []struct {
					Name string `json:"DomainName"`
				} `json:"Domain"`
			} `json:"Domains"`
		}
		parameters := url.Values{"PageNumber": {strconv.Itoa(page)}, "PageSize": {"100"}}
		if failure := client.rpc(ctx, connection, "DescribeDomains", parameters, &response); failure != nil {
			return nil, failure
		}
		for _, domain := range response.Domains.Domain {
			if strings.TrimSpace(domain.Name) == "" || seen[strings.ToLower(domain.Name)] {
				return nil, errors.New("阿里云 DNS 域名分页包含无效或重复域名")
			}
			seen[strings.ToLower(domain.Name)] = true
			domains = append(domains, domain.Name)
		}
		more, err := nextPage(response.TotalCount, page, response.PageNumber, len(domains), len(response.Domains.Domain))
		if err != nil || !more {
			if err != nil {
				return nil, err
			}
			return domains, nil
		}
	}
}

func (client *Client) Records(ctx context.Context, connection Connection, domain string) ([]Target, error) {
	if err := checkDNSConnection(connection); err != nil {
		return nil, err
	}
	if strings.TrimSpace(domain) == "" {
		return nil, errors.New("必须选择要查询的域名")
	}
	targets := make([]Target, 0)
	seen := make(map[string]bool)
	count := 0
	for page := 1; ; page++ {
		var response struct {
			TotalCount    *int `json:"TotalCount"`
			PageNumber    int  `json:"PageNumber"`
			DomainRecords struct {
				Record []dnsRecord `json:"Record"`
			} `json:"DomainRecords"`
		}
		parameters := url.Values{"DomainName": {domain}, "PageNumber": {strconv.Itoa(page)}, "PageSize": {"100"}}
		if failure := client.rpc(ctx, connection, "DescribeDomainRecords", parameters, &response); failure != nil {
			return nil, failure
		}
		readAt := time.Now().UTC()
		for _, record := range response.DomainRecords.Record {
			if record.RecordID == "" || seen[record.RecordID] || (record.Domain != "" && !sameDNSName(record.Domain, domain)) {
				return nil, errors.New("阿里云 DNS 记录分页包含无效、重复或不属于所选域名的记录")
			}
			seen[record.RecordID] = true
			if record.Type == "A" || record.Type == "AAAA" {
				targets = append(targets, Target{
					ConnectionID: connection.ID, AccountID: connection.AccountID,
					RecordID: record.RecordID, Domain: domain, RR: record.RR, Type: record.Type,
					Line: record.Line, TTL: record.TTL, Value: record.Value, ReadAt: &readAt,
				})
			}
		}
		count += len(response.DomainRecords.Record)
		more, err := nextPage(response.TotalCount, page, response.PageNumber, count, len(response.DomainRecords.Record))
		if err != nil || !more {
			if err != nil {
				return nil, err
			}
			return targets, nil
		}
	}
}

func sameDNSName(first, second string) bool {
	return strings.EqualFold(strings.TrimSuffix(first, "."), strings.TrimSuffix(second, "."))
}

func validateTarget(connection Connection, target Target, ip string) (string, error) {
	if err := checkDNSConnection(connection); err != nil {
		return "", err
	}
	if connection.ID == "" || connection.AccountID == "" || target.ConnectionID != connection.ID || target.AccountID != connection.AccountID {
		return "", errors.New("DNS 目标与所选连接或账号身份不一致")
	}
	if target.RecordID == "" || strings.TrimSpace(target.Domain) == "" || strings.TrimSpace(target.RR) == "" {
		return "", errors.New("DNS 目标缺少明确的记录标识、域名或主机记录")
	}
	address := net.ParseIP(ip)
	if address == nil || (target.Type != "A" && target.Type != "AAAA") || (target.Type == "A") != (address.To4() != nil) {
		return "", errors.New("优选 IP 无效或与 DNS 记录地址族不匹配")
	}
	return address.String(), nil
}

func (client *Client) readRecord(ctx context.Context, connection Connection, target Target) (dnsRecord, *requestError) {
	var record dnsRecord
	if failure := client.rpc(ctx, connection, "DescribeDomainRecordInfo", url.Values{"RecordId": {target.RecordID}}, &record); failure != nil {
		return record, failure
	}
	if record.RecordID != target.RecordID || !sameDNSName(record.Domain, target.Domain) || !sameDNSName(record.RR, target.RR) || record.Type != target.Type || record.TTL <= 0 || record.Line == "" {
		return record, &requestError{reason: "DNS 记录身份或原有属性核验失败，未改选其他记录"}
	}
	return record, nil
}

func sameIP(first, second string) bool {
	firstIP, secondIP := net.ParseIP(first), net.ParseIP(second)
	return firstIP != nil && secondIP != nil && firstIP.Equal(secondIP)
}

func (client *Client) Update(ctx context.Context, connection Connection, target Target, ip string) (outcome Outcome) {
	value, err := validateTarget(connection, target, ip)
	if err != nil {
		return Outcome{State: "failed", Reason: err.Error()}
	}
	record, failure := client.readRecord(ctx, connection, target)
	if failure != nil {
		return Outcome{State: "failed", Reason: "更新前读取核验失败，未发送写入：" + failure.reason}
	}
	before := &DNSObservation{Value: record.Value, At: time.Now().UTC()}
	defer func() { outcome.Before = before }()
	if sameIP(record.Value, value) {
		return Outcome{State: "success", Reason: "已核验目标记录为所选 IP，无需重复写入", Verified: true, NoWrite: true}
	}
	parameters := url.Values{
		"RecordId": {record.RecordID}, "RR": {record.RR}, "Type": {record.Type}, "Value": {value},
		"TTL": {strconv.Itoa(record.TTL)}, "Line": {record.Line},
	}
	if record.Priority != nil {
		parameters.Set("Priority", strconv.Itoa(*record.Priority))
	}
	var response struct {
		RecordID string `json:"RecordId"`
	}
	failure = client.rpc(ctx, connection, "UpdateDomainRecord", parameters, &response)
	if failure == nil && response.RecordID == target.RecordID {
		return Outcome{State: "success", Reason: "阿里云已确认 DNS 记录更新成功"}
	}
	if failure != nil && !failure.uncertain {
		return outcomeFromError(failure)
	}
	verificationContext, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	verifiedRecord, verificationFailure := client.readRecord(verificationContext, connection, target)
	if verificationFailure != nil {
		return Outcome{State: "unconfirmed", Reason: "写入结果未确认且只读核对失败；未重写或回滚，手动重试可能重复写入"}
	}
	readback := &DNSObservation{Value: verifiedRecord.Value, At: time.Now().UTC()}
	defer func() { outcome.Readback = readback }()
	if sameIP(verifiedRecord.Value, value) {
		return Outcome{State: "success", Reason: "写入响应未确认，已通过只读核对确认目标 IP", Verified: true}
	}
	return Outcome{State: "unconfirmed", Reason: "单次只读核对未观察到所选 IP，但超时请求可能稍后到达；未重写或回滚，手动重试可能重复写入"}
}
