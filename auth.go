package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type sessionPayload struct {
	User string `json:"u"`
	Exp  int64  `json:"exp"`
}

func issueSessionToken(key []byte, username string, ttl time.Duration) (string, error) {
	payload, err := json.Marshal(sessionPayload{
		User: username,
		Exp:  time.Now().Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := hmacSHA256(key, []byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func validateSessionToken(key []byte, token string) (string, bool) {
	body, sigB64, ok := strings.Cut(token, ".")
	if !ok {
		return "", false
	}
	digest := hmacSHA256(key, []byte(body))
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || !hmac.Equal(sig, digest) {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", false
	}
	var p sessionPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.User == "" {
		return "", false
	}
	if p.Exp <= time.Now().Unix() {
		return "", false
	}
	return p.User, true
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
