package models

import "testing"

func TestDefaultAppPreferencesHideLeviathan(t *testing.T) {
	prefs := DefaultAppPreferences()
	if prefs.LeviathanEnabled {
		t.Fatal("LeviathanEnabled = true, want false by default")
	}
}
