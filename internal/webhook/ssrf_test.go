package webhook

import "testing"

// The SSRF guard must block webhook delivery to loopback / private / cloud-metadata
// addresses (which resolve to blocked IPs), so a webhook can't exfiltrate creds or scan.
func TestSSRFBlocksInternalTargets(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1:9999/x",             // loopback
		"http://169.254.169.254/latest/meta/", // cloud metadata (link-local)
		"http://10.0.0.5/internal",            // private
		"http://192.168.1.1/",                 // private
		"http://[::1]:80/",                    // ipv6 loopback
	}
	for _, u := range blocked {
		err := Send(Endpoint{URL: u, Secret: "s"}, []byte("{}"))
		if err == nil {
			t.Fatalf("SSRF guard let a request through to %s", u)
		}
	}
}

func TestSchemeValidation(t *testing.T) {
	s := &Store{}
	for _, bad := range []string{"file:///etc/passwd", "gopher://x", "ftp://x", "notaurl"} {
		if _, err := s.Add("x", bad); err == nil {
			t.Fatalf("accepted a non-http(s) webhook url: %s", bad)
		}
	}
	if _, err := s.Add("ok", "https://example.com/hook"); err != nil {
		t.Fatalf("rejected a valid https url: %v", err)
	}
}
