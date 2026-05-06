package main

import "testing"

func TestNormalizeSecretBodyConvertsWindowsNewlines(t *testing.T) {
	got := normalizeSecretBody([]byte("line1\r\nline2\r\n"))
	if got != "line1\nline2" {
		t.Fatalf("normalizeSecretBody() = %q, want %q", got, "line1\nline2")
	}
}

func TestNormalizeSecretBodyConvertsBareCarriageReturns(t *testing.T) {
	got := normalizeSecretBody([]byte("line1\rline2\r"))
	if got != "line1\nline2" {
		t.Fatalf("normalizeSecretBody() = %q, want %q", got, "line1\nline2")
	}
}
