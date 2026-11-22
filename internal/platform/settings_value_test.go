package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// #207: each Settings row carries the accessor for its effective value, so
// adding a setting means its registry row and its Config field, nothing else.

// Every Settings row has a Value accessor; a row without one fails here,
// named, instead of when the startup report first runs.
func TestEverySettingHasAValueAccessor(t *testing.T) {
	for _, s := range Settings {
		if s.Value == nil {
			t.Errorf("Settings row %s has no Value accessor: add Value: func(c Config) any { return c.<Field> } to its row", s.Name)
		}
	}
}

// A row added without an accessor stops the startup report with a panic that
// names the setting and nothing else.
func TestReportNamesARowWithoutAnAccessor(t *testing.T) {
	saved := Settings
	defer func() { Settings = saved }()
	Settings = append(append([]SettingSpec(nil), saved...), SettingSpec{Name: "GOLDIE_NO_ACCESSOR", Meaning: "new"})
	defer func() {
		msg := fmt.Sprint(recover())
		if !strings.Contains(msg, "GOLDIE_NO_ACCESSOR") {
			t.Errorf("panic = %q, want it to name GOLDIE_NO_ACCESSOR", msg)
		}
	}()
	Config{}.Report(NewLogger(&syncBuffer{}, 0, "server"))
	t.Error("Report did not panic on a row without an accessor")
}

// A Secret row is redacted by its flag whatever its accessor returns: a
// string, bytes, a struct, an error or a fmt.Stringer holding the secret, or a
// slog.LogValuer that would resolve to it, never reach the startup report.
// Neither does the configuration reference, which reads no accessor.
func TestSecretRedactionDoesNotDependOnTheAccessor(t *testing.T) {
	const canary = "canary-accessor-" + canaryCore
	returns := map[string]any{
		"string":   canary,
		"bytes":    []byte(canary),
		"struct":   struct{ Token string }{canary},
		"pointer":  &struct{ Token string }{canary},
		"error":    errors.New(canary),
		"stringer": stringer(canary),
		"valuer":   valuer(canary),
		"map":      map[string]string{"token": canary},
	}
	for name, ret := range returns {
		t.Run(name, func(t *testing.T) {
			saved := Settings
			defer func() { Settings = saved }()
			rows := append([]SettingSpec(nil), saved...)
			hostile := 0
			for i := range rows {
				if rows[i].Secret {
					rows[i].Value = func(Config) any { return ret }
					hostile++
				}
			}
			if hostile == 0 {
				t.Fatal("no Settings row is marked Secret")
			}
			Settings = rows
			var buf syncBuffer
			Config{}.Report(NewLogger(&buf, 0, "server"))
			out := buf.String()
			if strings.Contains(out, canaryCore) {
				t.Fatalf("startup report leaks a Secret accessor's %s value:\n%s", name, out)
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(out), &rec); err != nil {
				t.Fatalf("startup report is not JSON: %v\n%s", err, out)
			}
			for _, s := range rows {
				if s.Secret && rec[s.Name] != "(redacted)" {
					t.Errorf("%s: report shows %v, want (redacted)", s.Name, rec[s.Name])
				}
			}
			if ref := string(ConfigReference(rows)); strings.Contains(ref, canaryCore) {
				t.Errorf("configuration reference leaks a Secret accessor's %s value", name)
			}
		})
	}
}

type stringer string

func (s stringer) String() string { return string(s) }

type valuer string

func (v valuer) LogValue() slog.Value { return slog.StringValue(string(v)) }
