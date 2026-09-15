package v1

import (
	"testing"
)

func TestValkeyVersionFromAiven(t *testing.T) {
	tests := []struct {
		name     string
		reported string
		want     ValkeyVersion
		wantOK   bool
	}{
		{name: "exact major.minor", reported: "9.1", want: ValkeyVersionV9_1, wantOK: true},
		{name: "patch version reported by Aiven", reported: "9.1.2", want: ValkeyVersionV9_1, wantOK: true},
		{name: "deprecated version still resolves", reported: "9.0.0", want: ValkeyVersionV9_0, wantOK: true},
		{name: "oldest supported version", reported: "8.1.4", want: ValkeyVersionV8_1, wantOK: true},
		{name: "unsupported major", reported: "7.2.5"},
		{name: "unsupported minor", reported: "9.2.0"},
		{name: "not a dot boundary", reported: "9.10.0"},
		{name: "empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ValkeyVersionFromAiven(tt.reported)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("version = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValkeyVersionValidateNewInstance(t *testing.T) {
	tests := []struct {
		name      string
		version   ValkeyVersion
		wantError string
	}{
		{name: "newest version", version: ValkeyVersionV9_1},
		{name: "oldest creatable version", version: ValkeyVersionV8_1},
		{name: "withdrawn from new instances", version: ValkeyVersionV9_0, wantError: "Valkey 9.0 is no longer available for new instances"},
		{name: "unset", wantError: "spec.version is required"},
		{name: "unknown", version: ValkeyVersion("7.2"), wantError: `unknown Valkey version: "7.2"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.version.ValidateNewInstance()
			if tt.wantError == "" {
				requireNoError(t, err)
				return
			}
			requireErrorEqual(t, err, tt.wantError)
		})
	}
}

func TestValkeyVersionResolve(t *testing.T) {
	t.Run("falls back to the version Aiven reports as running", func(t *testing.T) {
		got, err := ValkeyVersion("").Resolve("8.1.4")
		requireNoError(t, err)
		if got != ValkeyVersionV8_1 {
			t.Errorf("resolved version = %q, want %q", got, ValkeyVersionV8_1)
		}
	})

	// The first reconcile of a new instance: admission required a version, and no service exists yet
	// for Aiven to report on.
	t.Run("keeps the spec when Aiven reports nothing", func(t *testing.T) {
		got, err := ValkeyVersionV9_1.Resolve("")
		requireNoError(t, err)
		if got != ValkeyVersionV9_1 {
			t.Errorf("resolved version = %q, want %q", got, ValkeyVersionV9_1)
		}
	})

	t.Run("keeps the spec once Aiven runs the same version", func(t *testing.T) {
		got, err := ValkeyVersionV9_1.Resolve("9.1.2")
		requireNoError(t, err)
		if got != ValkeyVersionV9_1 {
			t.Errorf("resolved version = %q, want %q", got, ValkeyVersionV9_1)
		}
	})

	t.Run("adopts a running version newer than the pin", func(t *testing.T) {
		got, err := ValkeyVersionV9_0.Resolve("9.1.2")
		requireNoError(t, err)
		if got != ValkeyVersionV9_1 {
			t.Errorf("resolved version = %q, want %q", got, ValkeyVersionV9_1)
		}
	})

	t.Run("errors when nothing is set and Aiven reports nothing", func(t *testing.T) {
		_, err := ValkeyVersion("").Resolve("")
		requireErrorContains(t, err, "spec.version is unset")
	})

	t.Run("errors when Aiven reports an unsupported version", func(t *testing.T) {
		_, err := ValkeyVersion("").Resolve("7.2.5")
		requireErrorEqual(t, err, `unsupported Valkey version "7.2.5" reported by Aiven`)
	})

	t.Run("errors on a spec version we cannot name when Aiven reports nothing", func(t *testing.T) {
		_, err := ValkeyVersion("7.2").Resolve("")
		requireErrorEqual(t, err, `unsupported Valkey version "7.2" in spec.version`)
	})

	// Without a version we can name, there is no way to tell whether the spec would downgrade the
	// running service, so the pin is not trusted either.
	t.Run("errors on an unsupported running version even when pinned", func(t *testing.T) {
		_, err := ValkeyVersionV9_1.Resolve("10.0.1")
		requireErrorEqual(t, err, `unsupported Valkey version "10.0.1" reported by Aiven`)
	})

	// Adopting 9.0 would write a spec that validateUpdate rejects, leaving Aiven configured and the
	// object unable to record it.
	t.Run("errors when the running version is newer but not a legal step from the spec", func(t *testing.T) {
		_, err := ValkeyVersionV8_1.Resolve("9.0.4")
		requireErrorContains(t, err, "cannot adopt the running version 9.0")
	})

	// Admission cannot catch this: setting a version for the first time has no prior version to
	// step from, and the webhook cannot see what Aiven is running.
	t.Run("errors when the spec requests a step the upgrade table forbids", func(t *testing.T) {
		_, err := ValkeyVersionV9_0.Resolve("8.1.4")
		requireErrorContains(t, err, "spec.version cannot be applied to an instance running 8.1")
	})

	t.Run("keeps the spec while an upgrade is still pending at Aiven", func(t *testing.T) {
		got, err := ValkeyVersionV9_1.Resolve("8.1.4")
		requireNoError(t, err)
		if got != ValkeyVersionV9_1 {
			t.Errorf("resolved version = %q, want %q", got, ValkeyVersionV9_1)
		}
	})
}
