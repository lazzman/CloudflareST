package safedata

import "testing"

func TestURLSecretClassification(test *testing.T) {
	for _, address := range []string{
		"https://example.invalid/file?token=FAKE", "https://example.invalid/file?X-Amz-Signature=FAKE",
		"https://example.invalid/file?api_key=FAKE", "https://example.invalid/file?%2574oken=FAKE", "https://example.invalid/file?sig=FAKE",
		"https://example.invalid/file?auth_key=FAKE", "https://example.invalid/file?hdnts=FAKE", "https://example.invalid/file?hdnea=FAKE",
		"https://user:FAKE@example.invalid/file", "https://api.telegram.org/bot100001:FAKE/file", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=FAKE",
	} {
		if !URLHasSecrets(address) {
			test.Errorf("未识别认证参数或地址：%s", address)
		}
	}
	for _, address := range []string{"https://example.invalid/file?bytes=3000&size=10", "http://127.0.0.1/file?format=binary", "https://example.invalid/file"} {
		if URLHasSecrets(address) {
			test.Errorf("普通文件参数不应被当作凭据：%s", address)
		}
	}
}
