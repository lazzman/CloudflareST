package safedata

import (
	"net/url"
	"strings"
)

func SecretParameter(name string) bool {
	for attempt := 0; attempt < 3; attempt++ {
		decoded, err := url.QueryUnescape(name)
		if err != nil || decoded == name {
			break
		}
		name = decoded
	}
	name = strings.ToLower(name)
	name = strings.NewReplacer("_", "", "-", "", ".", "", "[", "", "]", "").Replace(name)
	for _, part := range []string{"token", "secret", "password", "passwd", "credential", "signature", "authorization", "authentication", "accesskey", "apikey", "privatekey", "authkey", "sessionid"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	switch name {
	case "key", "sig", "auth", "hdnts", "hdntl", "hdnea", "gda", "jwt", "bearer", "hmac", "session":
		return true
	}
	return false
}

func URLHasSecrets(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.User != nil {
		return true
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil && parsed.RawQuery != "" {
		return true
	}
	for key := range query {
		if SecretParameter(key) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(parsed.Path), "/webhook") || strings.HasPrefix(strings.ToLower(parsed.Path), "/bot") && strings.EqualFold(parsed.Hostname(), "api.telegram.org") || strings.EqualFold(parsed.Hostname(), "qyapi.weixin.qq.com")
}
