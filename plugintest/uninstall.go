package plugintest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// UninstallTimeout is the deadline the host gives Plugin.Uninstall (PROTOCOL.md §2.4).
const UninstallTimeout = 10 * time.Second

// checkUninstall calls Plugin.Uninstall like the host does before it stops the plugin for an uninstall. The host goes
// on with the uninstall whatever the answer, but a plugin that implements it should finish within the deadline and say
// whether it undid its outside setup.
func checkUninstall(ctx context.Context, t *testing.T, conn *grpc.ClientConn) {
	t.Helper()
	cctx, cancel := context.WithTimeout(ctx, UninstallTimeout)
	defer cancel()
	began := time.Now()
	_, err := pluginv1.NewPluginClient(conn).Uninstall(cctx, &pluginv1.UninstallRequest{})
	switch code := status.Code(err); {
	case err == nil:
	case code == codes.Unimplemented:
		t.Log("Plugin.Uninstall is not implemented; that is fine when the plugin sets up nothing outside the server")
	case code == codes.DeadlineExceeded || cctx.Err() != nil:
		t.Errorf("Plugin.Uninstall did not return within %v; the host stops waiting and uninstalls anyway", UninstallTimeout)
	default:
		t.Errorf("Plugin.Uninstall = %v after %v; want OK once the outside setup is undone (the host uninstalls anyway)",
			code, time.Since(began).Round(time.Millisecond))
	}
}
