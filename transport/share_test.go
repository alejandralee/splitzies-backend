package transport

import "testing"

func TestShareURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		token   string
		want    string
	}{
		{
			name:  "falls back to the production app when unset",
			token: "abc123",
			want:  defaultAppBaseURL + "/join/abc123",
		},
		{
			name:    "uses APP_BASE_URL when set",
			baseURL: "https://staging.example.com",
			token:   "abc123",
			want:    "https://staging.example.com/join/abc123",
		},
		{
			name:    "trailing slash does not double up",
			baseURL: "https://staging.example.com/",
			token:   "abc123",
			want:    "https://staging.example.com/join/abc123",
		},
		{
			name:    "surrounding whitespace is trimmed",
			baseURL: "  https://staging.example.com  ",
			token:   "abc123",
			want:    "https://staging.example.com/join/abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_BASE_URL", tt.baseURL)
			if got := shareURL(tt.token); got != tt.want {
				t.Errorf("shareURL(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}
