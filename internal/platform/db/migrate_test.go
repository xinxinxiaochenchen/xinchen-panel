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
	if len(migrations) != 21 {
		t.Fatalf("expected relay certificate migration 21; found %d migrations", len(migrations))
	}
	for i, migration := range migrations {
		if migration.Version != int64(i+1) {
			t.Fatalf("expected contiguous migration version %d, got %d", i+1, migration.Version)
		}
	}
}

func TestProxyAccessMigrationGrantsUserPermissions(t *testing.T) {
	contents, err := os.ReadFile("../../../migrations/000008_proxy_accesses.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"proxy_accesses.read", "proxy_accesses.write"} {
		if !strings.Contains(string(contents), code) {
			t.Fatalf("migration 8 does not grant %s", code)
		}
	}
}
