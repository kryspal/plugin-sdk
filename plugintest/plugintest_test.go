package plugintest_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kryspal/plugin-sdk/plugintest"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// The suite passes against the quickstart plugin (examples/minimal), built on the spot.
func TestRunAgainstTheMinimalExample(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not in PATH")
	}
	bin := filepath.Join(t.TempDir(), "minimal")
	build := exec.CommandContext(t.Context(), goBin, "build", "-o", bin, "../examples/minimal")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	plugintest.Run(t, bin, plugintest.Options{
		Metadata: &plugintest.MetadataCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Big Buck Bunny", Year: 2008},
	})
}
