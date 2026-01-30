package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

func TestNew(t *testing.T) {
	dbPath := tempDBPath(t)
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Verify file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestSaveAndGetStatus(t *testing.T) {
	s, err := New(tempDBPath(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	now := time.Now().Truncate(time.Second)
	expires := now.Add(365 * 24 * time.Hour)
	healthy := true

	status := &ServerStatus{
		EntityID:        "https://example.com",
		BaseURI:         "https://api.example.com",
		LastChecked:     &now,
		IsHealthy:       &healthy,
		ErrorMessage:    "all good",
		CertExpires:     &expires,
		CertCN:          "api.example.com",
		CertFingerprint: "abc123",
	}

	// Save
	if err := s.SaveStatus(status); err != nil {
		t.Fatalf("SaveStatus() error = %v", err)
	}

	// Get
	got, err := s.GetStatus("https://example.com", "https://api.example.com")
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetStatus() returned nil")
	}

	// Verify fields
	if got.EntityID != status.EntityID {
		t.Errorf("EntityID = %v, want %v", got.EntityID, status.EntityID)
	}
	if got.BaseURI != status.BaseURI {
		t.Errorf("BaseURI = %v, want %v", got.BaseURI, status.BaseURI)
	}
	if got.IsHealthy == nil || *got.IsHealthy != healthy {
		t.Errorf("IsHealthy = %v, want %v", got.IsHealthy, healthy)
	}
	if got.ErrorMessage != status.ErrorMessage {
		t.Errorf("ErrorMessage = %v, want %v", got.ErrorMessage, status.ErrorMessage)
	}
	if got.CertCN != status.CertCN {
		t.Errorf("CertCN = %v, want %v", got.CertCN, status.CertCN)
	}
	if got.CertFingerprint != status.CertFingerprint {
		t.Errorf("CertFingerprint = %v, want %v", got.CertFingerprint, status.CertFingerprint)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	s, err := New(tempDBPath(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	got, err := s.GetStatus("nonexistent", "nonexistent")
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if got != nil {
		t.Errorf("GetStatus() = %v, want nil for nonexistent", got)
	}
}

func TestSaveStatusWithNullFields(t *testing.T) {
	s, err := New(tempDBPath(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Save status with minimal fields (NULLs for optional fields)
	status := &ServerStatus{
		EntityID: "https://example.com",
		BaseURI:  "https://api.example.com",
		// All other fields are nil/empty
	}

	if err := s.SaveStatus(status); err != nil {
		t.Fatalf("SaveStatus() error = %v", err)
	}

	// Get it back - this is where the NULL scanning error occurs
	got, err := s.GetStatus("https://example.com", "https://api.example.com")
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetStatus() returned nil")
	}

	if got.LastChecked != nil {
		t.Errorf("LastChecked = %v, want nil", got.LastChecked)
	}
	if got.IsHealthy != nil {
		t.Errorf("IsHealthy = %v, want nil", got.IsHealthy)
	}
	if got.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %v, want empty string", got.ErrorMessage)
	}
}

func TestEnsureServerExists(t *testing.T) {
	s, err := New(tempDBPath(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Ensure server exists
	if err := s.EnsureServerExists("https://example.com", "https://api.example.com"); err != nil {
		t.Fatalf("EnsureServerExists() error = %v", err)
	}

	// Verify it was created
	got, err := s.GetStatus("https://example.com", "https://api.example.com")
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetStatus() returned nil after EnsureServerExists")
	}

	// Call again - should not error (INSERT OR IGNORE)
	if err := s.EnsureServerExists("https://example.com", "https://api.example.com"); err != nil {
		t.Fatalf("EnsureServerExists() second call error = %v", err)
	}
}

func TestGetAllStatuses(t *testing.T) {
	s, err := New(tempDBPath(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Add some servers
	s.EnsureServerExists("https://entity1.com", "https://server1.com")
	s.EnsureServerExists("https://entity1.com", "https://server2.com")
	s.EnsureServerExists("https://entity2.com", "https://server3.com")

	statuses, err := s.GetAllStatuses()
	if err != nil {
		t.Fatalf("GetAllStatuses() error = %v", err)
	}

	if len(statuses) != 3 {
		t.Errorf("GetAllStatuses() returned %d statuses, want 3", len(statuses))
	}
}

func TestGetServersNeedingCheck(t *testing.T) {
	s, err := New(tempDBPath(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Add servers - one never checked, one checked recently, one checked long ago
	s.EnsureServerExists("https://entity.com", "https://never-checked.com")

	recentTime := time.Now().Add(-1 * time.Hour)
	healthy := true
	s.SaveStatus(&ServerStatus{
		EntityID:    "https://entity.com",
		BaseURI:     "https://recent.com",
		LastChecked: &recentTime,
		IsHealthy:   &healthy,
	})

	oldTime := time.Now().Add(-10 * time.Hour)
	s.SaveStatus(&ServerStatus{
		EntityID:    "https://entity.com",
		BaseURI:     "https://old.com",
		LastChecked: &oldTime,
		IsHealthy:   &healthy,
	})

	// With 5 hour interval, should get never-checked and old, but not recent
	servers, err := s.GetServersNeedingCheck(5*time.Hour, 10)
	if err != nil {
		t.Fatalf("GetServersNeedingCheck() error = %v", err)
	}

	if len(servers) != 2 {
		t.Errorf("GetServersNeedingCheck() returned %d servers, want 2", len(servers))
	}

	// First should be never-checked (NULL last_checked comes first)
	if len(servers) > 0 && servers[0].BaseURI != "https://never-checked.com" {
		t.Errorf("First server = %v, want never-checked.com", servers[0].BaseURI)
	}
}

func TestRemoveServersNotIn(t *testing.T) {
	s, err := New(tempDBPath(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	// Add servers
	s.EnsureServerExists("https://entity.com", "https://keep1.com")
	s.EnsureServerExists("https://entity.com", "https://keep2.com")
	s.EnsureServerExists("https://entity.com", "https://remove.com")

	// Remove servers not in list
	keepList := []struct{ EntityID, BaseURI string }{
		{"https://entity.com", "https://keep1.com"},
		{"https://entity.com", "https://keep2.com"},
	}
	if err := s.RemoveServersNotIn(keepList); err != nil {
		t.Fatalf("RemoveServersNotIn() error = %v", err)
	}

	// Verify
	statuses, _ := s.GetAllStatuses()
	if len(statuses) != 2 {
		t.Errorf("After RemoveServersNotIn, got %d statuses, want 2", len(statuses))
	}

	// Removed server should be gone
	got, _ := s.GetStatus("https://entity.com", "https://remove.com")
	if got != nil {
		t.Error("Removed server still exists")
	}
}
