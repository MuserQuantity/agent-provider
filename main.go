package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

var cfg struct {
	addr    string
	proxy   string
	workdir string
	apiKey  string
	timeout time.Duration
}

func main() {
	loadDotEnv(".env")

	defTimeout := 10 * time.Minute
	if v := os.Getenv("AGENT_PROVIDER_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			defTimeout = d
		} else {
			log.Printf("ignoring invalid AGENT_PROVIDER_TIMEOUT=%q: %v", v, err)
		}
	}
	flag.StringVar(&cfg.addr, "addr", envOr("AGENT_PROVIDER_ADDR", "127.0.0.1:8080"), "listen address")
	flag.StringVar(&cfg.proxy, "proxy", envOr("AGENT_PROVIDER_PROXY", "http://127.0.0.1:7890"),
		"proxy URL exported to backend CLIs as all_proxy/http_proxy/https_proxy; empty string disables")
	flag.StringVar(&cfg.workdir, "workdir", envOr("AGENT_PROVIDER_WORKDIR", "."), "working directory the backend CLIs run in")
	flag.StringVar(&cfg.apiKey, "api-key", os.Getenv("AGENT_PROVIDER_API_KEY"), "if set, require 'Authorization: Bearer <key>'")
	flag.DurationVar(&cfg.timeout, "timeout", defTimeout, "max duration for a single CLI run")
	flag.Parse()
	if strings.EqualFold(cfg.proxy, "none") {
		cfg.proxy = ""
	}
	if err := os.MkdirAll(cfg.workdir, 0o755); err != nil {
		log.Fatalf("cannot create workdir %q: %v", cfg.workdir, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", auth(handleChatCompletions))
	mux.HandleFunc("POST /v1/completions", auth(handleCompletions))
	mux.HandleFunc("GET /v1/models", auth(handleModels))

	avail := availableBackends()
	log.Printf("agent-provider listening on %s", cfg.addr)
	log.Printf("available backends: %s (known: %s)", strings.Join(avail, ", "), strings.Join(knownBackends(), ", "))
	if cfg.proxy != "" {
		log.Printf("backend CLIs will run with all_proxy=%s", cfg.proxy)
	}

	srv := &http.Server{Addr: cfg.addr, Handler: logMW(cors(mux)), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// loadDotEnv loads KEY=VALUE lines from path into the process environment.
// Real environment variables take precedence over the file.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if k != "" && os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// cors allows browser-based clients (e.g. translator extensions) to call the
// API; preflight OPTIONS requests are answered here.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.apiKey != "" && r.Header.Get("Authorization") != "Bearer "+cfg.apiKey {
			writeErr(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		next(w, r)
	}
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
