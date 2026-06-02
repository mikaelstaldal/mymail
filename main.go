package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
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
	demoMode := flag.Bool("demo", false, "Populate database with demo data and exit")
	port := flag.Int("port", 8080, "HTTP listen port")
	addr := flag.String("addr", "127.0.0.1", "Bind address")
	publicURL := flag.String("public-url", "", "Public-facing base URL for CSRF validation, e.g. https://example.com (defaults to http://<addr>:<port>)")
	dataDir := flag.String("data", "data/", "Data directory")
	basicAuthFile := flag.String("basic-auth-file", "", "Path to htpasswd file")
	basicAuthRealm := flag.String("basic-auth-realm", "mymail", "Auth realm")
	sendmailBin := flag.String("sendmail", "sendmail", "Path or name of sendmail binary")
	ldaSocket := flag.String("lda-socket", "", "UNIX socket path for LDA delivery (enables socket-based LDA alongside HTTP server)")
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
	case *demoMode:
		runDemo(*dataDir)
	default:
		runServer(*dataDir, *addr, *port, *publicURL, *basicAuthFile, *basicAuthRealm, *sendmailBin, *ldaSocket)
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
	// Bound the read: a single message must not be able to exhaust memory.
	// Read one byte past the limit so we can distinguish "at limit" from "over".
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, lda.MaxMessageBytes+1))
	if err != nil {
		log.Printf("lda: read stdin: %v", err)
		os.Exit(75)
	}
	if int64(len(raw)) > lda.MaxMessageBytes {
		log.Printf("lda: message exceeds %d bytes, rejecting", lda.MaxMessageBytes)
		os.Exit(1)
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

func runServer(dataDir, addr string, port int, publicURL, basicAuthFile, basicAuthRealm, sendmailBin, ldaSocket string) {
	if port < 1 || port > 65535 {
		log.Fatalf("invalid port: %d", port)
	}

	dbPath := filepath.Join(dataDir, "mymail.sqlite")
	if err := repository.CheckDBExists(dbPath); err != nil {
		log.Fatalf("error: %v", err)
	}

	db, err := repository.OpenDB(dbPath, 5000,
		"mmap_size=134217728",
		"synchronous=NORMAL",
	)
	if err != nil {
		log.Fatalf("error: open database: %v", err)
	}
	defer db.Close()

	// Allow concurrent reads under WAL mode; writes still serialize at the SQLite level.
	numConns := runtime.GOMAXPROCS(0)
	db.SetMaxOpenConns(numConns)
	db.SetMaxIdleConns(numConns)

	// One-shot: update query planner statistics for all connections.
	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		log.Fatalf("error: PRAGMA optimize: %v", err)
	}

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

	ogenServer, err := api.NewServer(h, api.WithPathPrefix("/api/v1"))
	if err != nil {
		log.Fatalf("error: create API server: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", ogenServer)
	mux.HandleFunc("/", indexFallbackHandler())

	serverOrigin := fmt.Sprintf("http://%s:%d", addr, port)
	if publicURL != "" {
		u, err := url.Parse(publicURL)
		if err != nil || u.Host == "" {
			log.Fatalf("invalid -public-url %q: must be a full URL like https://example.com", publicURL)
		}
		serverOrigin = u.Scheme + "://" + u.Host
	}
	var httpHandler http.Handler = mux
	httpHandler = maxBodyMiddleware(httpHandler)
	httpHandler = auth.NewCSRF(serverOrigin)(httpHandler)
	httpHandler = auth.NewBasicAuth(basicAuthFile, basicAuthRealm)(httpHandler)
	importMapHash, err := web.ImportMapCSPHash(web.Static)
	if err != nil {
		log.Fatalf("error: compute importmap CSP hash: %v", err)
	}
	httpHandler = auth.NewSecurityHeaders(importMapHash)(httpHandler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if ldaSocket != "" {
		ln, err := lda.BindSocket(ldaSocket)
		if err != nil {
			log.Fatalf("error: lda socket: %v", err)
		}
		go lda.ServeSocket(ctx, ln, db)
		log.Printf("mymail lda socket listening on %s", ldaSocket)
	}

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

// maxRequestBody is the hard ceiling on the size of an incoming HTTP request
// body, as documented in openapi.yaml. It guards against memory exhaustion from
// oversized JSON payloads and disk exhaustion from oversized multipart uploads
// (parts above MaxMultipartMemory otherwise spill to temp files).
const maxRequestBody = 32 << 20 // 32 MiB

// maxBodyMiddleware caps every request body at maxRequestBody bytes. When the
// limit is exceeded, http.MaxBytesReader makes the next Read fail and responds
// 413 (Request Entity Too Large) if no bytes have been written yet.
func maxBodyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
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
			_ = f.Close()
			if serr == nil && !stat.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Serve index.html for all unmatched paths (hash routing).
		// Use "/" so the file server finds index.html via directory lookup — passing
		// "/index.html" directly would trigger Go's built-in redirect of that path to "./".
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	}
}
