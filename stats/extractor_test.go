package stats

import "testing"

func TestExtractOrgID(t *testing.T) {
	tests := []struct {
		name       string
		failureURL string
		want       string
	}{
		{
			name:       "normal URL with org_id",
			failureURL: "http://example.com/callback?org_id=191&target_env=staging",
			want:       "191",
		},
		{
			name:       "org_id only param",
			failureURL: "http://example.com/callback?org_id=999",
			want:       "999",
		},
		{
			name:       "empty string",
			failureURL: "",
			want:       "",
		},
		{
			name:       "URL without org_id param",
			failureURL: "http://example.com/callback?target_env=staging",
			want:       "",
		},
		{
			name:       "URL with no query params",
			failureURL: "http://example.com/callback",
			want:       "",
		},
		{
			name:       "invalid URL",
			failureURL: "://bad-url",
			want:       "",
		},
		{
			name:       "org_id with empty value",
			failureURL: "http://example.com/callback?org_id=&target_env=staging",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractOrgID(tt.failureURL)
			if got != tt.want {
				t.Errorf("ExtractOrgID(%q) = %q, want %q", tt.failureURL, got, tt.want)
			}
		})
	}
}

func TestExtractTargetEnv(t *testing.T) {
	tests := []struct {
		name       string
		failureURL string
		want       string
	}{
		{
			name:       "normal URL with target_env",
			failureURL: "http://example.com/callback?org_id=191&target_env=staging",
			want:       "staging",
		},
		{
			name:       "empty string",
			failureURL: "",
			want:       "",
		},
		{
			name:       "URL without target_env",
			failureURL: "http://example.com/callback?org_id=191",
			want:       "",
		},
		{
			name:       "target_env with empty value",
			failureURL: "http://example.com/callback?target_env=&org_id=1",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTargetEnv(tt.failureURL)
			if got != tt.want {
				t.Errorf("ExtractTargetEnv(%q) = %q, want %q", tt.failureURL, got, tt.want)
			}
		})
	}
}
