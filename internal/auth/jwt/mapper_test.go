package jwt

import (
	"strings"
	"testing"
)

func TestMapClaimsStringClaim(t *testing.T) {
	claims := Claims{"email": "alice@company.com"}
	mappings := map[string]string{"email": "USER-EMAIL"}

	headers := MapClaims(claims, mappings, "X-AUTH-")

	if headers["X-AUTH-USER-EMAIL"] != "alice@company.com" {
		t.Fatalf("expected mapped email header, got: %v", headers["X-AUTH-USER-EMAIL"])
	}
}

func TestMapClaimsArrayClaimToCSV(t *testing.T) {
	claims := Claims{"roles": []interface{}{"a", "b", "c"}}
	mappings := map[string]string{"roles": "ROLES"}

	headers := MapClaims(claims, mappings, "X-AUTH-")

	if headers["X-AUTH-ROLES"] != "a,b,c" {
		t.Fatalf("expected CSV array mapping, got: %v", headers["X-AUTH-ROLES"])
	}
}

func TestMapClaimsObjectClaimToJSON(t *testing.T) {
	claims := Claims{
		"profile": map[string]interface{}{
			"team": "platform",
			"level": "senior",
		},
	}
	mappings := map[string]string{"profile": "PROFILE"}

	headers := MapClaims(claims, mappings, "X-AUTH-")
	jsonValue := headers["X-AUTH-PROFILE"]

	if !strings.Contains(jsonValue, `"team":"platform"`) {
		t.Fatalf("expected JSON object mapping, got: %v", jsonValue)
	}
}

func TestMapClaimsMissingClaimSkipped(t *testing.T) {
	claims := Claims{"sub": "user-1"}
	mappings := map[string]string{"email": "USER-EMAIL"}

	headers := MapClaims(claims, mappings, "X-AUTH-")

	if len(headers) != 0 {
		t.Fatalf("expected missing claim to be skipped, got headers: %v", headers)
	}
}

func TestMapClaimsNumericClaimToString(t *testing.T) {
	claims := Claims{"tenant_id": float64(42)}
	mappings := map[string]string{"tenant_id": "TENANT-ID"}

	headers := MapClaims(claims, mappings, "X-AUTH-")

	if headers["X-AUTH-TENANT-ID"] != "42" {
		t.Fatalf("expected numeric claim to be mapped as string, got: %v", headers["X-AUTH-TENANT-ID"])
	}
}
