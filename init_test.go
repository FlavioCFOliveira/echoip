package main

import "testing"

func TestValidatePort(t *testing.T) {
	cases := []struct {
		port int
		ok   bool
	}{
		{1, true},
		{80, true},
		{8080, true},
		{65535, true},
		{0, false},
		{-1, false},
		{65536, false},
		{1 << 20, false},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			err := validatePort(tc.port)
			if tc.ok && err != nil {
				t.Errorf("validatePort(%d) = %v, want nil", tc.port, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("validatePort(%d) = nil, want error", tc.port)
			}
		})
	}
}

func TestValidateHost(t *testing.T) {
	cases := []struct {
		host string
		ok   bool
	}{
		{"", true},
		{"0.0.0.0", true},
		{"127.0.0.1", true},
		{"::", true},
		{"::1", true},
		{"2001:db8::1", true},
		{"localhost", true},
		{"echo-ip.com", true},
		{"foo.bar.baz.qux", true},
		{"invalid host", false},            // whitespace
		{"foo\nbar", false},                // newline
		{"\x7f", false},                    // DEL
		{string(make([]byte, 254)), false}, // too long
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			err := validateHost(tc.host)
			if tc.ok && err != nil {
				t.Errorf("validateHost(%q) = %v, want nil", tc.host, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("validateHost(%q) = nil, want error", tc.host)
			}
		})
	}
}
