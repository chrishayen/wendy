package main

import "testing"

func TestParseNodeURLMap(t *testing.T) {
	got, err := parseNodeURLMap("node_linux_gpu=http://linux.local:18087/, node_mac=http://mac.local:18087")
	if err != nil {
		t.Fatalf("parseNodeURLMap: %v", err)
	}
	if got["node_linux_gpu"] != "http://linux.local:18087" {
		t.Fatalf("linux node URL = %q", got["node_linux_gpu"])
	}
	if got["node_mac"] != "http://mac.local:18087" {
		t.Fatalf("mac node URL = %q", got["node_mac"])
	}
}

func TestParseNodeURLMapRejectsMalformedEntry(t *testing.T) {
	if _, err := parseNodeURLMap("node_linux_gpu"); err == nil {
		t.Fatal("expected malformed mapping error")
	}
}
