package protocol

import "fmt"

// Account settings travel with the account (spec user-settings-sync). The table
// below is the one definition of what a setting may hold: the client builds its
// choices from it and the server validates every save against it.

const (
	OpSettingsGet     = "settings.get"
	OpSettingsPut     = "settings.put"
	EvSettingsUpdated = "settings.updated"
)

// Settings maps a setting key to its stored value.
type Settings map[string]string

// SettingSpec is one setting: its key, default and allowed values.
type SettingSpec struct {
	Key     string
	Default string
	Choices []string
}

func (s SettingSpec) has(v string) bool {
	for _, c := range s.Choices {
		if c == v {
			return true
		}
	}
	return false
}

// SettingColors are the colour choices of the message colour settings.
var SettingColors = []string{"default", "cyan", "blue", "green", "amber", "rose", "violet"}

// SettingSpecs lists every account setting, in display order.
var SettingSpecs = []SettingSpec{
	{"theme", "ink", []string{"ink", "paper"}},
	{"motion", "on", []string{"on", "off"}},
	{"language", "zh", []string{"zh", "en"}},
	{"bubble_width", "70", []string{"60", "70", "80"}},
	{"density", "comfortable", []string{"comfortable", "compact"}},
	{"time_format", "24h", []string{"24h", "12h"}},
	{"msg_border", "show", []string{"show", "hide"}},
	{"msg_time", "show", []string{"show", "hide"}},
	{"self_text", "default", SettingColors},
	{"other_text", "default", SettingColors},
	{"self_border", "default", SettingColors},
	{"other_border", "default", SettingColors},
}

func specFor(key string) (SettingSpec, bool) {
	for _, sp := range SettingSpecs {
		if sp.Key == key {
			return sp, true
		}
	}
	return SettingSpec{}, false
}

// Validate reports the first unknown key or value outside its choices.
func (s Settings) Validate() error {
	for k, v := range s {
		sp, ok := specFor(k)
		if !ok {
			return fmt.Errorf("unknown setting %q", k)
		}
		if !sp.has(v) {
			return fmt.Errorf("setting %q: invalid value %q", k, v)
		}
	}
	return nil
}

// Normalize returns a complete set: unknown keys and invalid values are dropped
// and every missing setting takes its default.
func (s Settings) Normalize() Settings {
	out := make(Settings, len(SettingSpecs))
	for _, sp := range SettingSpecs {
		if v, ok := s[sp.Key]; ok && sp.has(v) {
			out[sp.Key] = v
		} else {
			out[sp.Key] = sp.Default
		}
	}
	return out
}

// DefaultSettings is the complete set of defaults.
func DefaultSettings() Settings { return Settings(nil).Normalize() }

// SettingsGetRes answers settings.get. Exists is false when the account has
// never saved settings.
type SettingsGetRes struct {
	Settings Settings `json:"settings"`
	Exists   bool     `json:"exists"`
}

// SettingsPutReq carries only the settings to change.
type SettingsPutReq struct {
	Settings Settings `json:"settings"`
}

// SettingsRes answers settings.put and is the settings.updated event payload:
// the complete settings after the save.
type SettingsRes struct {
	Settings Settings `json:"settings"`
}
