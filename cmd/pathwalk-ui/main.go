package main

import (
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/tools"
)

func main() {
	addr := flag.String("addr", ":0", "listen address (use :0 for random port)")
	uiDir := flag.String("ui", "ui/dist", "path to React build output directory")
	pathwaysDir := flag.String("pathways", "examples", "directory containing pathway JSON files")
	apiKey := flag.String("api-key", "", "default OpenAI API key (can be overridden per request)")
	baseURL := flag.String("base-url", "", "default OpenAI base URL (can be overridden per request)")
	model := flag.String("model", "qwen/qwen3.5-35b-a3b", "default LLM model (can be overridden per request)")
	maxSteps := flag.Int("max-steps", 50, "default max steps per run")
	flag.Parse()

	// Allow API key from env
	if *apiKey == "" {
		*apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if *baseURL == "" {
		*baseURL = os.Getenv("OPENAI_BASE_URL")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/pathways", listPathways(*pathwaysDir))
	mux.HandleFunc("/api/pathway", getPathway(*pathwaysDir))
	mux.HandleFunc("/api/run", runPathway(*pathwaysDir, *apiKey, *baseURL, *model, *maxSteps))
	mux.HandleFunc("POST /api/pathway", savePathway(*pathwaysDir))
	mux.HandleFunc("OPTIONS /", corsHandler)
	mux.Handle("/", spaHandler(*uiDir))

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	actualAddr := listener.Addr().(*net.TCPAddr)
	log.Printf("listening on http://localhost:%d  (ui=%s  pathways=%s)", actualAddr.Port, *uiDir, *pathwaysDir)
	log.Fatal(http.Serve(listener, mux))
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

// corsHandler responds to CORS preflight requests
func corsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.WriteHeader(http.StatusOK)
}

// RunRequest is the request body for POST /api/run
type RunRequest struct {
	File    string `json:"file"`
	Task    string `json:"task"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	MaxSteps int   `json:"max_steps"`
}

// RunResponse is the response from POST /api/run
type RunResponse struct {
	Output     string                 `json:"output"`
	Reason     string                 `json:"reason"`
	Variables  map[string]any         `json:"variables"`
	Steps      []map[string]any       `json:"steps"`
	FailedNode string                 `json:"failed_node"`
	Error      string                 `json:"error,omitempty"`
}

// runPathway returns a handler for POST /api/run
func runPathway(pathwaysDir, defaultAPIKey, defaultBaseURL, defaultModel string, defaultMaxSteps int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")

		var req RunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(RunResponse{Error: "invalid request: " + err.Error()})
			return
		}

		// Apply defaults if not specified in request
		if req.Model == "" {
			req.Model = defaultModel
		}
		if req.APIKey == "" {
			req.APIKey = defaultAPIKey
		}
		if req.BaseURL == "" {
			req.BaseURL = defaultBaseURL
		}
		if req.MaxSteps == 0 {
			req.MaxSteps = defaultMaxSteps
		}

		// Verify file exists and is a .json file
		filename := filepath.Base(req.File)
		if !strings.HasSuffix(filename, ".json") {
			json.NewEncoder(w).Encode(RunResponse{Error: "only .json files are allowed"})
			return
		}

		pathwayPath := filepath.Join(pathwaysDir, filename)
		if _, err := os.Stat(pathwayPath); os.IsNotExist(err) {
			json.NewEncoder(w).Encode(RunResponse{Error: "pathway not found"})
			return
		}

		// Parse the pathway
		pathway, err := pathwalk.ParsePathway(pathwayPath)
		if err != nil {
			json.NewEncoder(w).Encode(RunResponse{Error: "failed to parse pathway: " + err.Error()})
			return
		}

		// Create LLM client
		llm := pathwalk.NewOpenAIClient(req.APIKey, req.BaseURL, req.Model)

		// Create engine with built-in tools
		engine := pathwalk.NewEngine(
			pathway,
			llm,
			pathwalk.WithTools(tools.BuiltinTools()...),
		)

		// Run the pathway
		ctx := r.Context()
		result, err := engine.Run(ctx, req.Task)
		if err != nil {
			json.NewEncoder(w).Encode(RunResponse{Error: "execution error: " + err.Error()})
			return
		}

		// Convert steps to JSON-serializable format
		stepsOut := make([]map[string]any, len(result.Steps))
		for i, step := range result.Steps {
			stepMap := map[string]any{
				"nodeId":      step.NodeID,
				"nodeName":    step.NodeName,
				"output":      step.Output,
				"variables":   step.Vars,
				"nextNode":    step.NextNode,
				"routeReason": step.RouteReason,
			}
			// Include tool calls if any
			if len(step.ToolCalls) > 0 {
				toolCalls := make([]map[string]any, len(step.ToolCalls))
				for j, tc := range step.ToolCalls {
					toolCalls[j] = map[string]any{
						"id":     tc.ID,
						"name":   tc.Name,
						"args":   tc.Args,
						"result": tc.Result,
						"error":  tc.Error,
					}
				}
				stepMap["toolCalls"] = toolCalls
			}
			stepsOut[i] = stepMap
		}

		resp := RunResponse{
			Output:     result.Output,
			Reason:     result.Reason,
			Variables:  result.Variables,
			Steps:      stepsOut,
			FailedNode: result.FailedNode,
		}

		json.NewEncoder(w).Encode(resp)
	}
}

// SavePathwayRequest is the request body for POST /api/pathway
type SavePathwayRequest struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

// SavePathwayResponse is the response from POST /api/pathway
type SavePathwayResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// savePathway returns a handler for POST /api/pathway
func savePathway(pathwaysDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")

		var req SavePathwayRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(SavePathwayResponse{OK: false, Error: "invalid request"})
			return
		}

		// Verify filename
		filename := filepath.Base(req.File)
		if !strings.HasSuffix(filename, ".json") {
			json.NewEncoder(w).Encode(SavePathwayResponse{OK: false, Error: "only .json files allowed"})
			return
		}

		// Validate JSON
		var obj any
		if err := json.Unmarshal([]byte(req.Content), &obj); err != nil {
			json.NewEncoder(w).Encode(SavePathwayResponse{OK: false, Error: "invalid JSON: " + err.Error()})
			return
		}

		// Write file
		pathwayPath := filepath.Join(pathwaysDir, filename)
		if err := os.WriteFile(pathwayPath, []byte(req.Content), 0644); err != nil {
			json.NewEncoder(w).Encode(SavePathwayResponse{OK: false, Error: "failed to save: " + err.Error()})
			return
		}

		json.NewEncoder(w).Encode(SavePathwayResponse{OK: true})
	}
}
