package main

import "testing"

func TestResolveToken(t *testing.T) {
	for _, tc := range []struct {
		name, flag, env, want string
	}{
		{"environment", "", "secret_from_env", "secret_from_env"},
		{"flag overrides environment", "secret_from_flag", "secret_from_env", "secret_from_flag"},
		{"missing token", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveToken(tc.flag, tc.env); got != tc.want {
				t.Errorf("resolveToken = %q, want %q", got, tc.want)
			}
		})
	}
}
