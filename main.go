package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/auth"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/lda"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/mikaelstaldal/mymail/internal/service"
	"github.com/mikaelstaldal/mymail/web"
)

func main() {
	initMode := flag.Bool("init", false, "Initialize database and exit")
	ldaMode := flag.Bool("lda", false, "LDA mode: read RFC 5322 from stdin and store in DB")
	importMode := flag.Bool("import", false, "Import messages from mbox/Maildir and exit")
	port := flag.Int("port", 8080, "HTTP listen port")
	addr := flag.String("addr", "127.0.0.1", "Bind address")
	dataDir := flag.String("data", "data/", "Data directory")
	basicAuthFile := flag.String("basic-auth-file", "", "Path to htpasswd file")
	basicAuthRealm := flag.String("basic-auth-realm", "mymail", "Auth realm")
	sendmailBin := flag.String("sendmail", "sendmail", "Path or name of sendmail binary")
	identityAddress := flag.String("identity-address", "", "Initial identity email address (used with -init)")
	identityName := flag.String("identity-name", "", "Initial identity display name (used with -init)")
	flag.Parse()

	switch {
	case *initMode:
		runInit(*dataDir, *identityAddress, *identityName)
	case *ldaMode:
		runLDA(*dataDir)
	case *importMode:
		runImport(*dataDir, flag.Args())
	default:
		runServer(*dataDir, *addr, *port, *basicAuthFile, *basicAuthRealm, *sendmailBin)
	}
}

func runLDA(dataDir string) {
	dbPath := filepath.Join(dataDir, "mymail.sqlite")
	if err := repository.CheckDBExists(dbPath); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}

	db, err := repository.OpenDB(dbPath, 30000)
	if err != nil {
		log.Printf("error: open database: %v", err)
		os.Exit(75)
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		log.Printf("lda: read stdin: %v", err)
		os.Exit(75)
	}

	lda.Run(db, raw) // calls os.Exit internally
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

func runServer(dataDir, addr string, port int, basicAuthFile, basicAuthRealm, sendmailBin string) {
	if port < 1 || port > 65535 {
		log.Fatalf("invalid port: %d", port)
	}

	dbPath := filepath.Join(dataDir, "mymail.sqlite")
	if err := repository.CheckDBExists(dbPath); err != nil {
		log.Fatalf("error: %v", err)
	}

	db, err := repository.OpenDB(dbPath, 5000)
	if err != nil {
		log.Fatalf("error: open database: %v", err)
	}
	defer db.Close()

	sendmailPath, err := exec.LookPath(sendmailBin)
	if err != nil {
		log.Fatalf("error: resolve sendmail %q: %v", sendmailBin, err)
	}

	lockFile, err := lda.AcquireImportLock(dataDir)
	if err != nil {
		log.Printf("error: acquire server lock (import running?): %v", err)
		os.Exit(1)
	}
	defer lockFile.Close()

	// Repositories.
	folderRepo := repository.NewFolderRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	attachmentRepo := repository.NewAttachmentRepository(db)
	draftRepo := repository.NewDraftRepository(db)
	contactRepo := repository.NewContactRepository(db)
	identityRepo := repository.NewIdentityRepository(db)
	filterRepo := repository.NewFilterRepository(db)
	spamFilterRepo := repository.NewSpamFilterRepository(db)

	h := handler.New(
		folderRepo, messageRepo, attachmentRepo, draftRepo,
		contactRepo, identityRepo, filterRepo, spamFilterRepo,
		sendmailPath,
	)

	ogenServer, err := api.NewServer(h)
	if err != nil {
		log.Fatalf("error: create API server: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", ogenServer)
	mux.HandleFunc("/", indexFallbackHandler())

	serverOrigin := fmt.Sprintf("http://%s:%d", addr, port)
	var httpHandler http.Handler = mux
	httpHandler = auth.NewCSRF(serverOrigin)(httpHandler)
	httpHandler = auth.NewBasicAuth(basicAuthFile, basicAuthRealm)(httpHandler)
	httpHandler = auth.SecurityHeaders(httpHandler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler := service.NewScheduler(db, sendmailPath, contactRepo)
	scheduler.Start(ctx)

	listenAddr := fmt.Sprintf("%s:%d", addr, port)
	srv := &http.Server{
		Addr:    listenAddr,
		Handler: httpHandler,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		signal.Stop(sigCh)
		log.Println("shutting down...")
		cancel()
		shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	log.Printf("mymail listening on %s", listenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server: %v", err)
	}
}

// indexFallbackHandler serves files from the embedded static FS by their path;
// anything not found falls back to index.html for hash-based client-side routing.
func indexFallbackHandler() http.HandlerFunc {
	staticSub, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(fmt.Sprintf("web: sub static: %v", err))
	}
	fileServer := http.FileServer(http.FS(staticSub))
	return func(w http.ResponseWriter, r *http.Request) {
		// Probe the sub-FS: strip the leading slash to get the bare file path.
		fsPath := strings.TrimPrefix(r.URL.Path, "/")
		f, err := staticSub.Open(fsPath)
		if err == nil {
			stat, serr := f.Stat()
			f.Close()
			if serr == nil && !stat.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Serve index.html for all unmatched paths (hash routing).
		// Rewrite the request URL so the file server handles ETag/Last-Modified.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r2)
	}
}
