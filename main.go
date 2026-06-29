package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/mikaelstaldal/go-server-common/auth"
	"github.com/mikaelstaldal/go-server-common/csrf"
	"github.com/mikaelstaldal/go-server-common/httputil"
	commonweb "github.com/mikaelstaldal/go-server-common/web"
	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/lda"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/mikaelstaldal/mymail/internal/service"
	"github.com/mikaelstaldal/mymail/web"
)

func main() {
	version := flag.Bool("version", false, "print version information and exit")
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
	case *version:
		printVersion()
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

func printVersion() {
	fmt.Println("MyMail")
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	settings := make(map[string]string, len(info.Settings))
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if vcs, ok := settings["vcs"]; ok {
		fmt.Printf("%s ", vcs)
	}
	modified := settings["vcs.modified"] == "true"
	if rev, ok := settings["vcs.revision"]; ok {
		if modified {
			fmt.Printf("revision: %s (dirty)\n", rev)
		} else {
			fmt.Printf("revision: %s\n", rev)
		}
	}
	if t, ok := settings["vcs.time"]; ok {
		if parsedTime, err := time.Parse(time.RFC3339, t); err == nil {
			fmt.Printf("updated at: %s\n", parsedTime.Local().Format("2006-01-02 15:04:05"))
		} else {
			fmt.Printf("updated at: %s\n", t)
		}
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

// deriveMycalURL returns the MyCal base URL derived from publicURL by replacing
// its path with "/mycal". Returns empty string if publicURL has no path segment.
func deriveMycalURL(publicURL string) string {
	if publicURL == "" {
		return ""
	}
	u, err := url.Parse(publicURL)
	if err != nil || strings.Trim(u.Path, "/") == "" {
		return ""
	}
	u.Path = "/mycal"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// serverConfigScript returns an inline JS snippet that sets window.__serverConfig.
func serverConfigScript(mycalURL string) string {
	b, _ := json.Marshal(mycalURL)
	return "window.__serverConfig={mycalUrl:" + string(b) + "};"
}

// inlineScriptCSPHash returns the CSP sha256 hash token for an inline script.
func inlineScriptCSPHash(script string) string {
	h := sha256.Sum256([]byte(script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
}

// buildIndexHTML reads index.html from the embedded FS and, when configScript is
// non-empty, injects it as an inline <script> before </head>.
func buildIndexHTML(staticFS fs.FS, configScript string) ([]byte, error) {
	content, err := fs.ReadFile(staticFS, "static/index.html")
	if err != nil {
		return nil, err
	}
	if configScript == "" {
		return content, nil
	}
	modified := strings.Replace(string(content), "</head>",
		"<script>"+configScript+"</script>\n</head>", 1)
	return []byte(modified), nil
}

func runServer(dataDir, addr string, port int, publicURL, basicAuthFile, basicAuthRealm, sendmailBin, ldaSocket string) {
	if port < 1 || port > 65535 {
		log.Fatalf("invalid port: %d", port)
	}

	var authMiddleware func(http.Handler) http.Handler
	if basicAuthFile != "" {
		htpasswd, err := auth.LoadHtpasswd(basicAuthFile)
		if err != nil {
			log.Fatalf("load htpasswd: %v", err)
		}
		authMiddleware = htpasswd.Middleware(basicAuthRealm)
		log.Printf("basic authentication enabled")
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

	importMapHash, err := commonweb.ImportMapCSPHash(web.Static)
	if err != nil {
		log.Fatalf("error: compute importmap CSP hash: %v", err)
	}

	var configScript string
	if mycalURL := deriveMycalURL(publicURL); mycalURL != "" {
		configScript = serverConfigScript(mycalURL)
		log.Printf("mymail: MyCal URL auto-configured as %s", mycalURL)
	}
	indexHTML, err := buildIndexHTML(web.Static, configScript)
	if err != nil {
		log.Fatalf("error: build index.html: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", ogenServer)
	mux.HandleFunc("/", indexFallbackHandler(indexHTML))

	serverOrigin, err := csrf.ResolveServerOrigin(publicURL, addr, port)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	var httpHandler http.Handler = mux
	httpHandler = csrf.Middleware(serverOrigin)(httpHandler)

	csp := "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' " + importMapHash
	if configScript != "" {
		csp += " " + inlineScriptCSPHash(configScript)
	}
	httpHandler = httputil.SecurityHeaders(httputil.SecurityHeadersOptions{
		CSP:            csp,
		ReferrerPolicy: "same-origin",
		HSTS:           "max-age=31536000",
	})(httpHandler)
	if authMiddleware != nil {
		httpHandler = authMiddleware(httpHandler)
	}
	httpHandler = http.MaxBytesHandler(httpHandler, maxRequestBody)

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

	serverAddr := fmt.Sprintf("%s:%d", addr, port)
	srv := &http.Server{
		Addr:              serverAddr,
		Handler:           httpHandler,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       time.Minute,
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

	log.Printf("Starting server on %s", serverAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("Failed to start server: %v", err)
	}
}

// maxRequestBody is the hard ceiling on the size of an incoming HTTP request
// body, as documented in openapi.yaml. It guards against memory exhaustion from
// oversized JSON payloads and disk exhaustion from oversized multipart uploads
// (parts above MaxMultipartMemory otherwise spill to temp files).
const maxRequestBody = 32 << 20 // 32 MiB

// indexFallbackHandler serves files from the embedded static FS by their path;
// anything not found falls back to indexHTML for hash-based client-side routing.
// indexHTML is the (possibly config-injected) content of index.html.
func indexFallbackHandler(indexHTML []byte) http.HandlerFunc {
	staticSub, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(fmt.Sprintf("web: sub static: %v", err))
	}
	staticHandler, err := httputil.StaticHandler(staticSub)
	if err != nil {
		panic(fmt.Sprintf("web: static handler: %v", err))
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// Probe the sub-FS: strip the leading slash to get the bare file path.
		// Skip index.html so it is always served from the (possibly modified) indexHTML.
		fsPath := strings.TrimPrefix(r.URL.Path, "/")
		if fsPath != "" && fsPath != "index.html" {
			f, err := staticSub.Open(fsPath)
			if err == nil {
				stat, serr := f.Stat()
				_ = f.Close()
				if serr == nil && !stat.IsDir() {
					// StaticHandler adds Cache-Control, ETag, gzip and 304 handling.
					staticHandler.ServeHTTP(w, r)
					return
				}
			}
		}
		// Serve index.html for root and all unmatched paths (hash routing).
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	}
}
