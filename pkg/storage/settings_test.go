package storage

import (
	"os"
	"testing"
	"time"
)

// storeDir is a temp dir removed with retries: on Windows a file the store
// has just written can briefly refuse deletion.
func storeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "linkchat-settings-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 20; i++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	return dir
}

func TestUserSettingsMissingThenMerged(t *testing.T) {
	st, err := Open(storeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	got, ok, err := st.UserSettings("u1")
	if err != nil || ok || len(got) != 0 {
		t.Fatalf("never saved: %v %v %v, want empty, false", got, ok, err)
	}

	if _, err := st.MergeUserSettings("u1", map[string]string{"theme": "paper", "density": "compact"}); err != nil {
		t.Fatal(err)
	}
	merged, err := st.MergeUserSettings("u1", map[string]string{"theme": "ink"})
	if err != nil {
		t.Fatal(err)
	}
	if merged["theme"] != "ink" || merged["density"] != "compact" {
		t.Fatalf("merge lost an untouched key or kept the old value: %v", merged)
	}

	// Another user's settings are separate.
	if other, ok, _ := st.UserSettings("u2"); ok || len(other) != 0 {
		t.Fatalf("u2 sees settings: %v", other)
	}

	// The returned map is a copy: changing it does not change the store.
	merged["theme"] = "x"
	if again, _, _ := st.UserSettings("u1"); again["theme"] != "ink" {
		t.Fatal("the caller's map aliases the store")
	}
}

func TestUserSettingsSurviveReopen(t *testing.T) {
	dir := storeDir(t)
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MergeUserSettings("u1", map[string]string{"language": "en"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got, ok, err := again.UserSettings("u1")
	if err != nil || !ok || got["language"] != "en" {
		t.Fatalf("after reopen: %v %v %v", got, ok, err)
	}
}
