package apikey

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/fp8/lite-auth-proxy/internal/config"
)

var (
	ErrMissingAPIKey = errors.New("missing api key")
	ErrInvalidAPIKey = errors.New("invalid api key")
)

// ValidateAPIKey validates API-key credentials and returns injected auth headers on success.
// When API-key auth is disabled, it returns (nil, nil).
func ValidateAPIKey(request *http.Request, authConfig *config.AuthConfig) (map[string]string, error) {
	if authConfig == nil {
		return nil, fmt.Errorf("auth config is required")
	}

	if !authConfig.APIKey.Enabled {
		return nil, nil
	}

	headerName := authConfig.APIKey.Name
	if headerName == "" {
		headerName = "X-API-KEY"
	}

	providedValue := request.Header.Get(headerName)
	if providedValue == "" {
		return nil, ErrMissingAPIKey
	}

	if !constantTimeEquals(providedValue, authConfig.APIKey.Value) {
		return nil, ErrInvalidAPIKey
	}

	headers := make(map[string]string, len(authConfig.APIKey.Payload))
	for key, value := range authConfig.APIKey.Payload {
		headerKey := authConfig.HeaderPrefix + strings.ToUpper(key)
		headers[headerKey] = value
	}

	return headers, nil
}

func constantTimeEquals(left, right string) bool {
	leftBytes := []byte(left)
	rightBytes := []byte(right)
	maxLen := len(leftBytes)
	if len(rightBytes) > maxLen {
		maxLen = len(rightBytes)
	}

	result := byte(len(leftBytes) ^ len(rightBytes))
	for i := 0; i < maxLen; i++ {
		var leftByte byte
		if i < len(leftBytes) {
			leftByte = leftBytes[i]
		}

		var rightByte byte
		if i < len(rightBytes) {
			rightByte = rightBytes[i]
		}

		result |= leftByte ^ rightByte
	}

	return subtle.ConstantTimeByteEq(result, 0) == 1
}
