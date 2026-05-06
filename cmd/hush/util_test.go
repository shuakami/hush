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

func TestSplitHostPath(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPath string
	}{
		{"hk1:/opt/uapipro/worker", "hk1", "/opt/uapipro/worker"},
		{"hk1:relative/path", "hk1", "relative/path"},
		{"./local/file", "", "./local/file"},
		{`C:\Users\win11\AppData\Local\Temp\file.txt`, "", `C:\Users\win11\AppData\Local\Temp\file.txt`},
		{"C:/Users/win11/AppData/Local/Temp/file.txt", "", "C:/Users/win11/AppData/Local/Temp/file.txt"},
	}
	for _, c := range cases {
		gotHost, gotPath := splitHostPath(c.in)
		if gotHost != c.wantHost || gotPath != c.wantPath {
			t.Errorf("splitHostPath(%q) = (%q, %q), want (%q, %q)", c.in, gotHost, gotPath, c.wantHost, c.wantPath)
		}
	}
}
