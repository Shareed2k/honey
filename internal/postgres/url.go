package postgres

import (
	"net/url"
	"strings"
)

// ParseURL securely parses a Postgres connection string,
// properly encoding unencoded passwords containing special characters like '#'.
func ParseURL(rawURL string) (*url.URL, error) {
	schemeEnd := strings.Index(rawURL, "://")
	if schemeEnd == -1 {
		return url.Parse(rawURL)
	}

	atIndex := strings.LastIndex(rawURL, "@")
	if atIndex > schemeEnd+3 {
		userInfo := rawURL[schemeEnd+3 : atIndex]
		parts := strings.SplitN(userInfo, ":", 2)
		var newUserInfo string
		if len(parts) == 2 {
			newUserInfo = url.QueryEscape(parts[0]) + ":" + url.QueryEscape(parts[1])
		} else {
			newUserInfo = url.QueryEscape(parts[0])
		}
		rawURL = rawURL[:schemeEnd+3] + newUserInfo + rawURL[atIndex:]
	}

	return url.Parse(rawURL)
}
