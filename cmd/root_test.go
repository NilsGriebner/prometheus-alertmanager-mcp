package cmd

import (
	"os"
	"slices"
	"testing"

	"github.com/spf13/viper"
)

func TestNormalizeScopes(t *testing.T) {
	tests := map[string]struct {
		in   []string
		want []string
	}{
		"env arrives as one string": {
			in:   []string{"openid,email,offline_access"},
			want: []string{"openid", "email", "offline_access"},
		},
		"flag is already split": {
			in:   []string{"openid", "email"},
			want: []string{"openid", "email"},
		},
		"spaces and empties are dropped": {
			in:   []string{" openid , ,email "},
			want: []string{"openid", "email"},
		},
		"empty stays empty": {in: nil, want: []string{}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeScopes(tc.in); !slices.Equal(got, tc.want) {
				t.Errorf("normalizeScopes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The environment variable path is the one that silently misbehaved: viper
// splits it on whitespace, not commas.
func TestScopesFromEnvironmentAreSplit(t *testing.T) {
	t.Setenv("ALERTMANAGER_OIDC_SCOPES", "openid,email,offline_access")
	defer viper.Reset()

	viper.Reset()

	if err := viper.BindEnv(
		"alertmanager.oidc.scopes", "ALERTMANAGER_OIDC_SCOPES",
	); err != nil {
		t.Fatalf("BindEnv: %v", err)
	}

	raw := viper.GetStringSlice("alertmanager.oidc.scopes")
	if len(raw) != 1 {
		t.Fatalf("expected viper to yield one unsplit value, got %q", raw)
	}

	want := []string{"openid", "email", "offline_access"}
	if got := normalizeScopes(raw); !slices.Equal(got, want) {
		t.Errorf("normalizeScopes(%q) = %q, want %q", raw, got, want)
	}

	_ = os.Unsetenv("ALERTMANAGER_OIDC_SCOPES")
}
