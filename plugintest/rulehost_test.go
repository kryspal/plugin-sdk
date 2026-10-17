package plugintest

import "testing"

func TestRuleAddressClassification(t *testing.T) {
	for _, tc := range []struct {
		host                   string
		isAddr, never, private bool
	}{
		{"images.example.com", false, false, false},
		{"192.168.1.20", true, false, true},
		{"10.0.0.0/8", true, false, true},
		{"fd00::/8", true, false, true},
		{"100.64.1.1", true, false, true},
		{"8.8.8.8", true, false, false},
		{"127.0.0.1", true, true, false},
		{"::1", true, true, false},
		{"169.254.169.254", true, true, false},
		{"fe80::1", true, true, false},
		{"224.0.0.251", true, true, false},
		{"0.0.0.0/0", true, true, false},
		{"::ffff:127.0.0.1", true, true, false},
	} {
		p, isAddr := parseRuleAddr(tc.host)
		if isAddr != tc.isAddr {
			t.Errorf("%q: isAddr = %v, want %v", tc.host, isAddr, tc.isAddr)
			continue
		}
		if !isAddr {
			continue
		}
		if got := ruleAddrNeverFetched(p); got != tc.never {
			t.Errorf("%q: never fetched = %v, want %v", tc.host, got, tc.never)
		}
		if got := ruleAddrPrivate(p); got != tc.private {
			t.Errorf("%q: private = %v, want %v", tc.host, got, tc.private)
		}
	}
	if p, isAddr := parseRuleAddr("192.168.1.0/33"); !isAddr || p.IsValid() {
		t.Error("a malformed CIDR should be an address rule with an invalid prefix")
	}
}
