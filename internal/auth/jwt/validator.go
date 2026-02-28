package jwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"crypto/ed25519"

	"github.com/fp8/lite-auth-proxy/internal/config"
)

// Claims represents the decoded JWT payload
type Claims map[string]interface{}

// JWTHeader represents the decoded JWT header
type JWTHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// Validator validates JWT tokens
type Validator struct {
	config   *config.JWTConfig
	keyCache *KeyCache
}

// NewValidator creates a new JWT validator
func NewValidator(cfg *config.JWTConfig) *Validator {
	return &Validator{
		config:   cfg,
		keyCache: NewKeyCache(cfg.CacheTTLMins),
	}
}

// ValidateToken validates a JWT token and returns the claims
func (v *Validator) ValidateToken(tokenString string) (Claims, error) {
	// Parse the token
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format: expected 3 parts, got %d", len(parts))
	}

	headerStr, payloadStr, signatureStr := parts[0], parts[1], parts[2]

	// Decode header
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerStr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode header: %w", err)
	}

	var header JWTHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("failed to parse header: %w", err)
	}

	// Validate algorithm is not symmetric
	if isSymmetricAlgorithm(header.Alg) {
		return nil, fmt.Errorf("symmetric algorithms not allowed, got %s", header.Alg)
	}

	// Validate kid is present
	if header.Kid == "" {
		return nil, fmt.Errorf("kid not found in token header")
	}

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadStr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse payload: %w", err)
	}

	// Validate standard claims
	if err := v.validateStandardClaims(claims); err != nil {
		return nil, err
	}

	// Get public key from JWKS cache
	publicKey, err := v.keyCache.GetPublicKey(header.Kid, v.config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}

	// Verify signature
	if err := v.verifySignature(header.Alg, headerStr, payloadStr, signatureStr, publicKey); err != nil {
		return nil, err
	}

	return claims, nil
}

// validateStandardClaims validates exp, nbf, iss, and aud claims
func (v *Validator) validateStandardClaims(claims Claims) error {
	now := time.Now().Unix()
	clockTolerance := int64(v.config.ToleranceSecs)

	// Validate exp (expiration time)
	if exp, exists := claims["exp"]; exists {
		expTime, err := getInt64(exp)
		if err != nil {
			return fmt.Errorf("invalid exp claim: %w", err)
		}
		if now > expTime+clockTolerance {
			return fmt.Errorf("token expired")
		}
	}

	// Validate nbf (not before)
	if nbf, exists := claims["nbf"]; exists {
		nbfTime, err := getInt64(nbf)
		if err != nil {
			return fmt.Errorf("invalid nbf claim: %w", err)
		}
		if now < nbfTime-clockTolerance {
			return fmt.Errorf("token not yet valid")
		}
	}

	// Validate iss (issuer)
	if iss, exists := claims["iss"]; exists {
		issStr, ok := iss.(string)
		if !ok {
			return fmt.Errorf("iss claim is not a string")
		}
		if issStr != v.config.Issuer {
			return fmt.Errorf("invalid issuer: expected %s, got %s", v.config.Issuer, issStr)
		}
	} else {
		return fmt.Errorf("iss claim not found")
	}

	// Validate aud (audience)
	if aud, exists := claims["aud"]; exists {
		if !v.validateAudience(aud) {
			return fmt.Errorf("invalid audience")
		}
	} else {
		return fmt.Errorf("aud claim not found")
	}

	return nil
}

// validateAudience checks if the audience claim matches the expected audience
func (v *Validator) validateAudience(aud interface{}) bool {
	switch audVal := aud.(type) {
	case string:
		return audVal == v.config.Audience
	case []interface{}:
		// If aud is an array, check if our audience is in it
		for _, a := range audVal {
			if str, ok := a.(string); ok && str == v.config.Audience {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// verifySignature verifies the JWT signature
func (v *Validator) verifySignature(alg, headerStr, payloadStr, signatureStr string, publicKey crypto.PublicKey) error {
	// Decode signature
	sig, err := base64.RawURLEncoding.DecodeString(signatureStr)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Reconstruct the signed message
	message := headerStr + "." + payloadStr
	messageBytes := []byte(message)

	// Verify based on algorithm
	switch alg {
	case "RS256":
		return v.verifyRSA(messageBytes, sig, publicKey, crypto.SHA256)
	case "RS384":
		return v.verifyRSA(messageBytes, sig, publicKey, crypto.SHA384)
	case "RS512":
		return v.verifyRSA(messageBytes, sig, publicKey, crypto.SHA512)
	case "PS256":
		return v.verifyPSS(messageBytes, sig, publicKey, crypto.SHA256)
	case "PS384":
		return v.verifyPSS(messageBytes, sig, publicKey, crypto.SHA384)
	case "PS512":
		return v.verifyPSS(messageBytes, sig, publicKey, crypto.SHA512)
	case "ES256":
		return v.verifyECDSA(messageBytes, sig, publicKey, crypto.SHA256)
	case "ES384":
		return v.verifyECDSA(messageBytes, sig, publicKey, crypto.SHA384)
	case "ES512":
		return v.verifyECDSA(messageBytes, sig, publicKey, crypto.SHA512)
	case "EdDSA":
		return v.verifyEdDSA(messageBytes, sig, publicKey)
	default:
		return fmt.Errorf("unsupported algorithm: %s", alg)
	}
}

// verifyRSA verifies an RSA PKCS#1 v1.5 signature
func (v *Validator) verifyRSA(message, signature []byte, publicKey crypto.PublicKey, hashAlg crypto.Hash) error {
	rsaKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("public key is not an RSA key")
	}

	h := hashAlg.New()
	h.Write(message)
	digest := h.Sum(nil)

	return rsa.VerifyPKCS1v15(rsaKey, hashAlg, digest, signature)
}

// verifyPSS verifies an RSA PSS signature
func (v *Validator) verifyPSS(message, signature []byte, publicKey crypto.PublicKey, hashAlg crypto.Hash) error {
	rsaKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("public key is not an RSA key")
	}

	h := hashAlg.New()
	h.Write(message)
	digest := h.Sum(nil)

	return rsa.VerifyPSS(rsaKey, hashAlg, digest, signature, nil)
}

// verifyECDSA verifies an ECDSA signature
func (v *Validator) verifyECDSA(message, signature []byte, publicKey crypto.PublicKey, hashAlg crypto.Hash) error {
	ecdsaKey, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("public key is not an ECDSA key")
	}

	h := hashAlg.New()
	h.Write(message)
	digest := h.Sum(nil)

	return verifyECDSASignature(ecdsaKey, digest, signature)
}

// verifyEdDSA verifies an Ed25519 signature
func (v *Validator) verifyEdDSA(message, signature []byte, publicKey crypto.PublicKey) error {
	eddsaKey, ok := publicKey.(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("public key is not an Ed25519 key")
	}

	if !ed25519.Verify(eddsaKey, message, signature) {
		return fmt.Errorf("invalid token signature")
	}

	return nil
}

// verifyECDSASignature verifies an ECDSA signature in raw format (r||s)
func verifyECDSASignature(key *ecdsa.PublicKey, hash, sig []byte) error {
	if len(sig)%2 != 0 {
		return fmt.Errorf("invalid ECDSA signature length")
	}

	keySize := len(sig) / 2
	r := new(big.Int).SetBytes(sig[:keySize])
	s := new(big.Int).SetBytes(sig[keySize:])

	if !ecdsa.Verify(key, hash, r, s) {
		return fmt.Errorf("invalid token signature")
	}

	return nil
}

// isSymmetricAlgorithm returns true if the algorithm is symmetric (not allowed)
func isSymmetricAlgorithm(alg string) bool {
	return strings.HasPrefix(alg, "HS") // HS256, HS384, HS512
}

// getInt64 converts an interface{} to int64
func getInt64(v interface{}) (int64, error) {
	switch val := v.(type) {
	case float64:
		if val == math.Ceil(val) {
			return int64(val), nil
		}
		return 0, fmt.Errorf("float64 has fractional part")
	case int:
		return int64(val), nil
	case int64:
		return val, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}
