package thordata

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var sensitiveURLPattern = regexp.MustCompile(`(?i)https?://[^\s"<>]*(?:api_key|access_token|authorization|sign|token)=[^\s"<>]*`)
var sensitiveFieldPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|authorization|sign|token)\s*[:=]\s*[^\s,;"}]+`)

func redactText(value any, secrets []string) string {
	if value == nil {
		return ""
	}
	text := sensitiveURLPattern.ReplaceAllString(strings.TrimSpace(stringValue(value)), "[redacted URL]")
	text = sensitiveFieldPattern.ReplaceAllString(text, "$1: [redacted]")
	text = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, text)
	text = replaceSecrets(text, secrets)
	runes := []rune(text)
	if len(runes) > 250 {
		text = string(runes[:250])
	}
	return strings.TrimSpace(text)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return strings.TrimSpace(toJSONText(value))
}

func toJSONText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[unprintable]"
	}
	return string(encoded)
}

func replaceSecrets(text string, secrets []string) string {
	variants := make([]string, 0, len(secrets)*3)
	for _, secret := range secrets {
		if secret != "" {
			variants = append(variants, secret, url.QueryEscape(secret), url.PathEscape(secret))
		}
	}
	sort.Slice(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	for _, secret := range variants {
		var pattern strings.Builder
		for index := 0; index < len(secret); {
			if secret[index] == '%' && index+2 < len(secret) && isHex(secret[index+1]) && isHex(secret[index+2]) {
				pattern.WriteString("(?i:" + regexp.QuoteMeta(secret[index:index+3]) + ")")
				index += 3
			} else {
				pattern.WriteString(regexp.QuoteMeta(secret[index : index+1]))
				index++
			}
		}
		text = regexp.MustCompile(pattern.String()).ReplaceAllString(text, "[redacted]")
	}
	return text
}
func isHex(char byte) bool {
	return char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F'
}

func redactValue(value any, secrets []string) any {
	switch typed := value.(type) {
	case string:
		return replaceSecrets(sensitiveURLPattern.ReplaceAllString(typed, "[redacted URL]"), secrets)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactValue(item, secrets)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			redactedKey := replaceSecrets(key, secrets)
			result[redactedKey] = redactValue(item, secrets)
		}
		return result
	default:
		return value
	}
}
