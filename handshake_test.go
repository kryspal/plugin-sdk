package pluginapi_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/kryspal/plugin-sdk"
)

func TestParseHandshake(t *testing.T) {
	t.Parallel()
	good := map[string]pluginapi.Handshake{
		"1|unix|/tmp/p/plugin.sock\n":   {Protocol: 1, Network: "unix", Address: "/tmp/p/plugin.sock"},
		"2|unix|/a|b\r\n":               {Protocol: 2, Network: "unix", Address: "/a|b"},
		"1|tcp|127.0.0.1:7000":          {Protocol: 1, Network: "tcp", Address: "127.0.0.1:7000"},
		"10|unix|/run/ms/x/plugin.sock": {Protocol: 10, Network: "unix", Address: "/run/ms/x/plugin.sock"},
	}
	for line, want := range good {
		got, err := pluginapi.ParseHandshake(line)
		if err != nil || got != want {
			t.Errorf("ParseHandshake(%q) = %+v, %v; want %+v", line, got, err, want)
		}
	}
	if s := (pluginapi.Handshake{Protocol: 1, Network: "unix", Address: "/x"}).String(); s != "1|unix|/x" {
		t.Errorf("String = %q", s)
	}
	for _, line := range []string{"", "1|unix", "0|unix|/x", "a|unix|/x", "1|unix|rel/path", "1|udp|/x", "1|tcp|", "-1|unix|/x"} {
		if _, err := pluginapi.ParseHandshake(line); !errors.Is(err, pluginapi.ErrHandshake) {
			t.Errorf("ParseHandshake(%q) err = %v, want ErrHandshake", line, err)
		}
	}
}

func TestProtocols(t *testing.T) {
	t.Parallel()
	got, err := pluginapi.ParseProtocols(" 2,1,,2 ")
	if err != nil || !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("ParseProtocols = %v, %v", got, err)
	}
	if pluginapi.FormatProtocols(got) != "1,2" {
		t.Fatal("FormatProtocols")
	}
	for _, s := range []string{"", ",", "x", "0", "1,-2"} {
		if _, err := pluginapi.ParseProtocols(s); !errors.Is(err, pluginapi.ErrHandshake) {
			t.Errorf("ParseProtocols(%q) err = %v", s, err)
		}
	}
	if v, err := pluginapi.NegotiateProtocol([]int{1, 2}, []int{1, 2, 3}); err != nil || v != 2 {
		t.Fatalf("Negotiate = %v, %v", v, err)
	}
	if _, err := pluginapi.NegotiateProtocol([]int{2}, []int{1}); !errors.Is(err, pluginapi.ErrHandshake) {
		t.Fatalf("Negotiate no overlap err = %v", err)
	}
}

func TestDataDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := pluginapi.DataDir(env(map[string]string{pluginapi.EnvDataDir: "/data/p", "HOME": "/home/p"})); got != "/data/p" {
		t.Fatalf("DataDir with %s = %q, want /data/p", pluginapi.EnvDataDir, got)
	}
	if got := pluginapi.DataDir(env(map[string]string{"HOME": "/home/p"})); got != "/home/p" {
		t.Fatalf("DataDir on an older host = %q, want HOME /home/p", got)
	}
}

func TestHostFeatures(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == pluginapi.EnvHostFeatures {
				return v
			}
			return ""
		}
	}
	got := pluginapi.HostFeatures(env(" attributes, photo ,,future "))
	if !got[pluginapi.HostFeatureAttributes] || !got[pluginapi.HostFeaturePhoto] || !got["future"] || len(got) != 3 {
		t.Fatalf("HostFeatures = %v", got)
	}
	if got := pluginapi.HostFeatures(env("")); got == nil || len(got) != 0 {
		t.Fatalf("HostFeatures on an older host = %v, want an empty non-nil map", got)
	}
}
