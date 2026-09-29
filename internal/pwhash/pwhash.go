// Package pwhash implements the scrypt hash format shared with the dashboard:
// scrypt$N$r$p$<salt>$<hash> with base64url (no padding) salt and hash.
package pwhash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	n      = 16384
	r      = 8
	p      = 1
	keyLen = 32
)

var b64 = base64.RawURLEncoding

func Hash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := scrypt.Key([]byte(password), salt, n, r, p, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt$%d$%d$%d$%s$%s", n, r, p, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func Verify(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "scrypt" {
		return false
	}
	pn, err1 := strconv.Atoi(parts[1])
	pr, err2 := strconv.Atoi(parts[2])
	pp, err3 := strconv.Atoi(parts[3])
	salt, err4 := b64.DecodeString(parts[4])
	want, err5 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || len(want) == 0 {
		return false
	}
	got, err := scrypt.Key([]byte(password), salt, pn, pr, pp, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
