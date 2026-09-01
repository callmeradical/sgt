package ui

import (
	"testing"
)

func TestValidateChangeID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "valid simple id",
			id:      "test-change",
			wantErr: false,
		},
		{
			name:    "valid alphanumeric id",
			id:      "change-123-abc",
			wantErr: false,
		},
		{
			name:    "empty id",
			id:      "",
			wantErr: true,
		},
		{
			name:    "forward slash",
			id:      "a/b",
			wantErr: true,
		},
		{
			name:    "backslash",
			id:      "a\\b",
			wantErr: true,
		},
		{
			name:    "dot dot",
			id:      "..",
			wantErr: true,
		},
		{
			name:    "dot dot embedded",
			id:      "a..b",
			wantErr: true,
		},
		{
			name:    "single dot",
			id:      ".",
			wantErr: true,
		},
		{
			name:    "dot dot path traversal",
			id:      "../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "dot dot suffix",
			id:      "some-path/..",
			wantErr: true,
		},
		{
			name:    "valid dot embedded (not dot dot)",
			id:      "a.b",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateChangeID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateChangeID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
