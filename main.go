package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mikaelstaldal/mymail/internal/lda"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/mikaelstaldal/mymail/web"
)

func main() {
	initMode := flag.Bool("init", false, "Initialize database and exit")
	importMode := flag.Bool("import", false, "Import messages from mbox/Maildir and exit")
	port := flag.Int("port", 8080, "HTTP listen port (1-65535)")
	addr := flag.String("addr", "", "Bind address")
	dataDir := flag.String("data", "data/", "Data directory")
	flag.String("basic-auth-file", "", "Path to htpasswd file")
	flag.String("basic-auth-realm", "mymail", "Auth realm")
	identityAddress := flag.String("identity-address", "", "Initial identity email address (used with -init)")
	identityName := flag.String("identity-name", "", "Initial identity display name (used with -init)")
	flag.Parse()

	if *initMode {
		runInit(*dataDir, *identityAddress, *identityName)
		return
	}

	if *importMode {
		runImport(*dataDir, flag.Args())
		return
	}

	staticFS := http.FS(web.Static)
	mux := http.NewServeMux()
	mux.Handle("/static/", http.FileServer(staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/static/index.html", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	listenAddr := fmt.Sprintf("%s:%d", *addr, *port)
	log.Printf("mymail listening on %s", listenAddr)
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}

func runImport(dataDir string, mappingArgs []string) {
	dbPath := filepath.Join(dataDir, "mymail.sqlite")
	if err := repository.CheckDBExists(dbPath); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}

	lockFile, err := lda.AcquireImportLock(dataDir)
	if err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}

	db, err := repository.OpenDB(dbPath, 5000)
	if err != nil {
		log.Printf("error: open database: %v", err)
		lockFile.Close()
		os.Exit(1)
	}

	code := lda.RunImport(db, mappingArgs)
	db.Close()
	lockFile.Close()
	os.Exit(code)
}
