package stats

import "net/url"

// ExtractSubscribeID parses a failure URL and returns the "sub_id"
// query parameter value. Returns an empty string when the URL is
// empty, unparseable, or does not contain the parameter.
func ExtractSubscribeID(failureURL string) string {
	if failureURL == "" {
		return ""
	}
	u, err := url.Parse(failureURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("sub_id")
}
