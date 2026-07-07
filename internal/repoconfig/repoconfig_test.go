package repoconfig

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Config
		wantErr bool
	}{
		{
			name: "no file",
			raw:  "",
			want: Default,
		},
		{
			name: "partial file merges over defaults",
			raw: `
min_severity: warning
ignore:
  - "vendor/**"
`,
			want: Config{
				MinSeverity: "warning",
				Categories:  Default.Categories,
				Ignore:      []string{"vendor/**"},
				MaxFiles:    Default.MaxFiles,
				MaxComments: Default.MaxComments,
				Persona:     Default.Persona,
			},
		},
		{
			name: "full file honors every field",
			raw: `
min_severity: error
categories: [bug, security]
ignore:
  - "**/*.lock"
  - "vendor/**"
max_files: 5
max_comments: 3
persona: "concise senior engineer"
`,
			want: Config{
				MinSeverity: "error",
				Categories:  []string{"bug", "security"},
				Ignore:      []string{"**/*.lock", "vendor/**"},
				MaxFiles:    5,
				MaxComments: 3,
				Persona:     "concise senior engineer",
			},
		},
		{
			name:    "malformed yaml syntax falls back to defaults",
			raw:     "min_severity: [this is not valid\n",
			want:    Default,
			wantErr: true,
		},
		{
			name:    "invalid field value falls back to defaults",
			raw:     "min_severity: critical\n",
			want:    Default,
			wantErr: true,
		},
		{
			name:    "empty categories list falls back to defaults",
			raw:     "categories: []\n",
			want:    Default,
			wantErr: true,
		},
		{
			name:    "unknown category falls back to defaults",
			raw:     "categories: [bug, made-up]\n",
			want:    Default,
			wantErr: true,
		},
		{
			name:    "non-positive max_files falls back to defaults",
			raw:     "max_files: 0\n",
			want:    Default,
			wantErr: true,
		},
		{
			name:    "non-positive max_comments falls back to defaults",
			raw:     "max_comments: -1\n",
			want:    Default,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.raw))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
