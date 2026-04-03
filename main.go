package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/mikaelstaldal/mymail/web"
)

func main() {
	port := flag.Int("port", 8080, "HTTP listen port (1-65535)")
	addr := flag.String("addr", "", "Bind address")
	flag.String("data", "data/", "Data directory")
	flag.String("basic-auth-file", "", "Path to htpasswd file")
	flag.String("basic-auth-realm", "mymail", "Auth realm")
	flag.Parse()

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
