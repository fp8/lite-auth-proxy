package jwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// TokenBuilder helps construct test JWT tokens
type TokenBuilder struct {
	header map[string]interface{}
	claims map[string]interface{}
	key    interface{}
	alg    string
}

// NewTokenBuilder creates a new token builder
func NewTokenBuilder(alg string, key interface{}, kid string) *TokenBuilder {
	return &TokenBuilder{
		alg: alg,
		key: key,
		header: map[string]interface{}{
			"alg": alg,
			"kid": kid,
			"typ": "JWT",
		},
		claims: make(map[string]interface{}),
	}
}

// WithClaim adds a claim to the token
func (tb *TokenBuilder) WithClaim(key string, value interface{}) *TokenBuilder {
	tb.claims[key] = value
	return tb
}

// WithIssuedAt sets the iat claim
func (tb *TokenBuilder) WithIssuedAt(t time.Time) *TokenBuilder {
	tb.claims["iat"] = t.Unix()
	return tb
}

// WithExpiresAt sets the exp claim
func (tb *TokenBuilder) WithExpiresAt(t time.Time) *TokenBuilder {
	tb.claims["exp"] = t.Unix()
	return tb
}

// WithNotBefore sets the nbf claim
func (tb *TokenBuilder) WithNotBefore(t time.Time) *TokenBuilder {
	tb.claims["nbf"] = t.Unix()
	return tb
}

// WithIssuer sets the iss claim
func (tb *TokenBuilder) WithIssuer(issuer string) *TokenBuilder {
	tb.claims["iss"] = issuer
	return tb
}

// WithAudience sets the aud claim
func (tb *TokenBuilder) WithAudience(aud string) *TokenBuilder {
	tb.claims["aud"] = aud
	return tb
}

// Build creates a signed JWT token
func (tb *TokenBuilder) Build() (string, error) {
	// Encode header
	headerJSON, err := json.Marshal(tb.header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %w", err)
	}
	headerEncoded := base64.RawURLEncoding.EncodeToString(headerJSON)

	// Encode claims
	claimsJSON, err := json.Marshal(tb.claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal claims: %w", err)
	}
	claimsEncoded := base64.RawURLEncoding.EncodeToString(claimsJSON)

	// Create signature
	message := headerEncoded + "." + claimsEncoded
	signature, err := tb.sign([]byte(message))
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	signatureEncoded := base64.RawURLEncoding.EncodeToString(signature)

	return message + "." + signatureEncoded, nil
}

// sign creates a signature for the message
func (tb *TokenBuilder) sign(message []byte) ([]byte, error) {
	switch tb.alg {
	case "RS256", "RS384", "RS512":
		return tb.signRSA(message)
	case "PS256", "PS384", "PS512":
		return tb.signPSS(message)
	case "ES256", "ES384", "ES512":
		return tb.signECDSA(message)
	case "EdDSA":
		return tb.signEdDSA(message)
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", tb.alg)
	}
}

func (tb *TokenBuilder) signRSA(message []byte) ([]byte, error) {
	rsaKey, ok := tb.key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not an RSA private key")
	}

	h := getHashAlgorithm(tb.alg)
	if h == 0 {
		return nil, fmt.Errorf("unsupported hash algorithm")
	}

	hashed := h.New()
	hashed.Write(message)
	digest := hashed.Sum(nil)

	return rsa.SignPKCS1v15(rand.Reader, rsaKey, h, digest)
}

func (tb *TokenBuilder) signPSS(message []byte) ([]byte, error) {
	rsaKey, ok := tb.key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not an RSA private key")
	}

	h := getHashAlgorithm(tb.alg)
	if h == 0 {
		return nil, fmt.Errorf("unsupported hash algorithm")
	}

	hashed := h.New()
	hashed.Write(message)
	digest := hashed.Sum(nil)

	return rsa.SignPSS(rand.Reader, rsaKey, h, digest, nil)
}

func (tb *TokenBuilder) signECDSA(message []byte) ([]byte, error) {
	ecdsaKey, ok := tb.key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not an ECDSA private key")
	}

	h := getHashAlgorithm(tb.alg)
	if h == 0 {
		return nil, fmt.Errorf("unsupported hash algorithm")
	}

	hashed := h.New()
	hashed.Write(message)
	digest := hashed.Sum(nil)

	r, s, err := ecdsa.Sign(rand.Reader, ecdsaKey, digest)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	// Determine key size based on curve
	size := (ecdsaKey.Curve.Params().BitSize + 7) / 8

	// Convert r and s to fixed-length bytes
	rBytes := r.Bytes()
	sBytes := s.Bytes()

	// Pad with zeros if necessary
	rPadded := make([]byte, size)
	sPadded := make([]byte, size)
	copy(rPadded[size-len(rBytes):], rBytes)
	copy(sPadded[size-len(sBytes):], sBytes)

	return append(rPadded, sPadded...), nil
}

func (tb *TokenBuilder) signEdDSA(message []byte) ([]byte, error) {
	eddsaKey, ok := tb.key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not an Ed25519 private key")
	}

	return ed25519.Sign(eddsaKey, message), nil
}

// getHashAlgorithm returns the hash algorithm for JWT algorithms
func getHashAlgorithm(alg string) crypto.Hash {
	switch alg {
	case "RS256", "PS256", "ES256":
		return crypto.SHA256
	case "RS384", "PS384", "ES384":
		return crypto.SHA384
	case "RS512", "PS512", "ES512":
		return crypto.SHA512
	default:
		return 0
	}
}

// GenerateRSAKeyPair generates an RSA key pair for testing
func GenerateRSAKeyPair() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

// GenerateECDSAKeyPair generates an ECDSA key pair for testing
func GenerateECDSAKeyPair() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// GenerateEd25519KeyPair generates an Ed25519 key pair for testing
func GenerateEd25519KeyPair() (ed25519.PrivateKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	return key, err
}
