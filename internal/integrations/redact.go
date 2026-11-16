package integrations

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/masgzy/CloudflareST/internal/safedata"
)

const secretFields = `access_?key_?(?:id|secret)|authorization|bot_?token|chat_?id|webhook_?url|webhook|token|secret|signature|password|key`

var (
	secretJSONPattern    = regexp.MustCompile(`(?i)("(?:` + secretFields + `)"\s*:\s*)"(?:\\.|[^"\\])*"`)
	secretQuotedPattern  = regexp.MustCompile(`(?i)((?:` + secretFields + `)\s*[:=]\s*)'(?:\\.|[^'\\])*'`)
	secretValuePattern   = regexp.MustCompile(`(?i)(\b(?:` + secretFields + `)\s*[:=]\s*)[^\s&,;\]}"']+`)
	secretDoublePattern  = regexp.MustCompile(`(?i)(\b(?:` + secretFields + `)\s*=\s*)"(?:\\.|[^"\\])*"`)
	secretURLPattern     = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	telegramTokenPattern = regexp.MustCompile(`[0-9]+(?::|%3[Aa])[A-Za-z0-9_-]{6,}`)
	accessKeyPattern     = regexp.MustCompile(`\bLTAI[A-Za-z0-9]+\b`)
	authorizationPattern = regexp.MustCompile(`(?i)(\bauthorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s,;]+`)
)

func Redact(value string) string {
	value = strings.ReplaceAll(value, `\/`, "/")
	for attempt := 0; attempt < 2 && strings.Contains(value, "%"); attempt++ {
		decoded, err := url.PathUnescape(value)
		if err != nil || decoded == value {
			break
		}
		value = decoded
	}
	value = secretURLPattern.ReplaceAllStringFunc(value, func(rawURL string) string {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return "[已脱敏链接]"
		}
		if parsed.User != nil || strings.Contains(strings.ToLower(parsed.Path), "/bot") || strings.Contains(strings.ToLower(parsed.Path), "/webhook") || strings.EqualFold(parsed.Hostname(), "qyapi.weixin.qq.com") {
			return "[已脱敏链接]"
		}
		if safedata.URLHasSecrets(rawURL) {
			return "[已脱敏链接]"
		}
		for key := range parsed.Query() {
			lowerKey := strings.ToLower(key)
			if strings.Contains(lowerKey, "key") || strings.Contains(lowerKey, "token") || strings.Contains(lowerKey, "secret") || strings.Contains(lowerKey, "signature") || strings.Contains(lowerKey, "password") {
				return "[已脱敏链接]"
			}
		}
		return rawURL
	})
	value = secretJSONPattern.ReplaceAllString(value, `$1"[已脱敏]"`)
	value = secretQuotedPattern.ReplaceAllString(value, `$1'[已脱敏]'`)
	value = secretDoublePattern.ReplaceAllString(value, `$1"[已脱敏]"`)
	value = authorizationPattern.ReplaceAllString(value, `$1[已脱敏]`)
	value = secretValuePattern.ReplaceAllString(value, `$1[已脱敏]`)
	value = telegramTokenPattern.ReplaceAllString(value, "[已脱敏]")
	return accessKeyPattern.ReplaceAllString(value, "[已脱敏]")
}
