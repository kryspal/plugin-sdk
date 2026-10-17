package plugintest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/kryspal/plugin-sdk"
)

// Start launches a built plugin binary the way the host does, waits for its handshake and returns a client connection
// to it, so a test can call any service the plugin implements. Only Options.Env and Options.Setup are used. When the
// test ends the plugin gets SIGTERM, and is killed if it has not exited within ShutdownTimeout.
//
//	conn := plugintest.Start(t, bin, plugintest.Options{Env: map[string]string{"UPSTREAM_URL": srv.URL}})
//	resp, err := pluginv1.NewSubtitleProviderClient(conn).Search(t.Context(), req)
func Start(t *testing.T, binary string, opts Options) *grpc.ClientConn {
	t.Helper()
	p := start(t, absBinary(t, binary), socketDir(t), extraEnv(t, opts))
	t.Cleanup(func() {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.exited:
		case <-time.After(ShutdownTimeout):
			t.Errorf("the plugin did not exit within %v of SIGTERM", ShutdownTimeout)
		}
	})
	return p.conn
}

// Build compiles the Go main package pkg (for example "./cmd/myplugin", relative to the test's package directory)
// into a temporary directory and returns the binary's path.
func Build(t *testing.T, pkg string) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not in PATH")
	}
	bin := filepath.Join(t.TempDir(), "plugin")
	cmd := exec.CommandContext(t.Context(), goBin, "build", "-o", bin, pkg)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, out)
	}
	return bin
}

// RunSource builds the Go main package pkg with Build and runs the suite against the binary.
func RunSource(t *testing.T, pkg string, opts Options) {
	t.Helper()
	Run(t, Build(t, pkg), opts)
}

func absBinary(t *testing.T, binary string) string {
	t.Helper()
	bin, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("plugin binary: %v", err)
	}
	if fi.IsDir() {
		t.Fatalf("plugin binary %s is a directory; build it first, or use RunSource with the package path", binary)
	}
	return bin
}

// reservedEnv are the variables the host sets; a test cannot replace them.
var reservedEnv = []string{
	"PATH", "HOME", "TMPDIR",
	pluginapi.EnvCookie, pluginapi.EnvProtocols, pluginapi.EnvSocketDir, pluginapi.EnvDataDir,
}

// extraEnv runs Options.Setup and returns Options.Env plus its variables as KEY=VALUE pairs, sorted.
func extraEnv(t *testing.T, opts Options) []string {
	t.Helper()
	vars := map[string]string{}
	for k, v := range opts.Env {
		vars[k] = v
	}
	if opts.Setup != nil {
		for k, v := range opts.Setup(t) {
			vars[k] = v
		}
	}
	env := make([]string, 0, len(vars))
	for k, v := range vars {
		if k == "" || strings.Contains(k, "=") {
			t.Fatalf("invalid environment variable name %q", k)
		}
		if slices.Contains(reservedEnv, k) {
			t.Fatalf("Options.Env cannot set %s; the host sets it", k)
		}
		env = append(env, k+"="+v)
	}
	slices.Sort(env)
	return env
}
