// Package auth: passwords, static tokens and principal resolution (05).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	aMem, aTime, aPar, aKey = 64 * 1024, 3, 2, 32
)

// HashPassword returns an argon2id PHC string (AUTH-015).
func HashPassword(pw string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	k := argon2.IDKey([]byte(pw), salt, aTime, aMem, aPar, aKey)
	e := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", aMem, aTime, aPar, e.EncodeToString(salt), e.EncodeToString(k))
}

// VerifyPassword checks pw against a PHC string; any malformed hash simply fails.
func VerifyPassword(phc, pw string) bool {
	p := strings.Split(phc, "$") // "", argon2id, v=19, m=,t=,p=, salt, key
	if len(p) != 6 || p[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var par uint8
	if _, err := fmt.Sscanf(p[3], "m=%d,t=%d,p=%d", &m, &t, &par); err != nil || m == 0 || t == 0 || par == 0 || m > 1<<20 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(p[4])
	want, e2 := base64.RawStdEncoding.DecodeString(p[5])
	if e1 != nil || e2 != nil || len(want) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(argon2.IDKey([]byte(pw), salt, t, m, par, uint32(len(want))), want) == 1
}
