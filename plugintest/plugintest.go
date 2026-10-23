// Package plugintest is the conformance suite for Kryspal Media plugins. It starts a plugin the way the host does
// (clean environment, handshake, gRPC over the plugin's Unix socket, SIGTERM) and checks the contract in PROTOCOL.md.
// Plugin authors call RunSource (or Run with a built binary) from an ordinary Go test:
//
//	func TestConformance(t *testing.T) {
//		plugintest.RunSource(t, "./cmd/myplugin", plugintest.Options{
//			Config:   map[string]string{"api_key": "test"},
//			Metadata: &plugintest.MetadataCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Big Buck Bunny"},
//		})
//	}
//
// The suite covers the handshake, the health check, Describe, Configure and shutdown, and for the capabilities the
// manifest declares: one metadata round trip, item and person images, person provider IDs and name recognition.
// Subtitle downloads, user sync, segments, danmaku, alias maps, cast targets and the Host service are not covered yet; use Start
// to get a connection to the running plugin and test them yourself.
package plugintest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// Timeouts the host applies (PROTOCOL.md §11.1): the plugin must print its handshake line and exit after SIGTERM within
// these. Each call below gets the same deadline the host gives it.
const (
	HandshakeTimeout = 10 * time.Second
	ShutdownTimeout  = 5 * time.Second

	healthTimeout    = 10 * time.Second
	describeTimeout  = 10 * time.Second
	configureTimeout = 30 * time.Second
	searchTimeout    = 15 * time.Second
	lookupTimeout    = 30 * time.Second // GetItem, GetPerson, GetImages
	recognizeTimeout = 5 * time.Second

	subtitleSearchTimeout = 15 * time.Second
	danmakuTimeout        = 30 * time.Second
)

// call runs f with a deadline of d.
func call[T any](ctx context.Context, d time.Duration, f func(context.Context) (T, error)) (T, error) {
	cctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return f(cctx)
}

// Options configure a conformance run.
type Options struct {
	// Config is sent with Configure and must be accepted (no error, no field errors). Leave it nil for plugins
	// without required configuration.
	Config map[string]string
	// Metadata, when set, runs a Search for this title and a GetItem for the first candidate. The plugin must declare
	// the matching metadata capability. When the plugin also declares the matching images.* capability, GetImages runs
	// with the item's provider IDs.
	Metadata *MetadataCase
	// Person, when set, runs GetPerson for this person. The plugin must declare metadata.person. When it is nil and
	// the plugin declares metadata.person, the first credit with a person_id from the metadata round trip is used.
	Person *PersonCase
	// PersonSearch, when set, runs SearchPerson for this name and checks the candidates (an ID, a name, and the ID under
	// the plugin ID). The plugin must declare metadata.person.search.
	PersonSearch *PersonSearchCase
	// Subtitles, when set, runs the subtitle Search for this video and checks the candidates: IDs, language, format,
	// matched_by, ranges and the limit. The plugin must declare subtitles.search.
	Subtitles *SubtitleCase
	// Danmaku, when set, runs GetDanmaku for this item and checks the answer: comments in time order, known modes, colors up to
	// 0xFFFFFF, text, and at most 20000 of them. The plugin must declare danmaku.provider.
	Danmaku *DanmakuCase
	// RunTasks names tasks (Manifest.tasks) to run once each with RunTask. The plugin must declare plugin.tasks.
	// Tasks are not run unless listed, since they may reach the network or take long.
	RunTasks []string
	// Recognize, when set, is a path the plugin is known to recognize. The plugin must declare media.recognize.
	// Plugins that declare media.recognize are always checked with an empty path, whether or not this is set.
	Recognize *RecognizeCase

	// LibraryCases check the plugin's per-library settings (PROTOCOL.md §5.4): that each setting changes the answer,
	// that requests for different libraries running at once keep their own settings, that bad values fall back to the
	// global one, and that a dry run changes nothing. The plugin should have a case for each setting it marks
	// library_override; a manifest with such fields and no cases only gets a note in the log.
	LibraryCases []LibraryCase

	// Env adds variables to the plugin's environment, for example the address of a fake upstream service. The
	// variables the host sets (the KRYSPAL_PLUGIN_* variables, PATH, HOME and TMPDIR) cannot be overridden, with one
	// exception: KRYSPAL_HOST_FEATURES, which lists every feature by default; set it to "" to test against an older host.
	Env map[string]string
	// Setup, when set, runs once before the plugin starts, for example to start a fake upstream with httptest. The
	// variables it returns are added to Env. Stop what it starts with t.Cleanup.
	Setup func(t *testing.T) map[string]string
}

// MetadataCase is a title the plugin is known to find.
type MetadataCase struct {
	Type pluginv1.ItemType // ITEM_TYPE_MOVIE or ITEM_TYPE_SERIES
	Name string
	Year int32
}

// PersonCase is a person the plugin is known to find.
type PersonCase struct {
	ID string // the person's ID at this provider
}

// PersonSearchCase is a person name the plugin is known to find with SearchPerson.
type PersonSearchCase struct {
	Name string
}

// SubtitleCase is a video the plugin is known to find subtitles for.
type SubtitleCase struct {
	Type  pluginv1.ItemType // ITEM_TYPE_MOVIE or ITEM_TYPE_EPISODE
	Title string
	// OriginalTitle and ProductCode are sent as the host sends them (host feature lookup_names); empty when the item has none.
	OriginalTitle, ProductCode string
	Year                       int32
	// Season and Episode are sent for an episode.
	Season, Episode int32
	// ProviderIDs are the item's IDs as the host sends them (lower case keys, for example imdb).
	ProviderIDs map[string]string
	// Languages are the wanted languages (BCP 47); en when empty.
	Languages []string
}

// DanmakuCase is a movie or an episode the plugin is known to have danmaku for.
type DanmakuCase struct {
	Type  pluginv1.ItemType // ITEM_TYPE_MOVIE or ITEM_TYPE_EPISODE
	Title string
	// OriginalTitle and ProductCode are sent as the host sends them (host feature lookup_names); empty when the item has none.
	OriginalTitle, ProductCode string
	Year                       int32
	// Season and Episode are sent for an episode.
	Season, Episode int32
	// ProviderIDs are the item's IDs as the host sends them (lower case keys, for example imdb).
	ProviderIDs map[string]string
}

// RecognizeCase is a path the plugin is known to recognize.
type RecognizeCase struct {
	LibraryKind pluginv1.LibraryKind
	Type        pluginv1.ItemType // the host's current judgment: a movie, a series or an episode
	Path        []string          // segments relative to the library root; the last one is the folder or file name
	Siblings    []string
}

// Run checks the plugin binary against the protocol. Failures are reported on t; Run returns when every check ran.
func Run(t *testing.T, binary string, opts Options) {
	t.Helper()
	bin := absBinary(t, binary)
	extra := extraEnv(t, opts)
	sockDir := socketDir(t)

	t.Run("RefusesWithoutCookie", func(t *testing.T) {
		out, err := runToExit(t, bin, []string{"PATH=/usr/bin:/bin"})
		if err == nil {
			t.Fatalf("the plugin started without %s; it must exit with a non-zero status", pluginapi.EnvCookie)
		}
		if strings.TrimSpace(out) != "" {
			t.Errorf("stdout = %q, want nothing (explain on stderr)", out)
		}
	})
	t.Run("RefusesUnsupportedProtocol", func(t *testing.T) {
		env := hostEnv(t, sockDir, []int{pluginv1.ProtocolVersion + 100}, extra)
		if _, err := runToExit(t, bin, env); err == nil {
			t.Fatal("the plugin started although no protocol version is shared; it must exit with a non-zero status")
		}
	})

	p := start(t, bin, sockDir, extra)
	ctx := t.Context()

	t.Run("Health", func(t *testing.T) {
		cctx, cancel := context.WithTimeout(ctx, healthTimeout)
		defer cancel()
		resp, err := healthpb.NewHealthClient(p.conn).Check(cctx, &healthpb.HealthCheckRequest{})
		if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("Check(\"\") = %v, %v; want SERVING", resp.GetStatus(), err)
		}
	})

	var manifest *pluginv1.Manifest
	t.Run("Describe", func(t *testing.T) {
		manifest = describe(ctx, t, p.conn, "")
		if manifest.GetId() == "" || manifest.GetVersion() == "" || manifest.GetName() == "" {
			t.Errorf("manifest id, name and version are required: %+v", manifest)
		}
		if got := int(manifest.GetProtocolVersion()); got != p.handshake.Protocol {
			t.Errorf("protocol_version = %d, handshake chose %d", got, p.handshake.Protocol)
		}
		if len(manifest.GetCapabilities()) == 0 {
			t.Error("the manifest declares no capabilities")
		}
		checkManifest(t, manifest)
		checkAutoFillLimit(t, manifest, hostListsFeature(extra, pluginapi.HostFeatureAutoFillLimit))
		// Only text may change with the language (PROTOCOL.md §5.1).
		for _, lang := range []string{"zh-CN", "ja"} {
			other := describe(ctx, t, p.conn, lang)
			if other.GetId() != manifest.GetId() || other.GetVersion() != manifest.GetVersion() ||
				!slices.Equal(other.GetCapabilities(), manifest.GetCapabilities()) {
				t.Errorf("Describe(%q) changed more than text: %+v", lang, other)
			}
		}
	})

	t.Run("Configure", func(t *testing.T) {
		cctx, cancel := context.WithTimeout(ctx, configureTimeout)
		defer cancel()
		resp, err := pluginv1.NewPluginClient(p.conn).Configure(cctx, &pluginv1.ConfigureRequest{Values: opts.Config})
		if err != nil {
			t.Fatalf("Configure: %v", err)
		}
		if resp.GetError() != "" || len(resp.GetFieldErrors()) > 0 {
			t.Fatalf("Configure rejected the configuration: %q %v", resp.GetError(), resp.GetFieldErrors())
		}
	})

	var item *pluginv1.ItemMetadata
	if mc := opts.Metadata; mc != nil {
		t.Run("MetadataRoundTrip", func(t *testing.T) { item = metadataRoundTrip(ctx, t, p.conn, manifest, *mc) })
		if item != nil && declares(manifest, imageCapability[item.GetType()]) {
			t.Run("ItemImages", func(t *testing.T) { itemImages(ctx, t, p.conn, item) })
		}
	}
	if personID := personToCheck(t, manifest, opts.Person, item); personID != "" {
		t.Run("Person", func(t *testing.T) { personRoundTrip(ctx, t, p.conn, manifest, personID) })
	}
	if pc := opts.PersonSearch; pc != nil {
		t.Run("PersonSearch", func(t *testing.T) { checkSearchPerson(ctx, t, p.conn, manifest, pc) })
	}
	if dc := opts.Danmaku; dc != nil {
		t.Run("Danmaku", func(t *testing.T) { checkDanmaku(ctx, t, p.conn, manifest, dc) })
	}
	if sc := opts.Subtitles; sc != nil {
		t.Run("Subtitles", func(t *testing.T) { checkSubtitles(ctx, t, p.conn, manifest, sc) })
	}
	if len(opts.RunTasks) > 0 {
		t.Run("Tasks", func(t *testing.T) { runTasks(ctx, t, p.conn, manifest, opts.RunTasks) })
	}
	if declares(manifest, pluginv1.CapabilityMediaRecognize) || opts.Recognize != nil {
		t.Run("Recognize", func(t *testing.T) { recognize(ctx, t, p.conn, manifest, opts.Recognize) })
	}

	if len(opts.LibraryCases) > 0 {
		t.Run("Library", func(t *testing.T) { libraryChecks(ctx, t, p.conn, manifest, opts.Config, opts.LibraryCases) })
	} else if hasLibraryOverride(manifest) {
		t.Log("the manifest marks fields library_override; add Options.LibraryCases to check them")
	}

	t.Run("Shutdown", func(t *testing.T) { p.stop(t) })
}

func describe(ctx context.Context, t *testing.T, conn *grpc.ClientConn, lang string) *pluginv1.Manifest {
	t.Helper()
	cctx, cancel := context.WithTimeout(ctx, describeTimeout)
	defer cancel()
	m, err := pluginv1.NewPluginClient(conn).Describe(cctx, &pluginv1.DescribeRequest{Language: lang})
	if err != nil {
		t.Fatalf("Describe(%q): %v", lang, err)
	}
	return m
}

func metadataRoundTrip(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, mc MetadataCase) *pluginv1.ItemMetadata {
	want := map[pluginv1.ItemType]string{
		pluginv1.ItemType_ITEM_TYPE_MOVIE:  pluginv1.CapabilityMetadataMovie,
		pluginv1.ItemType_ITEM_TYPE_SERIES: pluginv1.CapabilityMetadataSeries,
	}[mc.Type]
	if want == "" {
		t.Fatalf("MetadataCase.Type must be a movie or a series, got %v", mc.Type)
	}
	if !slices.Contains(m.GetCapabilities(), want) {
		t.Fatalf("the manifest does not declare %s", want)
	}
	c := pluginv1.NewMetadataProviderClient(conn)
	res, err := call(ctx, searchTimeout, func(cctx context.Context) (*pluginv1.SearchResponse, error) {
		return c.Search(cctx, &pluginv1.SearchRequest{Type: mc.Type, Name: mc.Name, Year: mc.Year})
	})
	if err != nil {
		t.Fatalf("Search(%q): %v", mc.Name, err)
	}
	checkNotices(t, "SearchResponse.notices", res.GetNotices(), false, 0)
	if len(res.GetCandidates()) == 0 {
		t.Fatalf("Search(%q) found nothing", mc.Name)
	}
	first := res.GetCandidates()[0]
	if first.GetId() == "" || first.GetType() != mc.Type {
		t.Fatalf("first candidate = %+v; want an id and type %v", first, mc.Type)
	}
	for i, c := range res.GetCandidates() {
		checkOwnIDKey(t, fmt.Sprintf("SearchCandidate %d", i), m.GetId(), c.GetId(), c.GetProviderIds(), false)
	}
	item, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.ItemMetadata, error) {
		return c.GetItem(cctx, &pluginv1.GetItemRequest{Type: mc.Type, Id: first.GetId()})
	})
	if err != nil {
		t.Fatalf("GetItem(%q): %v", first.GetId(), err)
	}
	if item.GetId() != first.GetId() || item.GetType() != mc.Type || item.GetName() == "" {
		t.Errorf("GetItem(%q) = id %q type %v name %q; want the candidate's id and type and a name",
			first.GetId(), item.GetId(), item.GetType(), item.GetName())
	}
	checkOwnIDKey(t, "GetItem("+first.GetId()+")", m.GetId(), item.GetId(), item.GetProviderIds(), false)
	for i, c := range item.GetCredits() {
		checkOwnIDKey(t, fmt.Sprintf("GetItem credit %d (%s)", i, c.GetName()), m.GetId(), c.GetPersonId(), c.GetProviderIds(), true)
	}
	checkImages(t, "ItemMetadata.images", item.GetImages(), nil)
	checkAttributes(t, "ItemMetadata.attributes", item.GetAttributes())
	checkFieldSources(t, "ItemMetadata.field_sources", item, item.GetFieldSources())
	checkNotices(t, "ItemMetadata.notices", item.GetNotices(), item.GetIncomplete(), item.GetRetryAfterSeconds())
	return item
}

// socketDir returns a short 0700 directory: Unix socket paths are limited to 107 bytes and t.TempDir may be longer.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kpt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// hostFeatures are the features a current host announces in EnvHostFeatures. field_sources is not among them: the
// host does not store field sources yet. The host's own list is checked against this one in its tests.
var hostFeatures = []string{
	pluginapi.HostFeatureAttributes, pluginapi.HostFeaturePhoto, pluginapi.HostFeatureStill, pluginapi.HostFeatureConfigTypes,
	pluginapi.HostFeatureImageReferer, pluginapi.HostFeatureTasks, pluginapi.HostFeatureImageCrop, pluginapi.HostFeatureConfigItems,
	pluginapi.HostFeatureItemChecks, pluginapi.HostFeatureScrapeNotices, pluginapi.HostFeaturePersonBackdrop,
	pluginapi.HostFeatureAutoFillLimit, pluginapi.HostFeatureSearchConfirmed, pluginapi.HostFeatureBadgeSuggestions,
	pluginapi.HostFeatureConfirmedNumbering, pluginapi.HostFeatureLibraryConfig, pluginapi.HostFeatureRecognitionHint,
	pluginapi.HostFeatureLookupNames,
}

// DefaultHostFeatures returns the features the suite announces to the plugin by default, the list of a current host.
func DefaultHostFeatures() []string { return slices.Clone(hostFeatures) }

// hostEnv is the clean environment the host passes (PROTOCOL.md §2.1), followed by the test's extra variables.
func hostEnv(t *testing.T, sockDir string, protocols []int, extra []string) []string {
	home := t.TempDir()
	env := []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + home,
		"TMPDIR=" + os.TempDir(),
		pluginapi.EnvCookie + "=" + pluginapi.CookieValue,
		pluginapi.EnvProtocols + "=" + pluginapi.FormatProtocols(protocols),
		pluginapi.EnvSocketDir + "=" + sockDir,
		pluginapi.EnvDataDir + "=" + home,
		pluginapi.EnvHostFeatures + "=" + strings.Join(hostFeatures, ","),
	}
	return append(env, extra...)
}

// runToExit runs the plugin until it exits (bounded by HandshakeTimeout) and returns its stdout.
func runToExit(t *testing.T, bin string, env []string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), HandshakeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = env
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if ctx.Err() != nil {
		t.Fatalf("the plugin did not exit within %v", HandshakeTimeout)
	}
	return string(out), err
}

type plugin struct {
	cmd       *exec.Cmd
	conn      *grpc.ClientConn
	handshake pluginapi.Handshake
	exited    chan error
}

// start launches the plugin like the host, waits for the handshake and connects.
func start(t *testing.T, bin, sockDir string, extra []string) *plugin {
	t.Helper()
	cmd := exec.Command(bin) // stopped with SIGTERM in stop, not bound to a context (that would send SIGKILL)
	cmd.Env = hostEnv(t, sockDir, []int{pluginv1.ProtocolVersion}, extra)
	cmd.Stderr = logWriter{t}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &plugin{cmd: cmd, exited: make(chan error, 1)}
	done := make(chan struct{})
	// Kill a plugin still running at the end and wait for its output to drain, so nothing logs after the test ends.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	lines := make(chan string, 1)
	go func() {
		defer close(done)
		r := bufio.NewReader(stdout)
		line, _ := r.ReadString('\n')
		lines <- line
		// Nothing may follow the handshake line on stdout (PROTOCOL.md §2.2 step 6); read until EOF.
		if rest, _ := io.ReadAll(r); len(rest) > 0 {
			t.Errorf("the plugin wrote to stdout after the handshake: %q", rest)
		}
		p.exited <- cmd.Wait()
	}()
	var line string
	select {
	case line = <-lines:
	case <-time.After(HandshakeTimeout):
		t.Fatalf("no handshake line within %v", HandshakeTimeout)
	}
	h, err := pluginapi.ParseHandshake(line)
	if err != nil {
		t.Fatalf("handshake line %q: %v", line, err)
	}
	if h.Protocol != pluginv1.ProtocolVersion || h.Network != pluginapi.NetworkUnix {
		t.Fatalf("handshake %+v; want protocol %d over unix", h, pluginv1.ProtocolVersion)
	}
	if filepath.Dir(h.Address) != filepath.Clean(sockDir) {
		t.Fatalf("socket %q is not directly inside %q", h.Address, sockDir)
	}
	p.handshake = h
	conn, err := grpc.NewClient("unix:"+h.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	p.conn = conn
	return p
}

// stop sends SIGTERM and expects a clean exit with status 0 and the socket removed (PROTOCOL.md §2.4).
func (p *plugin) stop(t *testing.T) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.exited:
		if err != nil {
			t.Errorf("exit after SIGTERM: %v; want status 0", err)
		}
	case <-time.After(ShutdownTimeout):
		t.Fatalf("the plugin did not exit within %v of SIGTERM", ShutdownTimeout)
	}
	if _, err := os.Stat(p.handshake.Address); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket %s still exists after shutdown (stat: %v)", p.handshake.Address, err)
	}
}

// logWriter forwards the plugin's stderr to the test log.
type logWriter struct{ t *testing.T }

func (w logWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(b), "\n"))
	return len(b), nil
}
