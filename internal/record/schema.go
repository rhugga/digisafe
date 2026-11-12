package record

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rhugga/digisafe/internal/totp"
)

// FieldSpec describes one type-specific field.
type FieldSpec struct {
	Name   string
	Label  string
	Secret bool // never echoed when typed; shown only by `show` and `copy`
	Hint   string
	// Check returns "" for a good value, else what's wrong with it.
	Check func(string) string
	// Strict makes a failed Check an error rather than a warning.
	Strict bool
	// Suggest lists the usual values (the form completes them with ctrl+e); others are allowed.
	// A value matching one but for case is saved as the suggestion.
	Suggest []string
}

// Schema describes a record type.
type Schema struct {
	Type            string
	Description     string
	Fields          []FieldSpec
	Address         bool // has an address
	SecretQuestions bool // has secret question/answer pairs
	History         bool // keeps the previous values of its secret fields (HistoryLimit each)
}

// Field returns the named field's spec.
func (s Schema) Field(name string) (FieldSpec, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// FieldNames lists the schema's field names in order.
func (s Schema) FieldNames() []string {
	names := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		names[i] = f.Name
	}
	return names
}

// Schemas holds every record type.
var Schemas = map[string]Schema{
	"bank_card": {
		Type:        "bank_card",
		Description: "a credit, debit, prepaid or gift card",
		Fields: []FieldSpec{
			{Name: "full_name", Label: "name on card"},
			{Name: "card_network", Label: "card network", Hint: "visa, mastercard, amex, discover, ...",
				Suggest: CardNetworks, Check: CheckCardNetwork},
			{Name: "card_no", Label: "card number", Secret: true, Check: CheckCardNumber},
			{Name: "exp_date", Label: "expiry date", Hint: "MMYY", Check: CheckExpDate},
			{Name: "cvv", Label: "CVV", Secret: true, Hint: "3 or 4 digits", Check: CheckCVV},
			{Name: "pin", Label: "PIN", Secret: true, Hint: "4 to 12 digits", Check: CheckPIN},
			{Name: "zip_code", Label: "billing zip code"},
			{Name: "bank_name", Label: "bank name"},
			{Name: "bank_phone", Label: "bank phone"},
		},
	},
	"bank_account": {
		Type:        "bank_account",
		Description: "a bank account",
		Fields: []FieldSpec{
			{Name: "account_no", Label: "account number", Secret: true},
			{Name: "account_type", Label: "account type", Hint: "e.g. checking, savings"},
			{Name: "account_routing_no", Label: "routing number"},
			{Name: "bank_name", Label: "bank name"},
			{Name: "bank_phone", Label: "bank phone"},
			{Name: "bank_url", Label: "bank website"},
		},
		Address:         true,
		SecretQuestions: true,
	},
	"login": {
		Type:        "login",
		Description: "a website or service login",
		Fields: []FieldSpec{
			{Name: "url", Label: "url"},
			{Name: "username", Label: "username"},
			{Name: "password", Label: "password", Secret: true},
			{Name: "email_address", Label: "email address"},
			{Name: "pin", Label: "PIN", Secret: true},
			{Name: "totp_secret", Label: "TOTP secret", Secret: true, Hint: "base32 seed or otpauth:// URI",
				Check: CheckTOTP, Strict: true},
			{Name: "token", Label: "token", Secret: true, Hint: "an API or access token"},
		},
		SecretQuestions: true,
		History:         true,
	},
	"note": {
		Type:        "note",
		Description: "free-form secret text",
		Fields: []FieldSpec{
			{Name: "content", Label: "content", Secret: true},
		},
	},
}

// renamedTypes maps old type names to their current ones, so safes and files written before a
// rename still read: credit_card became bank_card when it grew to cover debit and gift cards.
var renamedTypes = map[string]string{"credit_card": "bank_card"}

// renamedFields maps a type's old field names to their current ones: login's "login" became
// "username" (the type and the field had the same name).
var renamedFields = map[string]map[string]string{"login": {"login": "username"}}

// CanonicalField returns the current name of a type's field, mapping old names to new ones.
func CanonicalField(typ, name string) string {
	if n, ok := renamedFields[CanonicalType(typ)][name]; ok {
		return n
	}
	return name
}

// CanonicalType returns the current name for a record type, mapping old names to new ones.
// Unknown names are returned as they are.
func CanonicalType(t string) string {
	if n, ok := renamedTypes[t]; ok {
		return n
	}
	return t
}

// TypeNames lists the record types, sorted.
func TypeNames() []string {
	names := make([]string, 0, len(Schemas))
	for n := range Schemas {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var (
	expDateRe = regexp.MustCompile(`^(0[1-9]|1[0-2])[0-9]{2}$`)
	cvvRe     = regexp.MustCompile(`^[0-9]{3,4}$`)
	pinRe     = regexp.MustCompile(`^[0-9]{4,12}$`)
)

// CardNetworks are the usual card networks (card_network).
var CardNetworks = []string{"visa", "mastercard", "amex", "discover", "jcb", "unionpay", "diners", "maestro"}

// CheckCardNetwork warns about a network that isn't one of CardNetworks (it may still be right:
// a store's own gift card, say).
func CheckCardNetwork(v string) string {
	for _, n := range CardNetworks {
		if strings.EqualFold(v, n) {
			return ""
		}
	}
	return "not a network digisafe knows (" + strings.Join(CardNetworks, ", ") + ")"
}

// CheckPIN checks a card PIN is 4 to 12 digits.
func CheckPIN(v string) string {
	if !pinRe.MatchString(v) {
		return "want 4 to 12 digits"
	}
	return ""
}

// CheckCardNumber checks a card number's digits and Luhn checksum. Spaces and dashes are allowed.
func CheckCardNumber(v string) string {
	digits := strings.NewReplacer(" ", "", "-", "").Replace(v)
	if len(digits) < 12 || len(digits) > 19 {
		return fmt.Sprintf("a card number has 12 to 19 digits, this has %d characters", len(digits))
	}
	sum := 0
	for i := len(digits) - 1; i >= 0; i-- {
		c := digits[i]
		if c < '0' || c > '9' {
			return "a card number has only digits (spaces and dashes are allowed)"
		}
		d := int(c - '0')
		if (len(digits)-1-i)%2 == 1 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	if sum%10 != 0 {
		return "fails the Luhn check: likely a typo"
	}
	return ""
}

// CheckExpDate checks an expiry date is MMYY.
func CheckExpDate(v string) string {
	if !expDateRe.MatchString(v) {
		return "want MMYY, e.g. 0428"
	}
	return ""
}

// CheckCVV checks a card security code is 3 or 4 digits.
func CheckCVV(v string) string {
	if !cvvRe.MatchString(v) {
		return "want 3 or 4 digits"
	}
	return ""
}

// CheckTOTP checks a TOTP secret parses.
func CheckTOTP(v string) string {
	if _, err := totp.Parse(v); err != nil {
		return err.Error()
	}
	return ""
}
