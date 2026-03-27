package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	uiDir := flag.String("ui", "ui/dist", "path to React build output directory")
	pathwaysDir := flag.String("pathways", "examples", "directory containing pathway JSON files")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/pathways", listPathways(*pathwaysDir))
	mux.HandleFunc("/api/pathway", getPathway(*pathwaysDir))
	mux.Handle("/", spaHandler(*uiDir))

	log.Printf("listening on %s  (ui=%s  pathways=%s)", *addr, *uiDir, *pathwaysDir)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// listPathways returns a JSON array of .json filenames in dir.
func listPathways(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			http.Error(w, "cannot read pathways directory", http.StatusInternalServerError)
			return
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		if names == nil {
			names = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(names)
	}
}

// getPathway returns the raw JSON content of a single pathway file.
// The filename is taken from the "file" query parameter.
func getPathway(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("file")
		if name == "" {
			http.Error(w, "missing 'file' query parameter", http.StatusBadRequest)
			return
		}
		// Prevent path traversal: strip any directory component.
		name = filepath.Base(name)
		if !strings.HasSuffix(name, ".json") {
			http.Error(w, "only .json files are served", http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "pathway not found", http.StatusNotFound)
				return
			}
			http.Error(w, "error reading pathway", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(data)
	}
}

// spaHandler serves static files from dir, falling back to index.html for
// any path that doesn't correspond to an existing file (SPA client-side routing).
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			http.ServeFile(w, r, index)
			return
		}
		fs.ServeHTTP(w, r)
	})
}
