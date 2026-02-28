package jwt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fp8/lite-auth-proxy/internal/config"
)

func TestValidateTokenRS256(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+3600, 0)).
		WithClaim("sub", "user123")

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	// Parse token and add it to KeyCache for testing
	parts := strings.Split(token, ".")
	headerData, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var header map[string]interface{}
	_ = json.Unmarshal(headerData, &header)

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	claims, err := validator.ValidateToken(token)
	if err != nil {
		t.Fatalf("Failed to validate token: %v", err)
	}

	if claims["iss"] != "https://example.com" {
		t.Errorf("Expected issuer 'https://example.com', got '%v'", claims["iss"])
	}

	if claims["sub"] != "user123" {
		t.Errorf("Expected sub 'user123', got '%v'", claims["sub"])
	}
}

func TestValidateTokenES256(t *testing.T) {
	ecdsaKey, err := GenerateECDSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate ECDSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("ES256", ecdsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+3600, 0))

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	validator.keyCache.Set("test-key", &ecdsaKey.PublicKey)

	claims, err := validator.ValidateToken(token)
	if err != nil {
		t.Fatalf("Failed to validate token: %v", err)
	}

	if claims["iss"] != "https://example.com" {
		t.Errorf("Expected issuer 'https://example.com', got '%v'", claims["iss"])
	}
}

func TestValidateTokenEdDSA(t *testing.T) {
	eddsaKey, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("Failed to generate Ed25519 key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("EdDSA", eddsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+3600, 0))

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	pk := eddsaKey.Public().(ed25519.PublicKey)
	validator.keyCache.Set("test-key", pk)

	claims, err := validator.ValidateToken(token)
	if err != nil {
		t.Fatalf("Failed to validate token: %v", err)
	}

	if claims["iss"] != "https://example.com" {
		t.Errorf("Expected issuer 'https://example.com', got '%v'", claims["iss"])
	}
}

func TestValidateTokenPS256(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("PS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+3600, 0))

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	claims, err := validator.ValidateToken(token)
	if err != nil {
		t.Fatalf("Failed to validate token: %v", err)
	}

	if claims["iss"] != "https://example.com" {
		t.Errorf("Expected issuer 'https://example.com', got '%v'", claims["iss"])
	}
}

func TestValidateTokenExpired(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(now-7200, 0)).
		WithExpiresAt(time.Unix(now-3600, 0)) // Expired 1 hour ago

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	_, err = validator.ValidateToken(token)
	if err == nil {
		t.Error("Expected error for expired token")
	}

	if !strings.Contains(err.Error(), "expired") && !strings.Contains(err.Error(), "exp") {
		t.Errorf("Expected error about expiration, got: %v", err)
	}
}

func TestValidateTokenNotYetValid(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithNotBefore(time.Unix(now+3600, 0)). // Valid in 1 hour
		WithExpiresAt(time.Unix(now+7200, 0))

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	_, err = validator.ValidateToken(token)
	if err == nil {
		t.Error("Expected error for token not yet valid")
	}

	if !strings.Contains(err.Error(), "not yet valid") && !strings.Contains(err.Error(), "nbf") {
		t.Errorf("Expected error about nbf, got: %v", err)
	}
}

func TestValidateTokenClockTolerance(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 60, // 60 second tolerance
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+30, 0)) // Expires in 30 seconds

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	// Even though token expires in 30s, with 60s tolerance it should be valid
	claims, err := validator.ValidateToken(token)
	if err != nil {
		t.Fatalf("Failed to validate token with clock tolerance: %v", err)
	}

	if claims["iss"] != "https://example.com" {
		t.Error("Token should be valid with clock tolerance")
	}
}

func TestValidateTokenWrongIssuer(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://wrong-issuer.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+3600, 0))

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	_, err = validator.ValidateToken(token)
	if err == nil {
		t.Error("Expected error for wrong issuer")
	}

	if !strings.Contains(err.Error(), "issuer") {
		t.Errorf("Expected error about issuer, got: %v", err)
	}
}

func TestValidateTokenWrongAudience(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("wrong-app").
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+3600, 0))

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	_, err = validator.ValidateToken(token)
	if err == nil {
		t.Error("Expected error for wrong audience")
	}

	if !strings.Contains(err.Error(), "audience") {
		t.Errorf("Expected error about audience, got: %v", err)
	}
}

func TestValidateTokenSymmetricAlgorithmRejected(t *testing.T) {
	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	// Create a fake HS256 token (header.payload.signature)
	header := map[string]interface{}{
		"alg": "HS256",
		"kid": "test-key",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	payload := map[string]interface{}{
		"iss": "https://example.com",
		"aud": "test-app",
		"exp": time.Now().Unix() + 3600,
		"nbf": time.Now().Unix(),
		"iat": time.Now().Unix(),
		"sub": "user123",
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// For HS256, signature would be HMAC-SHA256 of header.payload
	signature := "fake_signature"

	token := fmt.Sprintf("%s.%s.%s", headerB64, payloadB64, signature)

	_, err := validator.ValidateToken(token)
	if err == nil {
		t.Error("Expected error for symmetric algorithm")
	}

	if !strings.Contains(err.Error(), "symmetric") {
		t.Errorf("Expected error about symmetric algorithm, got: %v", err)
	}
}

func TestValidateTokenInvalidFormat(t *testing.T) {
	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	tests := []string{
		"not-a-token",                    // Only 1 part
		"header.payload",                 // Only 2 parts
		"header.payload.sig.extra.parts", // More than 3 parts
	}

	for _, token := range tests {
		_, err := validator.ValidateToken(token)
		if err == nil {
			t.Errorf("Expected error for invalid format: %s", token)
		}
	}
}

func TestValidateTokenMissingKid(t *testing.T) {
	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	// Create a token without kid in header
	header := map[string]interface{}{
		"alg": "RS256",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	payload := map[string]interface{}{
		"iss": "https://example.com",
		"aud": "test-app",
		"exp": time.Now().Unix() + 3600,
		"nbf": time.Now().Unix(),
		"iat": time.Now().Unix(),
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	token := fmt.Sprintf("%s.%s.fake_sig", headerB64, payloadB64)

	_, err := validator.ValidateToken(token)
	if err == nil {
		t.Error("Expected error for missing kid")
	}
}

func TestValidateTokenInvalidBase64(t *testing.T) {
	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	// Create a token with invalid base64
	token := "not-valid-base64!!.payload.signature"

	_, err := validator.ValidateToken(token)
	if err == nil {
		t.Error("Expected error for invalid base64")
	}
}

func TestValidateTokenMultipleAudiences(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithClaim("aud", []string{"other-app", "test-app", "another-app"}). // Array with matching aud
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+3600, 0))

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	claims, err := validator.ValidateToken(token)
	if err != nil {
		t.Fatalf("Failed to validate token with multiple audiences: %v", err)
	}

	if claims["iss"] != "https://example.com" {
		t.Error("Claims should be populated with issuer")
	}
}

func TestValidateTokenInvalidSignature(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	now := time.Now().Unix()
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(now, 0)).
		WithExpiresAt(time.Unix(now+3600, 0))

	token, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build token: %v", err)
	}

	// Tamper with the signature
	parts := strings.Split(token, ".")
	parts[2] = "invalid_signature_here"
	tamperedToken := strings.Join(parts, ".")

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	_, err = validator.ValidateToken(tamperedToken)
	if err == nil {
		t.Error("Expected error for invalid signature")
	}

	// Error could contain "signature", "verify", "verification" or "error" depending on implementation
	if !strings.Contains(strings.ToLower(err.Error()), "signature") &&
		!strings.Contains(strings.ToLower(err.Error()), "verify") &&
		!strings.Contains(strings.ToLower(err.Error()), "error") {
		t.Errorf("Expected error about signature/verification, got: %v", err)
	}
}

func TestValidateTokenMissingExpiration(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	cfg := &config.JWTConfig{
		Issuer:                "https://example.com",
		Audience:              "test-app",
		ToleranceSecs: 30,
		CacheTTLMins:   60,
	}

	validator := NewValidator(cfg)

	// Create a token without exp field
	header := map[string]interface{}{
		"alg": "RS256",
		"kid": "test-key",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	payload := map[string]interface{}{
		"iss": "https://example.com",
		"aud": "test-app",
		"nbf": time.Now().Unix(),
		"iat": time.Now().Unix(),
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Create a valid signature for this payload
	builder := NewTokenBuilder("RS256", rsaKey, "test-key")
	builder.
		WithIssuer("https://example.com").
		WithAudience("test-app").
		WithIssuedAt(time.Unix(time.Now().Unix(), 0)).
		WithExpiresAt(time.Unix(time.Now().Unix()+3600, 0))

	token, _ := builder.Build()
	parts := strings.Split(token, ".")
	malformedToken := fmt.Sprintf("%s.%s.%s", headerB64, payloadB64, parts[2])

	validator.keyCache.Set("test-key", &rsaKey.PublicKey)

	_, err = validator.ValidateToken(malformedToken)
	if err == nil {
		t.Error("Expected error for missing expiration claim")
	}
}

func TestGetInt64Helper(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected int64
		hasError bool
	}{
		{int64(100), 100, false},
		{100, 100, false},
		{float64(100), 100, false},
		{"not-a-number", 0, true},
		{float64(100.5), 0, true}, // Non-integer float
	}

	for _, test := range tests {
		result, err := getInt64(test.input)
		if test.hasError {
			if err == nil {
				t.Errorf("Expected error for input %v", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input %v: %v", test.input, err)
			}
			if result != test.expected {
				t.Errorf("Expected %d, got %d for input %v", test.expected, result, test.input)
			}
		}
	}
}
