//go:build integration

package jwt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/fp8/lite-auth-proxy/internal/config"
)

// FirebaseSignInResponse represents the response from Firebase authentication
type FirebaseSignInResponse struct {
	IDToken      string `json:"idToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    string `json:"expiresIn"`
	LocalID      string `json:"localId"`
	Email        string `json:"email"`
	Registered   bool   `json:"registered"`
}

// getSecretFromGCP retrieves a secret from Google Cloud Secret Manager
func getSecretFromGCP(ctx context.Context, projectID, secretName string) (string, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create secret manager client: %w", err)
	}
	defer client.Close()

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretName),
	}

	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to access secret: %w", err)
	}

	return string(result.Payload.Data), nil
}

// signInWithFirebase authenticates with Firebase and returns the ID token
func signInWithFirebase(ctx context.Context, apiKey, email, password string) (string, error) {
	firebaseURL := fmt.Sprintf(
		"https://identitytoolkit.googleapis.com/v1/accounts:signInWithPassword?key=%s",
		apiKey,
	)

	payload := map[string]interface{}{
		"email":             email,
		"password":          password,
		"returnSecureToken": true,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, firebaseURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Firebase API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Firebase API returned status %d: %s", resp.StatusCode, string(body))
	}

	var signInResp FirebaseSignInResponse
	if err := json.NewDecoder(resp.Body).Decode(&signInResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if signInResp.IDToken == "" {
		return "", fmt.Errorf("no ID token in response")
	}

	return signInResp.IDToken, nil
}

// TestValidateRealWorldFirebaseJWT validates a real JWT from Firebase
func TestValidateRealWorldFirebaseJWT(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get configuration from environment
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		t.Skip("Skipping real-world test: GOOGLE_CLOUD_PROJECT not set")
	}

	apikeySecretName := os.Getenv("FIREBASE_API_KEY_SECRET_NAME")
	if apikeySecretName == "" {
		t.Skip("Skipping real-world test: FIREBASE_API_KEY_SECRET_NAME not set")
	}

	loginSecretName := os.Getenv("FIREBASE_LOGIN_SECRET_NAME")
	if loginSecretName == "" {
		t.Skip("Skipping real-world test: FIREBASE_LOGIN_SECRET_NAME not set")
	}

	// Read secrets from GCP Secret Manager
	apiKey, err := getSecretFromGCP(ctx, projectID, apikeySecretName)
	if err != nil {
		t.Skipf("Skipping real-world test: could not read secret %s: %v", apikeySecretName, err)
	}

	loginStr, err := getSecretFromGCP(ctx, projectID, loginSecretName)
	if err != nil {
		t.Skipf("Skipping real-world test: could not read secret %s: %v", loginSecretName, err)
	}

	// Parse email:password from secret
	parts := strings.SplitN(loginStr, ":", 2)
	if len(parts) != 2 {
		t.Skipf("Skipping real-world test: LOGIN_FIREBASE_AUTH_DEV secret format invalid (expected email:password)")
	}

	email := parts[0]
	password := parts[1]

	// Authenticate with Firebase
	idToken, err := signInWithFirebase(ctx, apiKey, email, password)
	if err != nil {
		t.Fatalf("Failed to sign in with Firebase: %v", err)
	}

	t.Logf("Successfully obtained ID token from Firebase")

	// Create validator for Firebase JWT
	cfg := &config.JWTConfig{
		Enabled:       true,
		Issuer:        fmt.Sprintf("https://securetoken.google.com/%s", projectID),
		Audience:      projectID,
		ToleranceSecs: 30,
		CacheTTLMins:  60,
	}

	validator := NewValidator(cfg)

	// Validate the token
	claims, err := validator.ValidateToken(idToken)
	if err != nil {
		t.Fatalf("Failed to validate Firebase JWT: %v", err)
	}

	t.Logf("Successfully validated Firebase JWT")

	// Verify expected claims
	expectedIssuer := fmt.Sprintf("https://securetoken.google.com/%s", projectID)
	if claims["iss"] != expectedIssuer {
		t.Errorf("Expected issuer '%s', got '%v'", expectedIssuer, claims["iss"])
	}

	if claims["aud"] != projectID {
		t.Errorf("Expected audience '%s', got '%v'", projectID, claims["aud"])
	}

	if claims["email"] != email {
		t.Errorf("Expected email '%s', got '%v'", email, claims["email"])
	}

	// Verify token is not expired
	if expTime, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(expTime)+int64(cfg.ToleranceSecs) {
			t.Error("Token is expired")
		}
	} else {
		t.Error("exp claim not found or not a number")
	}

	// Verify standard claims exist
	requiredClaims := []string{"sub", "iat", "auth_time"}
	for _, claim := range requiredClaims {
		if _, exists := claims[claim]; !exists {
			t.Errorf("Required claim '%s' not found in token", claim)
		}
	}

	t.Log("All JWT claims validated successfully")
	t.Logf("Claims: iss=%v, aud=%v, email=%v, sub=%v, user_id=%v",
		claims["iss"], claims["aud"], claims["email"], claims["sub"], claims["user_id"])
}
