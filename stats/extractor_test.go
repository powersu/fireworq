package stats

import "testing"

func TestExtractSubscribeID(t *testing.T) {
	tests := []struct {
		name       string
		failureURL string
		want       string
	}{
		{
			name:       "normal URL with sub_id",
			failureURL: "http://example.com/callback?sub_id=1444&org_id=191",
			want:       "1444",
		},
		{
			name:       "sub_id only param",
			failureURL: "http://example.com/callback?sub_id=999",
			want:       "999",
		},
		{
			name:       "empty string",
			failureURL: "",
			want:       "",
		},
		{
			name:       "URL without sub_id param",
			failureURL: "http://example.com/callback?org_id=191",
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
			name:       "sub_id with empty value",
			failureURL: "http://example.com/callback?sub_id=&org_id=1",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSubscribeID(tt.failureURL)
			if got != tt.want {
				t.Errorf("ExtractSubscribeID(%q) = %q, want %q", tt.failureURL, got, tt.want)
			}
		})
	}
}
