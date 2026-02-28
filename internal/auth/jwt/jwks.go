package jwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"crypto/ed25519"
)

// JWK represents a JSON Web Key
type JWK struct {
	KTy string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`

	// RSA keys
	N string `json:"n"`
	E string `json:"e"`

	// EC keys
	X string `json:"x"`
	Y string `json:"y"`
}

// JWKS represents a JSON Web Key Set
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// OIDCConfig represents an OpenID Connect discovery document
type OIDCConfig struct {
	JWKSUri string `json:"jwks_uri"`
}

// CachedKey holds a public key with expiration
type CachedKey struct {
	Key       crypto.PublicKey
	ExpiresAt time.Time
}

// KeyCache manages thread-safe key caching
type KeyCache struct {
	mu     sync.RWMutex
	cache  map[string]CachedKey
	ttl    time.Duration
	client *http.Client
}

// NewKeyCache creates a new key cache with the specified TTL
func NewKeyCache(ttlMinutes int) *KeyCache {
	if ttlMinutes == 0 {
		ttlMinutes = 1440 // 24 hours default
	}

	return &KeyCache{
		cache: make(map[string]CachedKey),
		ttl:   time.Duration(ttlMinutes) * time.Minute,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Get retrieves a key from cache if available and not expired
func (kc *KeyCache) Get(kid string) (crypto.PublicKey, bool) {
	kc.mu.RLock()
	defer kc.mu.RUnlock()

	cached, exists := kc.cache[kid]
	if !exists {
		return nil, false
	}

	if time.Now().After(cached.ExpiresAt) {
		return nil, false
	}

	return cached.Key, true
}

// Set stores a key in the cache with expiration
func (kc *KeyCache) Set(kid string, key crypto.PublicKey) {
	kc.mu.Lock()
	defer kc.mu.Unlock()

	kc.cache[kid] = CachedKey{
		Key:       key,
		ExpiresAt: time.Now().Add(kc.ttl),
	}
}

// DiscoverJWKSUri fetches the JWKS URI from an OpenID Connect discovery endpoint
func (kc *KeyCache) DiscoverJWKSUri(issuer string) (string, error) {
	discoveryURL := issuer + "/.well-known/openid-configuration"

	resp, err := kc.client.Get(discoveryURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OIDC discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OIDC discovery returned status %d: %s", resp.StatusCode, string(body))
	}

	var config OIDCConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return "", fmt.Errorf("failed to parse OIDC discovery response: %w", err)
	}

	if config.JWKSUri == "" {
		return "", fmt.Errorf("jwks_uri not found in OIDC discovery response")
	}

	return config.JWKSUri, nil
}

// FetchJWKS fetches the JSON Web Key Set from the given URI
func (kc *KeyCache) FetchJWKS(jwksURI string) (*JWKS, error) {
	resp, err := kc.client.Get(jwksURI)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("JWKS endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("failed to parse JWKS response: %w", err)
	}

	return &jwks, nil
}

// GetPublicKey retrieves a public key by kid, fetching from JWKS if not cached
func (kc *KeyCache) GetPublicKey(kid string, issuer string) (crypto.PublicKey, error) {
	// Check cache first
	if key, found := kc.Get(kid); found {
		return key, nil
	}

	// Discover JWKS URI
	jwksURI, err := kc.DiscoverJWKSUri(issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to discover JWKS URI: %w", err)
	}

	// Fetch JWKS
	jwks, err := kc.FetchJWKS(jwksURI)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}

	// Find the key with matching kid
	for _, jwk := range jwks.Keys {
		if jwk.Kid == kid {
			key, err := jwkToPublicKey(jwk)
			if err != nil {
				return nil, fmt.Errorf("failed to convert JWK to public key: %w", err)
			}

			// Cache the key
			kc.Set(kid, key)
			return key, nil
		}
	}

	return nil, fmt.Errorf("key with kid '%s' not found in JWKS", kid)
}

// jwkToPublicKey converts a JWK to a Go public key
func jwkToPublicKey(jwk JWK) (crypto.PublicKey, error) {
	switch jwk.KTy {
	case "RSA":
		return jwkToRSAPublicKey(jwk)
	case "EC":
		return jwkToECPublicKey(jwk)
	case "OKP":
		return jwkToOKPPublicKey(jwk)
	default:
		return nil, fmt.Errorf("unsupported key type: %s", jwk.KTy)
	}
}

// jwkToRSAPublicKey converts a JWK to an RSA public key
func jwkToRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	// Decode N (modulus)
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode N: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)

	// Decode E (exponent) - default to 65537 if not provided
	e := 65537
	if jwk.E != "" {
		eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
		if err != nil {
			return nil, fmt.Errorf("failed to decode E: %w", err)
		}
		e = int(new(big.Int).SetBytes(eBytes).Int64())
	}

	return &rsa.PublicKey{
		N: n,
		E: e,
	}, nil
}

// jwkToECPublicKey converts a JWK to an ECDSA public key
func jwkToECPublicKey(jwk JWK) (*ecdsa.PublicKey, error) {
	// Decode X and Y coordinates
	xBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("failed to decode X: %w", err)
	}

	yBytes, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil {
		return nil, fmt.Errorf("failed to decode Y: %w", err)
	}

	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)

	// Determine curve based on crv field
	var curve elliptic.Curve
	switch jwk.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported EC curve: %s", jwk.Crv)
	}

	return &ecdsa.PublicKey{
		Curve: curve,
		X:     x,
		Y:     y,
	}, nil
}

// jwkToOKPPublicKey converts a JWK to an Ed25519 public key
func jwkToOKPPublicKey(jwk JWK) (ed25519.PublicKey, error) {
	if jwk.Crv != "Ed25519" {
		return nil, fmt.Errorf("unsupported OKP curve: %s", jwk.Crv)
	}

	// Decode X coordinate
	xBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("failed to decode X: %w", err)
	}

	if len(xBytes) != 32 {
		return nil, fmt.Errorf("invalid Ed25519 key length: expected 32, got %d", len(xBytes))
	}

	return ed25519.PublicKey(xBytes), nil
}
