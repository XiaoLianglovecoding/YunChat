package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDirOrdersAndChecksumsMigrations(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "010_last.sql", "SELECT 10;")
	writeMigration(t, dir, "001_first.sql", "SELECT 1;")
	migrations, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].Version != 1 || migrations[1].Version != 10 {
		t.Fatalf("unexpected order: %+v", migrations)
	}
	if len(migrations[0].Checksum) != 64 {
		t.Fatalf("checksum = %q", migrations[0].Checksum)
	}
}

func TestLoadDirRejectsDuplicateVersion(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "001_first.sql", "SELECT 1;")
	writeMigration(t, dir, "001_again.sql", "SELECT 2;")
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("expected duplicate version error")
	}
}

func TestLoadDirRejectsEmptyMigration(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "001_empty.sql", "  \n")
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("expected empty migration error")
	}
}

func writeMigration(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
