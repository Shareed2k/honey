package hosts

import "regexp"

// NameMatches applies NameSubstring, NameRegex, or accepts all if both empty.
func NameMatches(name string, q Query) (bool, error) {
	if q.NameRegex != "" {
		re, err := regexp.Compile(q.NameRegex)
		if err != nil {
			return false, err
		}
		return re.MatchString(name), nil
	}
	if q.NameSubstring != "" {
		return containsFold(name, q.NameSubstring), nil
	}
	return true, nil
}

func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	ls := toLowerASCII(s)
	lsub := toLowerASCII(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return true
		}
	}
	return false
}

func toLowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
