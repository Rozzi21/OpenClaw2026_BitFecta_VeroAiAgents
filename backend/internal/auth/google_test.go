package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
)

// signTestIDToken mints a signed id_token with a throwaway RSA key so tests can
// assert signature/issuer/audience/expiry enforcement without any network.
func signTestIDToken(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-kid"
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign id_token: %v", err)
	}
	return raw
}

func baseClaims(clientID, nonce string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":            "https://accounts.google.com",
		"aud":            clientID,
		"sub":            "google-sub-123",
		"exp":            time.Now().Add(10 * time.Minute).Unix(),
		"iat":            time.Now().Unix(),
		"email":          "user@gmail.com",
		"email_verified": true,
		"name":           "Test User",
		"nonce":          nonce,
	}
}

func newKeyClient(t *testing.T, key *rsa.PrivateKey, clientID string) *GoogleClient {
	t.Helper()
	keySet := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}
	return newGoogleClientWithKeySet(clientID, "http://localhost/cb", keySet)
}

func genKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	return k
}

func TestVerifyIDToken_AcceptsValid(t *testing.T) {
	key := genKey(t)
	client := newKeyClient(t, key, "cid")
	raw := signTestIDToken(t, key, baseClaims("cid", "nonce-1"))
	id, err := client.verifyIDToken(context.Background(), raw, "nonce-1")
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if id.Subject != "google-sub-123" || id.Email != "user@gmail.com" || !id.EmailVerified {
		t.Errorf("unexpected identity: %+v", id)
	}
}

func TestVerifyIDToken_RejectsBadSignature(t *testing.T) {
	key := genKey(t)
	other := genKey(t)
	// Client trusts `key`, but token is signed by `other` → signature invalid.
	client := newKeyClient(t, key, "cid")
	raw := signTestIDToken(t, other, baseClaims("cid", "nonce-1"))
	if _, err := client.verifyIDToken(context.Background(), raw, "nonce-1"); !errors.Is(err, ErrGoogleInvalidIDToken) {
		t.Fatalf("expected ErrGoogleInvalidIDToken for bad signature, got %v", err)
	}
}

func TestVerifyIDToken_RejectsWrongIssuer(t *testing.T) {
	key := genKey(t)
	client := newKeyClient(t, key, "cid")
	claims := baseClaims("cid", "nonce-1")
	claims["iss"] = "https://evil.example.com"
	raw := signTestIDToken(t, key, claims)
	// go-oidc rejects iss mismatch as invalid token (verifier pins issuer).
	if _, err := client.verifyIDToken(context.Background(), raw, "nonce-1"); err == nil {
		t.Fatal("expected rejection for wrong issuer")
	}
}

func TestVerifyIDToken_RejectsWrongAudience(t *testing.T) {
	key := genKey(t)
	client := newKeyClient(t, key, "cid")
	claims := baseClaims("cid", "nonce-1")
	claims["aud"] = "some-other-client"
	raw := signTestIDToken(t, key, claims)
	if _, err := client.verifyIDToken(context.Background(), raw, "nonce-1"); err == nil {
		t.Fatal("expected rejection for wrong audience")
	}
}

func TestVerifyIDToken_RejectsExpired(t *testing.T) {
	key := genKey(t)
	client := newKeyClient(t, key, "cid")
	claims := baseClaims("cid", "nonce-1")
	claims["exp"] = time.Now().Add(-time.Minute).Unix()
	raw := signTestIDToken(t, key, claims)
	if _, err := client.verifyIDToken(context.Background(), raw, "nonce-1"); err == nil {
		t.Fatal("expected rejection for expired token")
	}
}

func TestVerifyIDToken_RejectsEmailUnverified(t *testing.T) {
	key := genKey(t)
	client := newKeyClient(t, key, "cid")
	claims := baseClaims("cid", "nonce-1")
	claims["email_verified"] = false
	raw := signTestIDToken(t, key, claims)
	if _, err := client.verifyIDToken(context.Background(), raw, "nonce-1"); !errors.Is(err, ErrGoogleEmailUnverified) {
		t.Fatalf("expected ErrGoogleEmailUnverified, got %v", err)
	}
}

func TestVerifyIDToken_RejectsNonceMismatch(t *testing.T) {
	key := genKey(t)
	client := newKeyClient(t, key, "cid")
	raw := signTestIDToken(t, key, baseClaims("cid", "nonce-A"))
	if _, err := client.verifyIDToken(context.Background(), raw, "nonce-B"); !errors.Is(err, ErrGoogleNonceMismatch) {
		t.Fatalf("expected ErrGoogleNonceMismatch, got %v", err)
	}
}

// Ensure claims JSON shape is what we expect (guard against accidental rename).
func TestGoogleClaims_JSONTags(t *testing.T) {
	var c googleClaims
	if err := json.Unmarshal([]byte(`{"email":"a@b.c","email_verified":true,"name":"N","picture":"P","nonce":"X"}`), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Email != "a@b.c" || !c.EmailVerified || c.Name != "N" || c.Picture != "P" || c.Nonce != "X" {
		t.Errorf("claims mapping broken: %+v", c)
	}
}
