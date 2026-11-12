package totp

import (
	"encoding/base32"
	"testing"
	"time"
)

// RFC 6238 appendix B: the test secrets are ASCII, 8-digit codes.
func TestRFC6238Vectors(t *testing.T) {
	secrets := map[string]string{
		"SHA1":   "12345678901234567890",
		"SHA256": "12345678901234567890123456789012",
		"SHA512": "1234567890123456789012345678901234567890123456789012345678901234",
	}
	vectors := []struct {
		unix int64
		alg  string
		code string
	}{
		{59, "SHA1", "94287082"}, {59, "SHA256", "46119246"}, {59, "SHA512", "90693936"},
		{1111111109, "SHA1", "07081804"}, {1111111109, "SHA256", "68084774"}, {1111111109, "SHA512", "25091201"},
		{1234567890, "SHA1", "89005924"}, {1234567890, "SHA256", "91819424"}, {1234567890, "SHA512", "93441116"},
		{20000000000, "SHA1", "65353130"}, {20000000000, "SHA256", "77737706"}, {20000000000, "SHA512", "47863826"},
	}
	for _, v := range vectors {
		k := &Key{Secret: []byte(secrets[v.alg]), Digits: 8, Period: 30 * time.Second, Algorithm: v.alg}
		if got, _ := k.Code(time.Unix(v.unix, 0)); got != v.code {
			t.Errorf("%s at %d = %s, want %s", v.alg, v.unix, got, v.code)
		}
	}
}

func TestParse(t *testing.T) {
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	// lowercase, spaced and padded forms of the same secret
	for _, s := range []string{b32, "  gezd gnbv gy3t qojq gezd gnbv gy3t qojq  ", b32 + "===="} {
		k, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if string(k.Secret) != "12345678901234567890" || k.Digits != 6 || k.Period != 30*time.Second || k.Algorithm != "SHA1" {
			t.Errorf("Parse(%q) = %+v", s, k)
		}
		if code, left := k.Code(time.Unix(59, 0)); code != "287082" || left != time.Second {
			t.Errorf("code = %s, %v; want 287082, 1s", code, left)
		}
	}

	k, err := Parse("otpauth://totp/Example:me@example.com?secret=" + b32 + "&issuer=Example&digits=8&period=60&algorithm=sha256")
	if err != nil {
		t.Fatal(err)
	}
	if k.Digits != 8 || k.Period != time.Minute || k.Algorithm != "SHA256" {
		t.Errorf("otpauth parse = %+v", k)
	}

	for _, bad := range []string{"", "not base32 !!", "otpauth://hotp/x?secret=" + b32,
		"otpauth://totp/x?secret=" + b32 + "&algorithm=md5", "otpauth://totp/x?secret=" + b32 + "&digits=4"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", bad)
		}
	}
}
