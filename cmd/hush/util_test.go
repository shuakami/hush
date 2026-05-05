package main

import "testing"

func TestClientEndpointFromListen(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"127.0.0.1:8443", "http://127.0.0.1:8443"},
		{"0.0.0.0:8443", "http://127.0.0.1:8443"},
		{":8443", "http://127.0.0.1:8443"},
		{"[::]:8443", "http://127.0.0.1:8443"},
		{"hush.example.com:443", "http://hush.example.com:443"},
		{"127.0.0.1:9999", "http://127.0.0.1:9999"},
	}
	for _, c := range cases {
		if got := clientEndpointFromListen(c.in); got != c.want {
			t.Errorf("clientEndpointFromListen(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
