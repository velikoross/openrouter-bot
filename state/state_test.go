package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "state.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Model() != "" || s.EffectiveModel("cfg") != "cfg" {
		t.Fatal("empty store should use the fallback")
	}
	for _, m := range []string{"a", "b", "a", "c"} {
		if err := s.SetModel(m); err != nil {
			t.Fatal(err)
		}
	}
	if s.EffectiveModel("cfg") != "c" {
		t.Errorf("effective = %q", s.EffectiveModel("cfg"))
	}
	if got := s.Recent(); len(got) != 3 || got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Errorf("recent = %v", got)
	}

	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Model() != "c" || len(s2.Recent()) != 3 {
		t.Errorf("not persisted: %q %v", s2.Model(), s2.Recent())
	}

	if err := s2.ResetModel(); err != nil {
		t.Fatal(err)
	}
	s3, _ := Open(p)
	if s3.Model() != "" || len(s3.Recent()) != 3 {
		t.Errorf("reset should keep recents: %q %v", s3.Model(), s3.Recent())
	}
}

func TestRecentIsBounded(t *testing.T) {
	s, _ := Open("")
	for i := 0; i < MaxRecent+5; i++ {
		_ = s.SetModel(string(rune('a' + i)))
	}
	if len(s.Recent()) != MaxRecent {
		t.Errorf("len = %d", len(s.Recent()))
	}
}

func TestCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	_ = os.WriteFile(p, []byte("{nope"), 0o644)
	if _, err := Open(p); err == nil {
		t.Error("expected an error for a corrupt file")
	}
}
