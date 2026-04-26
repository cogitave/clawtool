package governance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestLicense_Registered(t *testing.T) {
	r := setup.Lookup("license")
	if r == nil {
		t.Fatal("license recipe should self-register via init()")
	}
	if r.Meta().Category != setup.CategoryGovernance {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set (wrap-don't-reinvent enforcement)")
	}
}

func TestLicense_DetectAbsent(t *testing.T) {
	r := setup.Lookup("license")
	dir := t.TempDir()
	status, _, err := r.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want %q", status, setup.StatusAbsent)
	}
}

func TestLicense_ApplyThenVerify(t *testing.T) {
	r := setup.Lookup("license")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, setup.Options{"holder": "Test Holder"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(dir, "LICENSE"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(written, licenseMarker) {
		t.Error("written file lacks licenseMarker")
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusApplied {
		t.Errorf("after Apply, Detect = %q, want %q", status, setup.StatusApplied)
	}
}

func TestLicense_HolderAndYearSubstituted(t *testing.T) {
	r := setup.Lookup("license")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, setup.Options{
		"holder": "Test Holder",
		"year":   2027,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(dir, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(written)
	if !strings.Contains(body, "Test Holder") {
		t.Error("holder not substituted into LICENSE")
	}
	if !strings.Contains(body, "2027") {
		t.Error("year not substituted into LICENSE")
	}
	if strings.Contains(body, "{{ holder }}") || strings.Contains(body, "{{ year }}") {
		t.Error("template placeholders leaked into output")
	}
}

func TestLicense_RejectsUnsupportedSPDX(t *testing.T) {
	r := setup.Lookup("license")
	err := r.Apply(context.Background(), t.TempDir(), setup.Options{
		"holder": "Test",
		"spdx":   "GPL-3.0",
	})
	if err == nil {
		t.Fatal("Apply should reject unsupported SPDX id")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention 'unsupported': %v", err)
	}
}

func TestLicense_AGPL3SubstitutesAndKeepsCanonicalText(t *testing.T) {
	r := setup.Lookup("license")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, setup.Options{
		"holder": "Acme Inc.",
		"spdx":   "AGPL-3.0",
		"year":   2026,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "LICENSE"))
	s := string(body)
	if !strings.Contains(s, "Copyright (C) 2026 Acme Inc.") {
		t.Errorf("AGPL-3.0 holder/year prefix missing or unsubstituted")
	}
	// Canonical body must round-trip — these phrases are unique to
	// the official AGPL-3.0 text, so their presence is a fingerprint.
	for _, marker := range []string{
		"GNU AFFERO GENERAL PUBLIC LICENSE",
		"Version 3, 19 November 2007",
		"END OF TERMS AND CONDITIONS",
	} {
		if !strings.Contains(s, marker) {
			t.Errorf("AGPL canonical phrase %q missing — embedded asset corrupted?", marker)
		}
	}
}

func TestLicense_RequiresHolder(t *testing.T) {
	r := setup.Lookup("license")
	err := r.Apply(context.Background(), t.TempDir(), setup.Options{})
	if err == nil {
		t.Fatal("Apply should require holder option")
	}
}

func TestLicense_RefusesOverwriteOfUnmanagedFile(t *testing.T) {
	r := setup.Lookup("license")
	dir := t.TempDir()
	target := filepath.Join(dir, "LICENSE")
	if err := os.WriteFile(target, []byte("MIT License\n\nCopyright (c) 2020 Someone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, setup.Options{"holder": "Test"}); err == nil {
		t.Fatal("Apply should refuse to overwrite an unmanaged LICENSE")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged existing file should detect as Partial; got %q", status)
	}
}

func TestLicense_ForceOverridesUnmanagedRefusal(t *testing.T) {
	r := setup.Lookup("license")
	dir := t.TempDir()
	target := filepath.Join(dir, "LICENSE")
	if err := os.WriteFile(target, []byte("Some user-authored license\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without force, Apply must refuse (covered by the test above).
	// With force, it must succeed and the rendered file becomes
	// clawtool-managed (marker present).
	if err := r.Apply(context.Background(), dir, setup.Options{
		"holder": "Test",
		"force":  true,
	}); err != nil {
		t.Fatalf("Apply with force should succeed; got %v", err)
	}
	body, _ := os.ReadFile(target)
	if !setup.HasMarker(body, licenseMarker) {
		t.Error("force-overwritten file lacks licenseMarker")
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify after force-overwrite: %v", err)
	}
}

func TestLicense_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("license")
	dir := t.TempDir()
	opts := setup.Options{"holder": "Test"}
	if err := r.Apply(context.Background(), dir, opts); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, opts); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}

func TestLicense_VerifyFailsBeforeApply(t *testing.T) {
	r := setup.Lookup("license")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when file is missing")
	}
}
