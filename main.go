package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/mikaelstaldal/mymail/web"
)

func main() {
	initMode := flag.Bool("init", false, "Initialize database and exit")
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
