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
		"https://xn--e1afmkfd.xn--p1ai/":      "xn--e1afmkfd.xn--p1ai",
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
