package target

import (
	"strings"
	"unicode/utf8"
)

// Punycode parameters, RFC 3492 section 5.
const (
	pcBase        = 36
	pcTMin        = 1
	pcTMax        = 26
	pcSkew        = 38
	pcDamp        = 700
	pcInitialBias = 72
	pcInitialN    = 128
	pcMaxInt      = 1<<31 - 1
)

func pcAdapt(delta, numPoints int, first bool) int {
	if first {
		delta /= pcDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := 0
	for delta > ((pcBase-pcTMin)*pcTMax)/2 {
		delta /= pcBase - pcTMin
		k += pcBase
	}
	return k + (pcBase-pcTMin+1)*delta/(delta+pcSkew)
}

func pcDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c-'0') + 26
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= 'a' && c <= 'z':
		return int(c - 'a')
	}
	return -1
}

// punyDecode decodes a Punycode string (without the "xn--" prefix) as in
// RFC 3492 section 6.2. ok is false for malformed input.
func punyDecode(s string) (string, bool) {
	var out []rune
	in := s
	if b := strings.LastIndexByte(s, '-'); b >= 0 {
		for i := 0; i < b; i++ {
			if s[i] >= 0x80 {
				return "", false
			}
			out = append(out, rune(s[i]))
		}
		in = s[b+1:]
	}
	n, i, bias := pcInitialN, 0, pcInitialBias
	for pos := 0; pos < len(in); {
		oldi, w := i, 1
		for k := pcBase; ; k += pcBase {
			if pos >= len(in) {
				return "", false
			}
			digit := pcDigit(in[pos])
			pos++
			if digit < 0 || digit > (pcMaxInt-i)/w {
				return "", false
			}
			i += digit * w
			t := k - bias
			if t < pcTMin {
				t = pcTMin
			} else if t > pcTMax {
				t = pcTMax
			}
			if digit < t {
				break
			}
			if w > pcMaxInt/(pcBase-t) {
				return "", false
			}
			w *= pcBase - t
		}
		bias = pcAdapt(i-oldi, len(out)+1, oldi == 0)
		if i/(len(out)+1) > pcMaxInt-n {
			return "", false
		}
		n += i / (len(out) + 1)
		i %= len(out) + 1
		if n > utf8.MaxRune || (n >= 0xD800 && n <= 0xDFFF) {
			return "", false
		}
		out = append(out, 0)
		copy(out[i+1:], out[i:])
		out[i] = rune(n)
		i++
	}
	return string(out), true
}

// toUnicode decodes every "xn--" label of a host name to Unicode. Labels
// that are not valid Punycode are left as they are.
func toUnicode(host string) string {
	if !strings.Contains(host, "xn--") {
		return host
	}
	labels := strings.Split(host, ".")
	for i, l := range labels {
		if len(l) > 4 && strings.EqualFold(l[:4], "xn--") {
			if u, ok := punyDecode(l[4:]); ok && u != "" {
				labels[i] = strings.ToLower(u)
			}
		}
	}
	return strings.Join(labels, ".")
}
