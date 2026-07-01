package main

import "testing"

// The public-bind guard hinges on isLoopbackBind: loopback = safe (unreachable from other
// machines), wildcard/LAN/public = needs a password. Pin the classification.
func TestIsLoopbackBind(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080":   true,  // loopback
		"localhost:8080":   true,  // loopback
		"[::1]:8080":       true,  // ipv6 loopback
		":8080":            false, // wildcard = all interfaces = public
		"0.0.0.0:8080":     false, // wildcard
		"[::]:8080":        false, // ipv6 wildcard
		"192.168.1.5:8080": false, // LAN — reachable from other machines
		"10.0.0.1:8080":    false, // private but reachable
	}
	for addr, want := range cases {
		if got := isLoopbackBind(addr); got != want {
			t.Errorf("isLoopbackBind(%q) = %v, want %v", addr, got, want)
		}
	}
}
