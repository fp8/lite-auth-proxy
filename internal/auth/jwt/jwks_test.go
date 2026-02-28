package jwt

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKeyCache(t *testing.T) {
	cache := NewKeyCache(60)
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	publicKey := rsaKey.PublicKey
	cache.Set("test-kid", &publicKey)
	retrieved, found := cache.Get("test-kid")
	if !found {
		t.Error("Expected key to be in cache")
	}

	retrievedRSA, ok := retrieved.(*rsa.PublicKey)
	if !ok {
		t.Error("Retrieved key is not an RSA key")
	}

	if retrievedRSA.N.Cmp(publicKey.N) != 0 {
		t.Error("Retrieved key does not match original key")
	}
}

func TestKeyCacheExpiry(t *testing.T) {
	cache := NewKeyCache(0)
	cache.ttl = 1 * time.Millisecond

	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	publicKey := rsaKey.PublicKey
	cache.Set("test-kid", &publicKey)

	_, found := cache.Get("test-kid")
	if !found {
		t.Error("Expected key to be in cache immediately after setting")
	}

	time.Sleep(5 * time.Millisecond)

	_, found = cache.Get("test-kid")
	if found {
		t.Error("Expected key to be expired")
	}
}

func TestDiscoverJWKSUri(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}

		config := OIDCConfig{
			JWKSUri: "https://example.com/.well-known/jwks.json",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(config)
	}))
	defer server.Close()

	cache := NewKeyCache(60)
	uri, err := cache.DiscoverJWKSUri(server.URL)
	if err != nil {
		t.Fatalf("Failed to discover JWKS URI: %v", err)
	}

	if uri != "https://example.com/.well-known/jwks.json" {
		t.Errorf("Expected URI 'https://example.com/.well-known/jwks.json', got '%s'", uri)
	}
}

func TestDiscoverJWKSUriError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cache := NewKeyCache(60)
	_, err := cache.DiscoverJWKSUri(server.URL)
	if err == nil {
		t.Error("Expected error when discovering JWKS URI")
	}
}

func TestFetchJWKS(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwks := JWKS{
			Keys: []JWK{
				{
					KTy: "RSA",
					Kid: "test-key-1",
					Use: "sig",
					Alg: "RS256",
					N:   base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
					E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes()),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer server.Close()

	cache := NewKeyCache(60)
	jwks, err := cache.FetchJWKS(server.URL)
	if err != nil {
		t.Fatalf("Failed to fetch JWKS: %v", err)
	}

	if len(jwks.Keys) != 1 {
		t.Errorf("Expected 1 key, got %d", len(jwks.Keys))
	}

	if jwks.Keys[0].Kid != "test-key-1" {
		t.Errorf("Expected kid 'test-key-1', got '%s'", jwks.Keys[0].Kid)
	}
}

func TestJWKToRSAPublicKey(t *testing.T) {
	rsaKey, err := GenerateRSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}

	jwk := JWK{
		KTy: "RSA",
		Kid: "test-key",
		N:   base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes()),
	}

	key, err := jwkToRSAPublicKey(jwk)
	if err != nil {
		t.Fatalf("Failed to convert JWK to RSA key: %v", err)
	}

	if key.N.Cmp(rsaKey.N) != 0 {
		t.Error("Converted key N does not match")
	}
	if key.E != rsaKey.E {
		t.Errorf("Expected E %d, got %d", rsaKey.E, key.E)
	}
}

func TestJWKToECDSAPublicKey(t *testing.T) {
	ecdsaKey, err := GenerateECDSAKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate ECDSA key: %v", err)
	}

	jwk := JWK{
		KTy: "EC",
		Kid: "test-key",
		Crv: "P-256",
		X:   base64.RawURLEncoding.EncodeToString(ecdsaKey.X.Bytes()),
		Y:   base64.RawURLEncoding.EncodeToString(ecdsaKey.Y.Bytes()),
	}

	key, err := jwkToECPublicKey(jwk)
	if err != nil {
		t.Fatalf("Failed to convert JWK to ECDSA key: %v", err)
	}

	if key.X.Cmp(ecdsaKey.X) != 0 {
		t.Error("Converted key X does not match")
	}
	if key.Y.Cmp(ecdsaKey.Y) != 0 {
		t.Error("Converted key Y does not match")
	}
}

func TestUnsupportedKeyType(t *testing.T) {
	jwk := JWK{
		KTy: "UNKNOWN",
		Kid: "test-key",
	}

	_, err := jwkToPublicKey(jwk)
	if err == nil {
		t.Error("Expected error for unsupported key type")
	}
}

func TestUnsupportedECCurve(t *testing.T) {
	jwk := JWK{
		KTy: "EC",
		Kid: "test-key",
		Crv: "P-999",
		X:   "AQAB",
		Y:   "AQAB",
	}

	_, err := jwkToECPublicKey(jwk)
	if err == nil {
		t.Error("Expected error for unsupported EC curve")
	}
}
