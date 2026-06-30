package blob

import (
	"encoding/hex"
	"testing"
)

// Validates the SigV4 signing-key HMAC chain against an independent implementation of the
// AWS spec (Python's hmac on the same inputs). Two independent impls of AWS4-HMAC-SHA256
// agreeing confirms the derivation; the live S3/R2 round-trip is verified at deploy.
func TestSigningKeyVector(t *testing.T) {
	key := signingKey("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "20120215", "us-east-1", "iam")
	if got, want := hex.EncodeToString(key), "004aa806e13dae88b9032d9261bcb04c67d023afadd221e6b0d206e1760e0b5e"; got != want {
		t.Fatalf("signing key mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestAWSEncodeAndPath(t *testing.T) {
	cases := map[string]string{
		"seg/0000000001.sms": "seg/0000000001.sms",
		"a b+c":              "a%20b%2Bc",
		"tilde~ok":           "tilde~ok",
		"slash/keep":         "slash/keep",
	}
	for in, want := range cases {
		if got := encodePath(in); got != want {
			t.Fatalf("encodePath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := awsEncode("a/b c", false); got != "a/b%20c" {
		t.Fatalf("awsEncode = %q", got)
	}
}
