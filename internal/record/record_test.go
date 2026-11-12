package record

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestChecks(t *testing.T) {
	for _, v := range []string{"4111111111111111", "4111 1111 1111 1111", "4111-1111-1111-1111", "378282246310005"} {
		if msg := CheckCardNumber(v); msg != "" {
			t.Errorf("CheckCardNumber(%q) = %q, want ok", v, msg)
		}
	}
	for _, v := range []string{"4111111111111112", "1234", "4111x11111111111"} {
		if CheckCardNumber(v) == "" {
			t.Errorf("CheckCardNumber(%q) ok, want a problem", v)
		}
	}
	for v, ok := range map[string]bool{"0428": true, "1299": true, "1328": false, "0028": false, "428": false, "04/28": false} {
		if (CheckExpDate(v) == "") != ok {
			t.Errorf("CheckExpDate(%q) ok=%v, want %v", v, !ok, ok)
		}
	}
	for v, ok := range map[string]bool{"123": true, "1234": true, "12": false, "12345": false, "12a": false} {
		if (CheckCVV(v) == "") != ok {
			t.Errorf("CheckCVV(%q) ok=%v, want %v", v, !ok, ok)
		}
	}
}

func newRecord(t *testing.T, typ string) *Record {
	t.Helper()
	r, err := New(typ, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.Name = "test"
	return r
}

func TestValidate(t *testing.T) {
	if _, err := New("bogus", time.Now()); err == nil {
		t.Error("New(bogus) succeeded")
	}

	r := newRecord(t, "credit_card")
	r.Fields["card_no"] = "4111111111111112"
	r.Fields["exp_date"] = "13/28"
	warnings, err := r.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %q, want card_no and exp_date", warnings)
	}

	r = newRecord(t, "credit_card")
	r.Fields["password"] = "x"
	if _, err := r.Validate(); err == nil || !strings.Contains(err.Error(), "no field password") {
		t.Errorf("unknown field: err = %v", err)
	}

	r = newRecord(t, "login")
	r.Fields["totp_secret"] = "not base32 !!"
	if _, err := r.Validate(); err == nil {
		t.Error("a bad totp_secret should be an error")
	}

	r = newRecord(t, "login")
	r.Address = &Address{City: "x"}
	if _, err := r.Validate(); err == nil {
		t.Error("an address on a login should be an error")
	}

	r = newRecord(t, "note")
	r.Name = "  "
	if _, err := r.Validate(); err == nil {
		t.Error("an empty name should be an error")
	}

	r = newRecord(t, "note")
	r.ID = "not-a-uuid"
	if _, err := r.Validate(); err == nil {
		t.Error("a bad id should be an error")
	}
}

func TestNormalize(t *testing.T) {
	r := newRecord(t, "bank_account")
	r.Name = "  bank  "
	r.Fields["account_no"] = "  "
	r.Tags = []string{" a ", "a", "", "b"}
	r.Address = &Address{}
	r.Custom = map[string]string{"k": "", "": "v", "keep": "x"}
	r.Normalize()
	if r.Name != "bank" || len(r.Fields) != 0 || r.Address != nil || strings.Join(r.Tags, ",") != "a,b" ||
		len(r.Custom) != 1 || r.Custom["keep"] != "x" {
		t.Errorf("Normalize = %+v", r)
	}
}

func TestMatchesAndGet(t *testing.T) {
	r := newRecord(t, "login")
	r.Name, r.Description, r.Tags = "GitHub", "work account", []string{"Dev"}
	r.Fields["password"] = "hunter2"
	r.Custom = map[string]string{"recovery": "abc"}
	for _, term := range []string{"git", "WORK", "dev", ""} {
		if !r.Matches(term) {
			t.Errorf("Matches(%q) = false", term)
		}
	}
	if r.Matches("hunter2") {
		t.Error("search must not look at secret fields")
	}
	if v, ok := r.Get("password"); !ok || v != "hunter2" {
		t.Errorf("Get(password) = %q, %v", v, ok)
	}
	if v, ok := r.Get("custom.recovery"); !ok || v != "abc" {
		t.Errorf("Get(custom.recovery) = %q, %v", v, ok)
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("Get(nope) ok")
	}
}

func TestGetCommonAndNestedFields(t *testing.T) {
	r := newRecord(t, "bank_account")
	r.Description, r.Notes = "desc", "notes"
	r.Address = &Address{City: "Springfield"}
	for field, want := range map[string]string{"name": "test", "description": "desc", "notes": "notes", "address.city": "Springfield"} {
		if v, ok := r.Get(field); !ok || v != want {
			t.Errorf("Get(%s) = %q, %v", field, v, ok)
		}
	}
	for _, field := range []string{"address.state", "address.nope", "custom.none"} {
		if _, ok := r.Get(field); ok {
			t.Errorf("Get(%s) ok", field)
		}
	}
	if r.Schema().Type != "bank_account" {
		t.Error("Schema")
	}
	if CheckTOTP("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ") != "" || CheckTOTP("!!") == "" {
		t.Error("CheckTOTP")
	}
}

func TestCardChecks(t *testing.T) {
	for v, ok := range map[string]bool{"1234": true, "123456789012": true, "123": false, "1234567890123": false, "12a4": false} {
		if (CheckPIN(v) == "") != ok {
			t.Errorf("CheckPIN(%q) ok=%v, want %v", v, !ok, ok)
		}
	}
	for v, ok := range map[string]bool{"visa": true, "AMEX": true, "Mastercard": true, "store card": false} {
		if (CheckCardNetwork(v) == "") != ok {
			t.Errorf("CheckCardNetwork(%q) ok=%v, want %v", v, !ok, ok)
		}
	}
}

// credit_card was renamed bank_card: the old name still creates, filters and normalises to the
// new one.
func TestRenamedType(t *testing.T) {
	if CanonicalType("credit_card") != "bank_card" || CanonicalType("login") != "login" || CanonicalType("x") != "x" {
		t.Error("CanonicalType")
	}
	if _, ok := Schemas["credit_card"]; ok {
		t.Error("credit_card is still a schema")
	}
	r := newRecord(t, "credit_card")
	if r.Type != "bank_card" {
		t.Errorf("New(credit_card).Type = %q", r.Type)
	}
	r.Type = "credit_card"
	r.Fields["card_network"] = " VISA "
	r.Normalize()
	if r.Type != "bank_card" || r.Fields["card_network"] != "visa" {
		t.Errorf("after Normalize: %q %q", r.Type, r.Fields["card_network"])
	}
	r.Fields["card_network"] = "Store Card" // unknown networks are kept as typed, with a warning
	r.Normalize()
	if w, err := r.Validate(); err != nil || r.Fields["card_network"] != "Store Card" || len(w) != 1 {
		t.Errorf("unknown network: %q, %v, %v", r.Fields["card_network"], w, err)
	}
}

func TestNewFields(t *testing.T) {
	b := Schemas["bank_account"]
	if !b.SecretQuestions {
		t.Error("bank accounts have no secret questions")
	}
	for typ, name := range map[string]string{"login": "token", "bank_card": "pin"} {
		if f, ok := Schemas[typ].Field(name); !ok || !f.Secret {
			t.Errorf("%s.%s: %+v, %v", typ, name, f, ok)
		}
	}
	if f, _ := Schemas["bank_card"].Field("card_network"); f.Secret || len(f.Suggest) == 0 {
		t.Errorf("card_network: %+v", f)
	}
}

func TestNoteChanges(t *testing.T) {
	t0 := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	r := newRecord(t, "login")
	r.Fields["password"], r.Fields["url"] = "p0", "https://a"
	for i := 1; i <= 7; i++ {
		before := map[string]string{"password": r.Fields["password"], "url": r.Fields["url"]}
		r.Fields["password"], r.Fields["url"] = fmt.Sprintf("p%d", i), fmt.Sprintf("https://%d", i)
		if kept := r.NoteChanges(before, t0.Add(time.Duration(i)*time.Hour)); len(kept) != 1 || kept[0] != "password" {
			t.Fatalf("change %d kept %v", i, kept) // url isn't secret: no history
		}
	}
	h := r.History["password"]
	if len(h) != HistoryLimit || h[0].Value != "p6" || h[4].Value != "p2" || !h[0].ReplacedAt.Equal(t0.Add(7*time.Hour)) {
		t.Errorf("history = %+v", h)
	}
	if v, ok := r.Get("history.password.1"); !ok || v != "p6" {
		t.Errorf("Get history.password.1 = %q, %v", v, ok)
	}
	for _, bad := range []string{"history.password.0", "history.password.6", "history.password.x", "history.pin.1", "history.password"} {
		if _, ok := r.Get(bad); ok {
			t.Errorf("Get(%q) ok", bad)
		}
	}
	// unchanged, and set for the first time: nothing to keep; cleared: the old value is kept
	if kept := r.NoteChanges(map[string]string{"password": "p7"}, t0); kept != nil {
		t.Errorf("unchanged kept %v", kept)
	}
	r.Fields["pin"] = "1234"
	if kept := r.NoteChanges(map[string]string{"password": "p7"}, t0); kept != nil {
		t.Errorf("a new pin kept %v", kept)
	}
	delete(r.Fields, "pin")
	if kept := r.NoteChanges(map[string]string{"password": "p7", "pin": "1234"}, t0); len(kept) != 1 || r.History["pin"][0].Value != "1234" {
		t.Errorf("clearing the pin kept %v, %+v", kept, r.History["pin"])
	}
	if _, err := r.Validate(); err != nil {
		t.Error(err)
	}
	// types without history keep nothing
	c := newRecord(t, "bank_card")
	c.Fields["pin"] = "2"
	if kept := c.NoteChanges(map[string]string{"pin": "1"}, t0); kept != nil || c.History != nil {
		t.Errorf("bank_card kept %v", kept)
	}
}

func TestHistoryNormalizeAndValidate(t *testing.T) {
	r := newRecord(t, "login")
	var long []PastValue
	for i := 0; i < 8; i++ {
		long = append(long, PastValue{Value: fmt.Sprint(i)})
	}
	r.History = map[string][]PastValue{"password": long, "pin": {{Value: ""}}}
	r.Normalize()
	if len(r.History) != 1 || len(r.History["password"]) != HistoryLimit {
		t.Errorf("after Normalize: %+v", r.History)
	}
	r.History = map[string][]PastValue{"pin": {{Value: ""}}}
	if r.Normalize(); r.History != nil {
		t.Errorf("empty history kept: %+v", r.History)
	}
	for typ, field := range map[string]string{"login": "url", "note": "content"} {
		x := newRecord(t, typ)
		x.History = map[string][]PastValue{field: {{Value: "v"}}}
		if _, err := x.Validate(); err == nil || !strings.Contains(err.Error(), "keep no history for "+field) {
			t.Errorf("%s history for %s: %v", typ, field, err)
		}
	}
}

// login's "login" field was renamed username: old records move the value, and the old name still
// reads.
func TestRenamedField(t *testing.T) {
	r := newRecord(t, "login")
	r.Fields["login"] = "me"
	r.Upgrade()
	if r.Fields["username"] != "me" || r.Fields["login"] != "" {
		t.Errorf("fields = %v", r.Fields)
	}
	if v, ok := r.Get("login"); !ok || v != "me" {
		t.Errorf("Get(login) = %q, %v", v, ok)
	}
	if CanonicalField("login", "login") != "username" || CanonicalField("bank_card", "login") != "login" {
		t.Error("CanonicalField")
	}
	// both set (shouldn't happen): username wins, and the leftover is caught by Validate
	r.Fields["login"] = "other"
	r.Normalize()
	if _, err := r.Validate(); r.Fields["username"] != "me" || err == nil {
		t.Errorf("both: %v, %v", r.Fields, err)
	}
	// other types aren't touched
	n := newRecord(t, "note")
	n.Fields["login"] = "x"
	if n.Upgrade(); n.Fields["login"] != "x" {
		t.Errorf("note fields = %v", n.Fields)
	}
}

func TestPrintableAndOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"plain":               "plain",
		"two\nlines\tok":      "two\nlines\tok",
		"\x1b]0;pwned\x07":    `\x1b]0;pwned\x07`,
		"evil\u202edocx.exe":  "evil\\u202edocx.exe",
		"del\x7f c1\u0085 né": `del\x7f c1\x85 né`,
	} {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
	r := newRecord(t, "note")
	r.Name, r.Description, r.Tags = " a\x1b[31mb\nc ", "x\u202ey", []string{"t\x00ag", " "}
	r.Normalize()
	if r.Name != "a [31mb c" || r.Description != "x y" || len(r.Tags) != 1 || r.Tags[0] != "t ag" {
		t.Errorf("after Normalize: %q %q %q", r.Name, r.Description, r.Tags)
	}
}

func TestMatchesNonSecretFields(t *testing.T) {
	r := newRecord(t, "login")
	r.Name, r.Fields["url"], r.Fields["username"], r.Fields["password"] = "Code", "https://GitHub.com", "octo", "hunter2"
	for term, want := range map[string]bool{"github": true, "OCTO": true, "code": true, "hunter2": false, "": true, "nope": false} {
		if got := r.Matches(term); got != want {
			t.Errorf("Matches(%q) = %v, want %v", term, got, want)
		}
	}
	b := newRecord(t, "bank_account")
	b.Address = &Address{City: "Springfield"}
	b.Fields["account_no"] = "12345"
	if !b.Matches("springfield") || b.Matches("12345") {
		t.Error("bank account: the address should match, the account number shouldn't")
	}
}
