package protocol

import "testing"

func TestSettingsValidate(t *testing.T) {
	if err := (Settings{"theme": "paper", "bubble_width": "80"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Settings{{"font_size": "1"}, {"bubble_width": "55"}, {"theme": ""}, {"self_border": "crimson"}} {
		if bad.Validate() == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestSettingsNormalize(t *testing.T) {
	n := (Settings{"theme": "paper", "density": "roomy", "x": "y"}).Normalize()
	if n["theme"] != "paper" || n["density"] != "comfortable" {
		t.Fatalf("normalize gave %v", n)
	}
	if _, ok := n["x"]; ok || len(n) != len(SettingSpecs) {
		t.Fatalf("normalize kept unknown keys or lost some: %v", n)
	}
}

func TestEveryDefaultIsAChoice(t *testing.T) {
	seen := map[string]bool{}
	for _, sp := range SettingSpecs {
		if seen[sp.Key] {
			t.Errorf("key %q listed twice", sp.Key)
		}
		seen[sp.Key] = true
		if !sp.has(sp.Default) {
			t.Errorf("%s: default %q is not a choice", sp.Key, sp.Default)
		}
	}
	if err := DefaultSettings().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWheelKeySetting(t *testing.T) {
	for _, k := range WheelKeys {
		if err := (Settings{"wheel_key": k}).Validate(); err != nil {
			t.Errorf("%s rejected: %v", k, err)
		}
	}
	for _, bad := range []string{"ctrl", "alt_space", ""} {
		if (Settings{"wheel_key": bad}).Validate() == nil {
			t.Errorf("accepted wheel_key %q", bad)
		}
	}
	if DefaultSettings()["wheel_key"] != "f1" {
		t.Fatal("default wheel key is not f1")
	}
}
