package hanconv

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// conversions is every supported conversion, sorted.
func conversions() []string {
	names := make([]string, 0, len(chains))
	for n := range chains {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// corpus is the deterministic test input: every key and value of every embedded dictionary, then 20,000 strings
// that join two to four of them with ASCII text and punctuation in between (so matches run across entry
// boundaries). It only depends on the embedded dictionaries.
func corpus(t testing.TB) []string {
	t.Helper()
	seen := map[string]bool{}
	var words []string
	files, err := dictionaries.ReadDir("dictionary")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		r, err := dictionaries.Open("dictionary/" + f.Name())
		if err != nil {
			t.Fatal(err)
		}
		zr, err := gzip.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(zr)
		for sc.Scan() {
			for _, item := range strings.FieldsFunc(sc.Text(), func(r rune) bool { return r == '\t' || r == ' ' }) {
				if !seen[item] {
					seen[item] = true
					words = append(words, item)
				}
			}
		}
		_ = r.Close()
	}
	slices.Sort(words)
	out := slices.Clone(words)
	seps := []string{"", "", "", " ", "：", "（2024）", "-", "《", "》", "S01E02", "，", "abc"}
	rng := rand.New(rand.NewPCG(2026, 10))
	for range 20000 {
		var b strings.Builder
		for j := range 2 + rng.IntN(3) {
			if j > 0 {
				b.WriteString(seps[rng.IntN(len(seps))])
			}
			b.WriteString(words[rng.IntN(len(words))])
		}
		out = append(out, b.String())
	}
	return out
}

// TestGolden checks every conversion against testdata/golden-hashes.txt, generated with github.com/liuzl/gocc on
// the same corpus: SHA-256 over "input\toutput\n" for the whole corpus in order. testdata/golden-sample.tsv.gz
// holds every 250th line in readable form (conversion, input, gocc's output) to show what differs.
func TestGolden(t *testing.T) {
	t.Parallel()
	in := corpus(t)
	want := map[string]string{}
	raw, err := os.ReadFile("testdata/golden-hashes.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("golden-hashes.txt: bad line %q", line)
		}
		if n, _ := strconv.Atoi(f[2]); n != len(in) {
			t.Fatalf("golden-hashes.txt: %s covers %s inputs, the corpus has %d (dictionaries changed?)", f[0], f[2], len(in))
		}
		want[f[0]] = f[1]
	}
	sample := readSample(t)
	for _, name := range conversions() {
		c, err := New(name)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		for _, s := range in {
			fmt.Fprintf(h, "%s\t%s\n", s, c.Convert(s))
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want[name] {
			bad := 0
			for _, row := range sample[name] {
				if got := c.Convert(row[0]); got != row[1] && bad < 20 {
					t.Errorf("%s(%q) = %q, gocc gave %q", name, row[0], got, row[1])
					bad++
				}
			}
			t.Errorf("%s: corpus hash %s, want %s (%d sampled differences shown)", name, got, want[name], bad)
		}
	}
}

func readSample(t *testing.T) map[string][][2]string {
	t.Helper()
	f, err := os.Open("testdata/golden-sample.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][][2]string{}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			t.Fatalf("golden-sample: bad line %q", sc.Text())
		}
		out[parts[0]] = append(out[parts[0]], [2]string{parts[1], parts[2]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExamples(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ conv, in, want string }{
		{"s2t", "头发", "頭髮"},
		{"s2t", "发展", "發展"},
		{"t2s", "頭髮與發展", "头发与发展"},
		{"s2twp", "软件和鼠标", "軟體和滑鼠"},
		{"tw2sp", "軟體和滑鼠", "软件和鼠标"},
		{"s2t", "Season 1：第一季", "Season 1：第一季"},
		{"t2s", "", ""},
	} {
		cv, err := New(c.conv)
		if err != nil {
			t.Fatal(err)
		}
		if got := cv.Convert(c.in); got != c.want {
			t.Errorf("%s(%q) = %q, want %q", c.conv, c.in, got, c.want)
		}
	}
}

// TestInvalidUTF8: an invalid byte becomes U+FFFD and the next bytes are kept. gocc advanced three bytes after
// an invalid byte and dropped what followed; this package does not reproduce that.
func TestInvalidUTF8(t *testing.T) {
	t.Parallel()
	c, err := New("t2s")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Convert("a\xffbc發"); got != "a�bc发" {
		t.Fatalf("Convert(invalid) = %q", got)
	}
}

func TestUnknownConversion(t *testing.T) {
	t.Parallel()
	if _, err := New("s2x"); err == nil {
		t.Fatal("New(s2x) succeeded")
	}
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			c, err := New("s2hk")
			if err != nil {
				t.Error(err)
				return
			}
			for range 200 {
				if got := c.Convert("头发"); got != "頭髮" {
					t.Errorf("Convert = %q", got)
					return
				}
			}
		})
	}
	wg.Wait()
}
