package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

const sensitiveHashPrefix = "hmac-sha256$"

func getSensitivePepper() string {
	if AppConfig == nil {
		return ""
	}

	return strings.TrimSpace(AppConfig.SecurityPepper)
}

func hashSensitiveToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	pepper := getSensitivePepper()
	if pepper == "" {
		return ""
	}

	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(raw))
	sum := mac.Sum(nil)

	return sensitiveHashPrefix + base64.RawURLEncoding.EncodeToString(sum)
}

func isHashedSensitiveToken(stored string) bool {
	return strings.HasPrefix(stored, sensitiveHashPrefix)
}

func verifySensitiveToken(raw string, stored string) bool {
	raw = strings.TrimSpace(raw)
	stored = strings.TrimSpace(stored)

	if raw == "" || stored == "" {
		return false
	}

	if isHashedSensitiveToken(stored) {
		expected := hashSensitiveToken(raw)
		if expected == "" {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(expected), []byte(stored)) == 1
	}

	// 兼容旧明文。正常情况下，启动迁移后不会再走到这里。
	return subtle.ConstantTimeCompare([]byte(raw), []byte(stored)) == 1
}

func maskSecret(raw string) string {
	raw = strings.TrimSpace(raw)
	runes := []rune(raw)

	if len(runes) <= 4 {
		return "****"
	}

	if len(runes) <= 10 {
		return string(runes[:2]) + "****" + string(runes[len(runes)-2:])
	}

	return string(runes[:4]) + "****" + string(runes[len(runes)-4:])
}
