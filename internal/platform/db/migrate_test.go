package db

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsReadsOrderedFiles(t *testing.T) {
	migrations, err := LoadMigrations(fstest.MapFS{
		"000002_second.up.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
		"000001_first.up.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("unexpected order: %+v", migrations)
	}
	if migrations[0].Checksum == "" || migrations[0].Checksum == migrations[1].Checksum {
		t.Fatalf("unexpected checksums: %+v", migrations)
	}
}

func TestLoadMigrationsRejectsDuplicateVersion(t *testing.T) {
	_, err := LoadMigrations(fstest.MapFS{
		"000001_first.up.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000001_second.up.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
	})
	if err == nil {
		t.Fatal("expected duplicate version error")
	}
}

func TestLoadMigrationsIgnoresAppleDoubleMetadata(t *testing.T) {
	migrations, err := LoadMigrations(fstest.MapFS{
		"000001_first.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"._000001_first.up.sql": &fstest.MapFile{Data: []byte("macOS metadata")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 || migrations[0].Version != 1 {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
}

func TestPendingMigrationsRejectsChangedAppliedFile(t *testing.T) {
	migrations, err := LoadMigrations(fstest.MapFS{
		"000001_first.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PendingMigrations(migrations, map[int64]string{1: strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestPendingMigrationsRejectsRetroactiveVersion(t *testing.T) {
	migrations, err := LoadMigrations(fstest.MapFS{
		"000001_first.up.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000002_second.up.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PendingMigrations(migrations, map[int64]string{2: migrations[1].Checksum})
	if err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("expected retroactive migration error, got %v", err)
	}
}

func TestRepositoryMigrationLoads(t *testing.T) {
	migrations, err := LoadMigrations(os.DirFS("../../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 8 || migrations[0].Version != 1 || migrations[1].Version != 2 || migrations[2].Version != 3 || migrations[3].Version != 4 || migrations[4].Version != 5 || migrations[5].Version != 6 || migrations[6].Version != 7 || migrations[7].Version != 8 {
		t.Fatalf("expected proxy access migration after Agent presence; found %d migrations", len(migrations))
	}
}
