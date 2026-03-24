package stats

import "net/url"

func parseFailureURL(failureURL string) url.Values {
	if failureURL == "" {
		return nil
	}
	u, err := url.Parse(failureURL)
	if err != nil {
		return nil
	}
	return u.Query()
}

// ExtractOrgID parses a failure URL and returns the "org_id"
// query parameter value. Returns an empty string when the URL is
// empty, unparseable, or does not contain the parameter.
func ExtractOrgID(failureURL string) string {
	return parseFailureURL(failureURL).Get("org_id")
}

// ExtractTargetEnv parses a failure URL and returns the "target_env"
// query parameter value. Returns an empty string when the URL is
// empty, unparseable, or does not contain the parameter.
func ExtractTargetEnv(failureURL string) string {
	return parseFailureURL(failureURL).Get("target_env")
}
