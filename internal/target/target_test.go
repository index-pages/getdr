package target

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"example.com":                         "example.com",
		"  Example.COM  ":                     "example.com",
		"www.example.com":                     "example.com",
		"https://www.Example.com/path?q=1#x":  "example.com",
		"http://user:pass@example.com:8080/a": "example.com",
		"example.com/some/page":               "example.com",
		"example.com:443":                     "example.com",
		"example.com.":                        "example.com",
		"//cdn.example.com/x.js":              "cdn.example.com",
		"sub.www.example.com":                 "sub.www.example.com",
		"wwwexample.com":                      "wwwexample.com",
		"HTTPS://WWW.EXAMPLE.CO.UK":           "example.co.uk",
		"":                                    "",
		"   ":                                 "",
		"https://":                            "",
		"ftp://files.example.org/pub":         "files.example.org",
		"https://example.com?utm_source=x":    "example.com",
		"https://xn--e1afmkfd.xn--p1ai/":      "пример.рф",
		"https://[2001:db8::1]:8080/":         "2001:db8::1",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrepare(t *testing.T) {
	in := []string{"https://www.a.com/x", "A.com", "b.com", "", "b.com/", "c.com"}
	got := Prepare(in, false)
	want := []string{"a.com", "b.com", "c.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Prepare = %v, want %v", got, want)
	}
	raw := Prepare([]string{" https://www.a.com/x ", "https://www.a.com/x", "A.com"}, true)
	wantRaw := []string{"https://www.a.com/x", "A.com"}
	if !reflect.DeepEqual(raw, wantRaw) {
		t.Fatalf("Prepare raw = %v, want %v", raw, wantRaw)
	}
}

func TestReadList(t *testing.T) {
	in := "a.com\n\n# comment\n  b.com  \r\n#c.com\nd.com"
	got, err := ReadList(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.com", "b.com", "d.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadList = %v, want %v", got, want)
	}
}

func TestPunyDecodeRFC3492(t *testing.T) {
	// Sample strings from RFC 3492 section 7.1 plus the Russian test domain.
	cases := map[string]string{
		"ihqwcrb4cv8a8dqg056pqjye":                 "他们为什么不说中文",
		"Proprostnemluvesky-uyb24dma41a":           "Pročprostěnemluvíčesky",
		"3B-ww4c5e180e575a65lsy2b":                 "3年B組金八先生",
		"-with-SUPER-MONKEYS-pc58ag80a8qai00g7n9n": "安室奈美恵-with-SUPER-MONKEYS",
		"-> $1.00 <--":                             "-> $1.00 <-",
		"n8jok5ay5dzabd5bym9f0cm5685rrjetr6pdxa":   "なぜみんな日本語を話してくれないのか",
		"e1afmkfd":                                 "пример",
		"p1ai":                                     "рф",
	}
	for in, want := range cases {
		got, ok := punyDecode(in)
		if !ok || got != want {
			t.Errorf("punyDecode(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"ü-abc", "99999999999", "abc-!"} {
		if got, ok := punyDecode(bad); ok {
			t.Errorf("punyDecode(%q) = %q, must fail", bad, got)
		}
	}
}

func TestNormalizeIDN(t *testing.T) {
	cases := map[string]string{
		"xn--e1afmkfd.xn--p1ai":              "пример.рф",
		"ПРИМЕР.РФ":                          "пример.рф",
		"https://www.XN--E1AFMKFD.xn--p1ai/": "пример.рф",
		"xn--zz.example.com":                 "xn--zz.example.com",
		"xn--.com":                           "xn--.com",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	got := Prepare([]string{"xn--e1afmkfd.xn--p1ai", "ПРИМЕР.РФ"}, false)
	if len(got) != 1 || got[0] != "пример.рф" {
		t.Errorf("IDN dedup: %q", got)
	}
	if raw := Prepare([]string{"xn--e1afmkfd.xn--p1ai"}, true); raw[0] != "xn--e1afmkfd.xn--p1ai" {
		t.Errorf("raw must not decode: %q", raw)
	}
}

func TestValid(t *testing.T) {
	for _, s := range []string{"a.com", "localhost", "a..com", "пример.рф", "https://a.com/x"} {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false", s)
		}
	}
	for _, s := range []string{"", "   ", "exa mple.com", "a.com\t", "a\x00.com", "https://", "https://a.com/x y"} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true", s)
		}
	}
	got := Prepare([]string{"https://", "a.com", " "}, false)
	if len(got) != 2 || got[0] != "https://" || got[1] != "a.com" {
		t.Errorf("Prepare keeps unnormalizable input for reporting: %q", got)
	}
}
