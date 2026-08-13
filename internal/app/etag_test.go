package app

import "testing"

func TestEtagCompat(t *testing.T) {
	etags := []string{
		"df5f8f335a1043be16e3e6e8f83c3072",
		"2b4f1a5f1b96d48e5e3b6dfa9c1d8e2a",
		"00000000000000000000000000000000",
		"ffffffffffffffffffffffffffffffff",
		"0123456789abcdef0123456789abcdef",
	}
	for _, e := range etags {
		enc := encryptEtagTo123FastLinkEtag(e)
		dec := decrypt123FastLinkEtagToEtag(enc)
		if dec != e {
			t.Errorf("roundtrip failed: %s -> %s -> %s", e, enc, dec)
		}
		t.Logf("%s -> %s -> %s", e, enc, dec)
	}
}
