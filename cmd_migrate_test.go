package main

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikaelstaldal/go-server-common/sqlite"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the real main dispatch and exit status in a separate process.
func TestMigrationCLIHelper(t *testing.T) {
	if os.Getenv("MYMAIL_MIGRATION_CLI_HELPER") != "1" {
		return
	}
	os.Args = append([]string{"mymail"}, flag.Args()...)
	flag.CommandLine = flag.NewFlagSet("mymail", flag.ExitOnError)
	main()
	os.Exit(0)
}

func migrationCLI(t *testing.T, dataDir string) (string, error) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestMigrationCLIHelper$", "--",
		"-migrate", "-data", dataDir,
		"-sendmail", filepath.Join(dataDir, "missing-sendmail"),
		"-basic-auth-file", filepath.Join(dataDir, "missing-htpasswd"),
		"-port", "0", "-lda-socket", filepath.Join(dataDir, "lda.sock"))
	cmd.Env = append(os.Environ(), "MYMAIL_MIGRATION_CLI_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), "migration command must exit without starting a server")
	return string(output), err
}

func TestMigrationCLI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mymail.sqlite")
	// An existing version-zero SQLite file has every migration pending. Unlike
	// -init, migrating it must not seed folders or require/create an identity.
	db, err := sqlite.Open(path, 0, nil)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE migration_marker(value TEXT); INSERT INTO migration_marker VALUES('keep')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	for _, name := range []string{"pending", "already current"} {
		t.Run(name, func(t *testing.T) {
			freeBefore := migrationFreeSpace(t, path)
			output, err := migrationCLI(t, dir)
			require.NoError(t, err, output)
			assert.Empty(t, output)
			// Open without migration so this check cannot repair a broken CLI.
			db, err := sqlite.Open(path, 0, nil)
			require.NoError(t, err)
			defer db.Close()
			var version, folders, identities int
			var marker string
			require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&version))
			assert.Positive(t, version)
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM folders`).Scan(&folders))
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM identities`).Scan(&identities))
			assert.Zero(t, folders)
			assert.Zero(t, identities)
			require.NoError(t, db.QueryRow(`SELECT value FROM migration_marker`).Scan(&marker))
			assert.Equal(t, "keep", marker)
			// Check that the CLI reached the current version, not just v1.
			require.NoError(t, repository.InitSchema(db))
			var current int
			require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&current))
			assert.Equal(t, version, current)
			var freeAfter int
			require.NoError(t, db.QueryRow(`PRAGMA freelist_count`).Scan(&freeAfter))
			if name == "pending" {
				assert.Zero(t, freeAfter, "pending migrations must be followed by VACUUM")
			} else {
				assert.Equal(t, freeBefore, freeAfter, "an already current database must not be vacuumed")
			}
		})
	}
	_, err = os.Stat(filepath.Join(dir, "lda.sock"))
	assert.True(t, os.IsNotExist(err), "migration must not create an LDA socket")
}

// Leave reusable pages in the file so tests observe actual compaction rather
// than merely asserting that a VACUUM statement was executed.
func migrationFreeSpace(t *testing.T, path string) int {
	t.Helper()
	db, err := sqlite.Open(path, 0, nil)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE discarded(payload BLOB);
		INSERT INTO discarded VALUES(zeroblob(1048576)); DROP TABLE discarded`)
	require.NoError(t, err)
	var free int
	require.NoError(t, db.QueryRow(`PRAGMA freelist_count`).Scan(&free))
	require.Positive(t, free)
	return free
}

func TestAutomaticMigrationDoesNotVacuum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mymail.sqlite")
	migrationFreeSpace(t, path)
	// This is the same migration entry point used by normal server startup.
	db, err := repository.OpenDB(path, 0)
	require.NoError(t, err)
	defer db.Close()
	var version, free int
	require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&version))
	assert.Positive(t, version)
	require.NoError(t, db.QueryRow(`PRAGMA freelist_count`).Scan(&free))
	assert.Positive(t, free, "automatic migrations must leave free pages for reuse")
}

func TestMigrationCLIErrors(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt", "newer schema", "failed migration"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "mymail.sqlite")
			switch kind {
			case "corrupt":
				require.NoError(t, os.WriteFile(path, []byte("not a SQLite database"), 0o600))
			case "newer schema", "failed migration":
				db, err := sqlite.Open(path, 0, nil)
				require.NoError(t, err)
				if kind == "newer schema" {
					_, err = db.Exec(`PRAGMA user_version=2147483647`)
				} else {
					_, err = db.Exec(`CREATE TABLE messages(incompatible TEXT)`)
				}
				require.NoError(t, err)
				require.NoError(t, db.Close())
			}
			output, err := migrationCLI(t, dir)
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr, output)
			assert.Equal(t, 1, exitErr.ExitCode())
			assert.Contains(t, output, "error:")
			if kind == "missing" {
				_, err := os.Stat(path)
				assert.True(t, os.IsNotExist(err), "missing database must not be created")
			}
		})
	}
}
