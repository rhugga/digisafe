// age-plugin-dstest is a fake age plugin for digisafe's tests, standing in for a hardware key
// (age-plugin-se, age-plugin-yubikey). Its identity data is an ordinary age X25519 secret key, so
// it encrypts and decrypts like one; data prefixed "fail:" encrypts normally but never decrypts,
// like a hardware key that's missing or refuses (to test the passphrase fallback). The tests build
// it and put it on PATH.
package main

import (
	"errors"
	"os"
	"strings"

	"filippo.io/age"
	"filippo.io/age/plugin"
)

func parse(data []byte) (key *age.X25519Identity, fail bool, err error) {
	s, fail := strings.CutPrefix(string(data), "fail:")
	key, err = age.ParseX25519Identity(s)
	return key, fail, err
}

type failing struct{}

func (failing) Unwrap([]*age.Stanza) ([]byte, error) {
	return nil, errors.New("the dstest key refused (as a missing or locked hardware key would)")
}

func main() {
	p, err := plugin.New("dstest")
	if err != nil {
		panic(err)
	}
	p.HandleIdentityAsRecipient(func(data []byte) (age.Recipient, error) {
		key, _, err := parse(data)
		if err != nil {
			return nil, err
		}
		return key.Recipient(), nil
	})
	p.HandleIdentity(func(data []byte) (age.Identity, error) {
		key, fail, err := parse(data)
		if err != nil {
			return nil, err
		}
		if fail {
			return failing{}, nil
		}
		return key, nil
	})
	os.Exit(p.Main())
}
