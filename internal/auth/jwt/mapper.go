package jwt

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MapClaims maps JWT claims to HTTP headers based on configured mappings.
// Missing claims are skipped silently.
func MapClaims(claims Claims, mappings map[string]string, headerPrefix string) map[string]string {
	headers := make(map[string]string)

	for claimKey, headerSuffix := range mappings {
		claimValue, exists := claims[claimKey]
		if !exists {
			continue
		}

		headerValue, ok := coerceClaimToHeaderValue(claimValue)
		if !ok {
			continue
		}

		headers[headerPrefix+headerSuffix] = headerValue
	}

	return headers
}

func coerceClaimToHeaderValue(value interface{}) (string, bool) {
	switch v := value.(type) {
	case nil:
		return "", false
	case string:
		return v, true
	case float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%v", v), true
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
		return strings.Join(parts, ","), true
	case map[string]interface{}:
		jsonValue, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		return string(jsonValue), true
	default:
		jsonValue, err := json.Marshal(v)
		if err == nil && string(jsonValue) != "null" {
			if strings.HasPrefix(string(jsonValue), "{") || strings.HasPrefix(string(jsonValue), "[") {
				return string(jsonValue), true
			}
		}

		return fmt.Sprintf("%v", v), true
	}
}
