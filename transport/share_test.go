package transport

import (
	"errors"
	"testing"
)

func TestShareURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		token   string
		want    string
		wantErr error
	}{
		{
			name:    "refuses to guess a host when unset",
			token:   "abc123",
			wantErr: errNoAppBaseURL,
		},
		{
			name:    "refuses a value that is only whitespace",
			baseURL: "   ",
			token:   "abc123",
			wantErr: errNoAppBaseURL,
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
			got, err := shareURL(tt.token)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("shareURL(%q) error = %v, want %v", tt.token, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("shareURL(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}
