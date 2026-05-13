package server

import (
	"embed"
	"io/fs"
	"net/http"

	"stream-agents/internal/store"
)

//go:embed templates
var tmplFS embed.FS

//go:embed assets
var assetsFS embed.FS

func NewMux(idx *store.Index) http.Handler {
	mux := http.NewServeMux()
	h := &handlers{idx: idx}

	sub, _ := fs.Sub(assetsFS, "assets")
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/session/{agent}/{id}", h.handleSession)
	mux.HandleFunc("/stream/{agent}/{id}", h.handleStream)
	mux.HandleFunc("/notify", h.handleNotify)
	mux.HandleFunc("/hot.json", h.handleHotJSON)
	mux.HandleFunc("/", h.handleList)
	return mux
}
