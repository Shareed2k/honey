// Package webauthn provides WebAuthn (passkey) registration and assertion for
// biometric step-up, plus a short-lived signed token minted on a successful
// assertion. The token — not the assertion itself — is what the recipe runner
// checks for a require_biometric verdict, so the proof is bound to an actor and
// expires quickly.
package webauthn

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type tokenClaims struct {
	Actor string `json:"actor"`
	Exp   int64  `json:"exp"`
}

// mintToken returns a signed token asserting that actor passed a biometric check,
// valid until now+ttl. The signature is HMAC-SHA256 over the claims payload.
func mintToken(secret []byte, actor string, ttl time.Duration, now time.Time) (string, error) {
	payload, err := json.Marshal(tokenClaims{Actor: actor, Exp: now.Add(ttl).Unix()})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + sign(secret, body), nil
}

// verifyToken reports whether token is a valid, unexpired biometric proof for actor.
func verifyToken(secret []byte, actor, token string, now time.Time) bool {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	if !hmac.Equal([]byte(sig), []byte(sign(secret, body))) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return false
	}
	var c tokenClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return false
	}
	if c.Actor != actor {
		return false
	}
	return now.Unix() < c.Exp
}

func sign(secret []byte, body string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// errNoCredentials is returned when an actor has no registered passkey.
var errNoCredentials = fmt.Errorf("webauthn: no registered credentials for actor")
