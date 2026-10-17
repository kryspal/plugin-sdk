package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// TestStaticPlugin starts the example plugin the way the host does: compile on the spot, clean environment, handshake,
// calls over the Unix socket, and finally SIGTERM.
func TestStaticPlugin(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not in PATH")
	}
	bin := filepath.Join(t.TempDir(), "static")
	build := exec.CommandContext(t.Context(), goBin, "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Unix socket paths are limited to 108 bytes; t.TempDir includes the test name and may be too long, so create a separate
	// directory with a short path.
	sockDir, err := os.MkdirTemp("", "msp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	if err := os.Chmod(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("RefusesWithoutCookie", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), bin)
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("err = %v, want exit status 1", err)
		}
		if !strings.Contains(string(out), "must be started by the server") {
			t.Fatalf("output = %q", out)
		}
	})

	// Started by an older host from before the rename (passes only MEDIASERVER_PLUGIN_*): must give a clear "host too old"
	// message, not a vague cookie error.
	t.Run("ExplainsAnOldHost", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), bin)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "MEDIASERVER_PLUGIN_COOKIE=b9a6f1d2-mediaserver-plugin", "MEDIASERVER_PLUGIN_PROTOCOLS=1", "MEDIASERVER_PLUGIN_SOCKET_DIR=" + sockDir}
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || !strings.Contains(string(out), "needs a newer server") {
			t.Fatalf("err = %v, output = %q", err, out)
		}
	})

	t.Run("RefusesUnsupportedProtocol", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), bin)
		cmd.Env = []string{
			pluginapi.EnvCookie + "=" + pluginapi.CookieValue,
			pluginapi.EnvProtocols + "=2,3",
			pluginapi.EnvSocketDir + "=" + sockDir,
		}
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "no common protocol") {
			t.Fatalf("err = %v, output = %q", err, out)
		}
	})

	cmd := exec.Command(bin) // stopped with SIGTERM, not bound to ctx (canceling ctx would send SIGKILL)
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + t.TempDir(),
		pluginapi.EnvCookie + "=" + pluginapi.CookieValue,
		pluginapi.EnvProtocols + "=1,2",
		pluginapi.EnvSocketDir + "=" + sockDir,
	}
	cmd.Stderr = testWriter{t}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
	})

	lineCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lineCh <- line
		// stdout should stay quiet after the handshake; stop only at EOF.
		rest, _ := bufio.NewReader(stdout).ReadString('\n')
		if rest != "" {
			t.Errorf("plugin wrote to stdout after handshake: %q", rest)
		}
		exited <- cmd.Wait()
	}()
	var line string
	select {
	case line = <-lineCh:
	case <-time.After(10 * time.Second):
		t.Fatal("handshake timeout")
	}
	hs, err := pluginapi.ParseHandshake(line)
	if err != nil {
		t.Fatal(err)
	}
	if hs.Protocol != 1 || hs.Network != pluginapi.NetworkUnix || hs.Address != filepath.Join(sockDir, pluginapi.SocketName) {
		t.Fatalf("handshake = %+v", hs)
	}
	if fi, err := os.Stat(hs.Address); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket stat = %v, %v", fi, err)
	}

	conn, err := grpc.NewClient("unix://"+hs.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	plugin := pluginv1.NewPluginClient(conn)
	meta := pluginv1.NewMetadataProviderClient(conn)

	t.Run("Health", func(t *testing.T) {
		h := healthpb.NewHealthClient(conn)
		for _, svc := range []string{"", pluginv1.HealthServicePlugin, pluginv1.HealthServiceMetadataProvider,
			pluginv1.HealthServiceImageProvider, pluginv1.HealthServiceSubtitleProvider, pluginv1.HealthServiceUserSync} {
			resp, err := h.Check(ctx, &healthpb.HealthCheckRequest{Service: svc})
			if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				t.Fatalf("health %q = %v, %v", svc, resp, err)
			}
		}
	})

	t.Run("Describe", func(t *testing.T) {
		m, err := plugin.Describe(ctx, &pluginv1.DescribeRequest{Language: "zh-TW"})
		if err != nil {
			t.Fatal(err)
		}
		if m.GetId() != "static" || m.GetProtocolVersion() != 1 || len(m.GetCapabilities()) != 10 {
			t.Fatalf("manifest = %v", m)
		}
		f := m.GetConfigFields()
		if len(f) != 2 || f[0].GetKey() != "api_key" || f[0].GetType() != pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_SECRET ||
			!f[0].GetRequired() || f[0].GetLabel() != "API 金鑰" || f[1].GetGroup() != "advanced" || len(f[1].GetOptions()) != 2 {
			t.Fatalf("config fields = %v", f)
		}
		if m.GetAttribution().GetText() == "" {
			t.Fatal("attribution missing")
		}
	})

	t.Run("NeedsConfiguration", func(t *testing.T) {
		_, err := meta.Search(ctx, &pluginv1.SearchRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "static"})
		wantCode(t, err, codes.FailedPrecondition)
		resp, err := plugin.Configure(ctx, &pluginv1.ConfigureRequest{Values: map[string]string{"api_key": "wrong"}, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetFieldErrors()["api_key"] != "Invalid API key." {
			t.Fatalf("configure = %v", resp)
		}
		_, err = meta.Search(ctx, &pluginv1.SearchRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "static"})
		wantCode(t, err, codes.FailedPrecondition)
	})

	resp, err := plugin.Configure(ctx, &pluginv1.ConfigureRequest{Values: map[string]string{"api_key": validKey, "language_mode": "auto"}})
	if err != nil || resp.GetError() != "" || len(resp.GetFieldErrors()) != 0 {
		t.Fatalf("configure = %v, %v", resp, err)
	}

	t.Run("Search", func(t *testing.T) {
		r, err := meta.Search(ctx, &pluginv1.SearchRequest{
			Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Static Movie", Year: 2001,
			Languages: &pluginv1.Languages{Language: "zh-TW", Fallbacks: []string{"zh-CN"}},
		})
		if err != nil || len(r.GetCandidates()) != 1 || r.GetCandidates()[0].GetId() != movieID {
			t.Fatalf("search = %v, %v", r, err)
		}
		r, err = meta.Search(ctx, &pluginv1.SearchRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "nothing"})
		if err != nil || len(r.GetCandidates()) != 0 {
			t.Fatalf("empty search = %v, %v", r, err)
		}
	})

	t.Run("RateLimited", func(t *testing.T) {
		var trailer metadata.MD
		_, err := meta.Search(ctx, &pluginv1.SearchRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "rate-limit"}, grpc.Trailer(&trailer))
		wantCode(t, err, codes.ResourceExhausted)
		if d, ok := pluginv1.RetryAfter(trailer); !ok || d != 30*time.Second {
			t.Fatalf("retry after = %v, %v (trailer %v)", d, ok, trailer)
		}
	})

	t.Run("GetItem", func(t *testing.T) {
		it, err := meta.GetItem(ctx, &pluginv1.GetItemRequest{
			Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Id: movieID, Languages: &pluginv1.Languages{Language: "zh-TW"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if it.GetName() != "靜態電影" || it.GetPremiereDate() != "2001-02-03" || len(it.GetCredits()) != 2 ||
			it.GetCredits()[0].GetPersonId() != personID || it.GetCollection().GetId() != collectionID || len(it.GetImages()) != 2 {
			t.Fatalf("item = %v", it)
		}
		_, err = meta.GetItem(ctx, &pluginv1.GetItemRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Id: "404"})
		wantCode(t, err, codes.NotFound)
	})

	t.Run("GetPersonAndCollection", func(t *testing.T) {
		p, err := meta.GetPerson(ctx, &pluginv1.GetPersonRequest{Id: personID})
		if err != nil || p.GetGender() != pluginv1.Gender_GENDER_FEMALE || len(p.GetAliases()) != 1 {
			t.Fatalf("person = %v, %v", p, err)
		}
		c, err := meta.GetCollection(ctx, &pluginv1.GetCollectionRequest{Id: collectionID})
		if err != nil || len(c.GetParts()) != 1 {
			t.Fatalf("collection = %v, %v", c, err)
		}
	})

	t.Run("ServiceNamesAreKryspal", func(t *testing.T) {
		// The wire name changed to kryspal.plugin.v1: calling with the old name must get UNIMPLEMENTED, not silently
		// succeed.
		err := conn.Invoke(ctx, "/mediaserver.plugin.v1.Plugin/Describe", &pluginv1.DescribeRequest{}, &pluginv1.Manifest{})
		wantCode(t, err, codes.Unimplemented)
		if err := conn.Invoke(ctx, "/kryspal.plugin.v1.Plugin/Describe", &pluginv1.DescribeRequest{}, &pluginv1.Manifest{}); err != nil {
			t.Fatalf("the new service name: %v", err)
		}
	})

	t.Run("Images", func(t *testing.T) {
		c := pluginv1.NewImageProviderClient(conn)
		r, err := c.GetImages(ctx, &pluginv1.GetImagesRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, ProviderIds: map[string]string{"static": movieID}})
		if err != nil || len(r.GetImages()) != 2 || r.GetImages()[0].GetLikes() != 12 || r.GetImages()[0].GetSourceId() != "p1" {
			t.Fatalf("images = %v, %v", r, err)
		}
		r, err = c.GetImages(ctx, &pluginv1.GetImagesRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, ProviderIds: map[string]string{"static": movieID},
			Types: []pluginv1.ImageType{pluginv1.ImageType_IMAGE_TYPE_LOGO}})
		if err != nil || len(r.GetImages()) != 1 || r.GetImages()[0].GetType() != pluginv1.ImageType_IMAGE_TYPE_LOGO {
			t.Fatalf("filtered images = %v, %v", r, err)
		}
		r, err = c.GetImages(ctx, &pluginv1.GetImagesRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, ProviderIds: map[string]string{"tmdb": "1"}})
		if err != nil || len(r.GetImages()) != 0 {
			t.Fatalf("unknown ids must give an empty list: %v, %v", r, err)
		}
	})

	t.Run("Subtitles", func(t *testing.T) {
		c := pluginv1.NewSubtitleProviderClient(conn)
		r, err := c.Search(ctx, &pluginv1.SearchSubtitlesRequest{Target: &pluginv1.SubtitleTarget{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Title: "Static Movie"}, Languages: []string{"en"}})
		if err != nil || len(r.GetCandidates()) != 2 || r.GetCandidates()[0].GetMatchedBy() != pluginv1.SubtitleCandidate_MATCHED_BY_IDS {
			t.Fatalf("search = %v, %v", r, err)
		}
		d, err := c.Download(ctx, &pluginv1.DownloadSubtitleRequest{Id: "sub-1"})
		if err != nil || len(d.GetFiles()) != 1 || !strings.Contains(string(d.GetFiles()[0].GetData()), "Hello") || d.GetQuotaRemaining() != 7 {
			t.Fatalf("download = %v, %v", d, err)
		}
		var trailer metadata.MD
		_, err = c.Download(ctx, &pluginv1.DownloadSubtitleRequest{Id: "quota"}, grpc.Trailer(&trailer))
		wantCode(t, err, codes.ResourceExhausted)
		if dur, ok := pluginv1.RetryAfter(trailer); !ok || dur != time.Hour || pluginv1.RetryScope(trailer) != pluginv1.RetryScopePlugin {
			t.Fatalf("a quota error must pause the whole plugin: %v %v scope %q", dur, ok, pluginv1.RetryScope(trailer))
		}
		_, err = c.Download(ctx, &pluginv1.DownloadSubtitleRequest{Id: "nope"})
		wantCode(t, err, codes.NotFound)
	})

	t.Run("UserSync", func(t *testing.T) {
		c := pluginv1.NewUserSyncClient(conn)
		creds := func(access, refresh string) *pluginv1.Credentials {
			return &pluginv1.Credentials{Data: []byte(`{"access":"` + access + `","refresh":"` + refresh + `"}`)}
		}
		// Linking: the state from BeginLink is passed back to PollLink unchanged.
		ch, err := c.BeginLink(ctx, &pluginv1.BeginLinkRequest{})
		if err != nil || ch.GetUserCode() == "" || ch.GetIntervalSeconds() != 5 || string(ch.GetState()) != "device-1" {
			t.Fatalf("challenge = %v, %v", ch, err)
		}
		lr, err := c.PollLink(ctx, &pluginv1.PollLinkRequest{State: ch.GetState()})
		if err != nil || lr.GetStatus() != pluginv1.LinkResult_STATUS_AUTHORIZED || lr.GetAccountLabel() != "static-user" || len(lr.GetCredentials().GetData()) == 0 {
			t.Fatalf("link = %v, %v", lr, err)
		}
		if lr, err = c.PollLink(ctx, &pluginv1.PollLinkRequest{State: []byte("pending")}); err != nil || lr.GetStatus() != pluginv1.LinkResult_STATUS_PENDING {
			t.Fatalf("pending = %v, %v", lr, err)
		}
		if _, err := c.Unlink(ctx, &pluginv1.UnlinkRequest{Credentials: lr.GetCredentials()}); err != nil {
			t.Fatal(err)
		}

		// scrobble: success, credential rotation (updated_credentials), invalid credential, rate limit on this credential only.
		sr, err := c.Scrobble(ctx, &pluginv1.ScrobbleRequest{Credentials: creds("a1", "r1"), EventId: "e1", Action: pluginv1.ScrobbleRequest_ACTION_START})
		if err != nil || sr.GetUpdatedCredentials() != nil {
			t.Fatalf("scrobble = %v, %v", sr, err)
		}
		sr, err = c.Scrobble(ctx, &pluginv1.ScrobbleRequest{Credentials: creds("a1", "old"), EventId: "e2", Action: pluginv1.ScrobbleRequest_ACTION_STOP})
		if err != nil || !strings.Contains(string(sr.GetUpdatedCredentials().GetData()), `"r2"`) {
			t.Fatalf("a rotated refresh token must come back in updated_credentials: %v, %v", sr, err)
		}
		_, err = c.Scrobble(ctx, &pluginv1.ScrobbleRequest{Credentials: creds("revoked", "r"), EventId: "e3"})
		wantCode(t, err, codes.Unauthenticated)
		var trailer metadata.MD
		_, err = c.Scrobble(ctx, &pluginv1.ScrobbleRequest{Credentials: creds("limited", "r"), EventId: "e4"}, grpc.Trailer(&trailer))
		wantCode(t, err, codes.ResourceExhausted)
		if d, ok := pluginv1.RetryAfter(trailer); !ok || d != 20*time.Second || pluginv1.RetryScope(trailer) != pluginv1.RetryScopeCredential {
			t.Fatalf("a per-user limit must be scoped to the credential: %v %v scope %q", d, ok, pluginv1.RetryScope(trailer))
		}

		// Pull pagination, and unmatched entries on push.
		pr, err := c.PullWatched(ctx, &pluginv1.PullWatchedRequest{Credentials: creds("a1", "r1")})
		if err != nil || len(pr.GetEntries()) != 2 || pr.GetNextCursor() != "p2" || pr.GetEntries()[1].GetWatched() {
			t.Fatalf("pull page 1 = %v, %v", pr, err)
		}
		pr, err = c.PullWatched(ctx, &pluginv1.PullWatchedRequest{Credentials: creds("a1", "r1"), Cursor: pr.GetNextCursor()})
		if err != nil || len(pr.GetEntries()) != 1 || pr.GetNextCursor() != "" {
			t.Fatalf("pull page 2 = %v, %v", pr, err)
		}
		ref := func(id string) *pluginv1.WatchedEntry {
			return &pluginv1.WatchedEntry{Media: &pluginv1.MediaRef{ProviderIds: map[string]string{"static": id}}, Watched: true}
		}
		pw, err := c.PushWatched(ctx, &pluginv1.PushWatchedRequest{Credentials: creds("a1", "r1"), Entries: []*pluginv1.WatchedEntry{ref(movieID), ref("9999")}})
		if err != nil || len(pw.GetUnmatched()) != 1 || pw.GetUnmatched()[0] != 1 {
			t.Fatalf("push = %v, %v", pw, err)
		}
		_, err = c.PullWatched(ctx, &pluginv1.PullWatchedRequest{Credentials: &pluginv1.Credentials{}})
		wantCode(t, err, codes.Unauthenticated)
	})

	t.Run("SIGTERM", func(t *testing.T) {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-exited:
			if err != nil {
				t.Fatalf("exit = %v, want clean exit", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("plugin did not exit after SIGTERM")
		}
		if _, err := os.Stat(hs.Address); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("socket not removed: %v", err)
		}
	})
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("code = %v (%v), want %v", got, err, want)
	}
}

// testWriter forwards the plugin's stderr into the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
