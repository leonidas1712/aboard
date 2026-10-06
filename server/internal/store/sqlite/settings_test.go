package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

// A setting is stored once: a later value is ignored and the first one returned, so two
// servers starting at once on one database keep one digest key.
func TestASettingIsStoredOnce(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "aboard.db"), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if v, err := st.SettingOnce(ctx, "digest_key", "first"); err != nil || v != "first" {
		t.Fatalf("first store: %q %v", v, err)
	}
	if v, err := st.SettingOnce(ctx, "digest_key", "second"); err != nil || v != "first" {
		t.Fatalf("second store: %q %v", v, err)
	}
	if v, ok, err := st.Setting(ctx, "digest_key"); err != nil || !ok || v != "first" {
		t.Fatalf("read back: %q %v %v", v, ok, err)
	}
}
