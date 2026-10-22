package plugintest

import (
	"context"
	"slices"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// LibraryCase checks one per-library setting (PROTOCOL.md §5.4). The plugin declares the setting with
// ConfigField.library_override; the author gives two values that make an answer differ and a function that makes one request.
//
//	plugintest.LibraryCase{
//		Name: "title language", Key: "title_language", A: "romaji", B: "english",
//		Call: func(ctx context.Context, conn *grpc.ClientConn, lib *pluginv1.LibraryContext) (string, string, error) {
//			res, err := pluginv1.NewMetadataProviderClient(conn).Search(ctx, &pluginv1.SearchRequest{
//				Type: pluginv1.ItemType_ITEM_TYPE_SERIES, Name: "Sample", Library: lib})
//			if err != nil || len(res.GetCandidates()) == 0 {
//				return "", "", err
//			}
//			return res.GetCandidates()[0].GetName(), "", nil
//		},
//	}
type LibraryCase struct {
	Name string
	// Key is the overridable setting, and A and B are two values the plugin answers differently for.
	Key, A, B string
	// Call makes one request for lib (nil means a request without a library) and returns what depends on the setting,
	// for example the title the plugin chose or the protojson of the answer, and, when the request is a recognition,
	// the parser_version of the answer (empty otherwise). A and B must differ in the result, or in the version. It must
	// not depend on anything else that changes between calls.
	Call func(ctx context.Context, conn *grpc.ClientConn, lib *pluginv1.LibraryContext) (result, version string, err error)
	// NoEffect marks a case where A and B legitimately give the same answer; the check that they differ is skipped.
	NoEffect bool
}

const (
	libraryWorkers = 32
	libraryRounds  = 5
	unknownKey     = "plugintest_unknown_setting"
)

// libraryChecks runs every case. config is the global configuration the plugin was configured with.
func libraryChecks(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, config map[string]string, cases []LibraryCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if f := fieldByKey(m, c.Key); f == nil || !f.GetLibraryOverride() {
				t.Fatalf("LibraryCase key %q is not a config field marked library_override", c.Key)
			}
			if c.Call == nil || c.A == c.B {
				t.Fatal("LibraryCase needs Call and two different values A and B")
			}
			libraryCase(ctx, t, conn, m, config, c)
		})
	}
}

type answer struct{ result, version string }

func libraryCase(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, config map[string]string, c LibraryCase) {
	t.Helper()
	lib := func(id string, overrides map[string]string) *pluginv1.LibraryContext {
		return &pluginv1.LibraryContext{LibraryId: id, Overrides: overrides}
	}
	libA, libB := lib("plugintest-a", map[string]string{c.Key: c.A}), lib("plugintest-b", map[string]string{c.Key: c.B})
	ask := func(l *pluginv1.LibraryContext) (answer, error) {
		cctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		defer cancel()
		r, v, err := c.Call(cctx, conn, l)
		return answer{r, v}, err
	}
	must := func(what string, l *pluginv1.LibraryContext) answer {
		a, err := ask(l)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return a
	}

	// 1. Baselines, one at a time.
	global, a, b := must("a request without a library", nil), must("a request with A", libA), must("a request with B", libB)
	if !c.NoEffect {
		if a == b {
			t.Errorf("A (%q) and B (%q) give the same answer %v: the setting has no effect, or the plugin ignores the library's overrides", c.A, c.B, a)
		}
		// A recognizer that reports a parser_version must change it with the setting, or the host would not re-run the library's items.
		if a.version != "" && a.version == b.version && a.result != b.result {
			t.Errorf("A and B give different answers but the same parser_version %q: the host would not re-run the library's items", a.version)
		}
	}
	if again := must("a request with A, again", libA); again != a {
		t.Errorf("the same request with A answered %v, then %v: answers must be deterministic", a, again)
	}

	// 2. Parallel requests for different libraries, plus plain ones. Each must get its own answer; a plugin that keeps
	// the effective settings in its own state, or a cache whose key leaves the overrides out, mixes them up.
	want := []struct {
		name string
		lib  *pluginv1.LibraryContext
		ans  answer
	}{{"no library", nil, global}, {"A", libA, a}, {"B", libB, b}}
	var wg sync.WaitGroup
	for w := range libraryWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range libraryRounds {
				k := (w + r) % len(want)
				got, err := ask(want[k].lib)
				if err != nil {
					t.Errorf("parallel request (%s): %v", want[k].name, err)
					return
				}
				if got != want[k].ans {
					t.Errorf("parallel request (%s) answered %v, want %v: the plugin mixes up the settings of requests that run at once", want[k].name, got, want[k].ans)
					return
				}
			}
		}()
	}
	wg.Wait()
	if after := must("a request without a library, after the others", nil); after != global {
		t.Errorf("a request without a library answered %v before and %v after requests with overrides: the plugin keeps a library's values", global, after)
	}

	// 3. Values the plugin must live with: never an error, and an unknown key changes nothing.
	for _, tc := range []struct {
		name string
		lib  *pluginv1.LibraryContext
		same bool // must equal the answer without a library
	}{
		{"an unknown key", lib("plugintest-u", map[string]string{unknownKey: "x"}), true},
		{"no overrides", lib("plugintest-n", nil), true},
		{"an empty value", lib("plugintest-e", map[string]string{c.Key: ""}), false},
		{"a value that cannot be right", lib("plugintest-j", map[string]string{c.Key: "\x00junk"}), false},
		{"no library id", lib("", map[string]string{c.Key: c.A}), false},
	} {
		got, err := ask(tc.lib)
		if err != nil {
			t.Errorf("a request with %s failed: %v; it must be answered, falling back to the global value", tc.name, err)
		} else if tc.same && got != global {
			t.Errorf("a request with %s answered %v, want the global answer %v", tc.name, got, global)
		}
	}

	// 4. A dry run checks and applies nothing.
	cctx, cancel := context.WithTimeout(ctx, configureTimeout)
	defer cancel()
	resp, err := pluginv1.NewPluginClient(conn).Configure(cctx, &pluginv1.ConfigureRequest{Values: config, Library: libB, DryRun: true})
	if err != nil && status.Code(err) != codes.Unimplemented {
		t.Errorf("Configure with dry_run: %v; answer it, or return UNIMPLEMENTED", err)
	} else if err == nil && (resp.GetError() != "" || len(resp.GetFieldErrors()) > 0) {
		t.Errorf("Configure with dry_run rejected a value from its own test case: %q %v", resp.GetError(), resp.GetFieldErrors())
	}
	if after := must("a request without a library, after a dry run", nil); after != global {
		t.Errorf("a dry run changed what later requests see: %v, then %v", global, after)
	}
}

// hasLibraryOverride reports whether the manifest marks any field library_override.
func hasLibraryOverride(m *pluginv1.Manifest) bool {
	return slices.ContainsFunc(m.GetConfigFields(), func(f *pluginv1.ConfigField) bool { return f.GetLibraryOverride() })
}
