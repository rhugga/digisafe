// Package totp generates RFC 6238 time-based one-time passwords.
package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Key is a TOTP configuration: the shared secret and the code parameters.
type Key struct {
	Secret    []byte
	Digits    int
	Period    time.Duration
	Algorithm string // SHA1, SHA256 or SHA512
}

// Parse reads a base32 secret (spaces and lowercase allowed, padding optional) with the usual
// defaults (6 digits, 30s, SHA1), or an otpauth://totp/ URI with its own parameters.
func Parse(s string) (*Key, error) {
	s = strings.TrimSpace(s)
	k := &Key{Digits: 6, Period: 30 * time.Second, Algorithm: "SHA1"}
	secret := s
	if strings.HasPrefix(strings.ToLower(s), "otpauth://") {
		u, err := url.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("not a valid otpauth URI: %w", err)
		}
		if !strings.EqualFold(u.Host, "totp") {
			return nil, fmt.Errorf("otpauth URI is %q, only totp is supported", u.Host)
		}
		q := u.Query()
		secret = q.Get("secret")
		if d := q.Get("digits"); d != "" {
			n, err := strconv.Atoi(d)
			if err != nil || n < 6 || n > 10 {
				return nil, fmt.Errorf("otpauth digits must be 6 to 10, got %q", d)
			}
			k.Digits = n
		}
		if p := q.Get("period"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("otpauth period must be a positive number of seconds, got %q", p)
			}
			k.Period = time.Duration(n) * time.Second
		}
		if a := q.Get("algorithm"); a != "" {
			k.Algorithm = strings.ToUpper(a)
		}
	}
	if _, err := newHash(k.Algorithm); err != nil {
		return nil, err
	}
	secret = strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(secret))
	if secret == "" {
		return nil, fmt.Errorf("the TOTP secret is empty")
	}
	b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return nil, fmt.Errorf("the TOTP secret is not valid base32")
	}
	k.Secret = b
	return k, nil
}

func newHash(alg string) (func() hash.Hash, error) {
	switch alg {
	case "SHA1":
		return sha1.New, nil
	case "SHA256":
		return sha256.New, nil
	case "SHA512":
		return sha512.New, nil
	}
	return nil, fmt.Errorf("unsupported TOTP algorithm %q (want SHA1, SHA256 or SHA512)", alg)
}

// Code returns the code for time t, and how long it stays valid.
func (k *Key) Code(t time.Time) (code string, remaining time.Duration) {
	h, _ := newHash(k.Algorithm) // checked by Parse
	period := int64(k.Period / time.Second)
	counter := t.Unix() / period
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac := hmac.New(h, k.Secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := uint64(binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff)
	mod := uint64(1)
	for range k.Digits {
		mod *= 10
	}
	code = fmt.Sprintf("%0*d", k.Digits, bin%mod)
	next := time.Unix((counter+1)*period, 0)
	return code, next.Sub(t)
}
