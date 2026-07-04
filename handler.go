package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	run(w, r, req.Model, buildPrompt(req.Messages), true, req.Stream)
}

func handleCompletions(w http.ResponseWriter, r *http.Request) {
	var req CompletionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	run(w, r, req.Model, req.PromptText(), false, req.Stream)
}

// decodeJSON parses the request body, tolerating a UTF-8 BOM (common from
// Windows clients).
func decodeJSON(r *http.Request, v any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), v)
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	list := ModelList{Object: "list", Data: []Model{}}
	created := time.Now().Unix()
	for _, name := range availableBackends() {
		list.Data = append(list.Data, Model{ID: name, Object: "model", Created: created, OwnedBy: "agent-provider"})
	}
	writeJSON(w, http.StatusOK, list)
}

func run(w http.ResponseWriter, r *http.Request, model, prompt string, chat, stream bool) {
	name, inner := splitModel(model)
	b, ok := backends[name]
	if !ok {
		writeErr(w, http.StatusNotFound,
			fmt.Sprintf("unknown model %q; use one of: %s (optionally suffixed like \"devin:claude-opus-4.6\")",
				model, strings.Join(knownBackends(), ", ")))
		return
	}
	if _, err := exec.LookPath(b.Exe); err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("backend %q unavailable: %q not found in PATH", name, b.Exe))
		return
	}
	if strings.TrimSpace(prompt) == "" {
		writeErr(w, http.StatusBadRequest, "empty prompt")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), cfg.timeout)
	defer cancel()
	cmd, cleanup, err := buildCmd(ctx, b, inner, prompt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cleanup()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	id, created := genID(chat), time.Now().Unix()
	log.Printf("-> %s: %s", model, strings.Join(cmd.Args, " "))

	if !stream {
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			writeErr(w, http.StatusBadGateway, backendErr(name, err, &stderr))
			return
		}
		content := strings.TrimRight(stdout.String(), "\r\n")
		usage := &Usage{PromptTokens: estTokens(prompt), CompletionTokens: estTokens(content)}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		stop := "stop"
		if chat {
			writeJSON(w, http.StatusOK, ChatResponse{
				ID: id, Object: "chat.completion", Created: created, Model: model,
				Choices: []ChatChoice{{Message: &FullMessage{Role: "assistant", Content: content}, FinishReason: &stop}},
				Usage:   usage,
			})
		} else {
			writeJSON(w, http.StatusOK, CompletionResponse{
				ID: id, Object: "text_completion", Created: created, Model: model,
				Choices: []TextChoice{{Text: content, FinishReason: &stop}},
				Usage:   usage,
			})
		}
		return
	}

	// ---- streaming (SSE) ----
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported by server")
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		writeErr(w, http.StatusBadGateway, backendErr(name, err, &stderr))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	emit := func(delta *DeltaMessage, text string, finish *string) {
		if chat {
			sse(w, flusher, ChatResponse{ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
				Choices: []ChatChoice{{Delta: delta, FinishReason: finish}}})
		} else {
			sse(w, flusher, CompletionResponse{ID: id, Object: "text_completion", Created: created, Model: model,
				Choices: []TextChoice{{Text: text, FinishReason: finish}}})
		}
	}
	if chat {
		emit(&DeltaMessage{Role: "assistant"}, "", nil)
	}

	var chunker utf8Chunker
	buf := make([]byte, 4096)
	sent := false
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			if s := chunker.push(buf[:n]); s != "" {
				sent = true
				emit(&DeltaMessage{Content: s}, s, nil)
			}
		}
		if rerr != nil {
			break
		}
	}
	if s := chunker.flush(); s != "" {
		sent = true
		emit(&DeltaMessage{Content: s}, s, nil)
	}
	if werr := cmd.Wait(); werr != nil && !sent {
		var e APIError
		e.Error.Message = backendErr(name, werr, &stderr)
		e.Error.Type = "api_error"
		sse(w, flusher, e)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	stop := "stop"
	emit(&DeltaMessage{}, "", &stop)
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// utf8Chunker buffers raw bytes and releases only complete UTF-8 sequences,
// so multi-byte characters are never split across SSE chunks.
type utf8Chunker struct{ pending []byte }

func (c *utf8Chunker) push(p []byte) string {
	c.pending = append(c.pending, p...)
	cut := len(c.pending)
	for cut > 0 && len(c.pending)-cut < utf8.UTFMax && !utf8.Valid(c.pending[:cut]) {
		cut--
	}
	if !utf8.Valid(c.pending[:cut]) {
		if len(c.pending) >= utf8.UTFMax { // genuinely invalid bytes: pass through
			s := string(c.pending)
			c.pending = c.pending[:0]
			return s
		}
		return ""
	}
	s := string(c.pending[:cut])
	c.pending = append(c.pending[:0], c.pending[cut:]...)
	return s
}

func (c *utf8Chunker) flush() string {
	s := string(c.pending)
	c.pending = nil
	return s
}

// ---- helpers ----

func sse(w http.ResponseWriter, f http.Flusher, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	var e APIError
	e.Error.Message = msg
	e.Error.Type = "invalid_request_error"
	if status >= 500 {
		e.Error.Type = "api_error"
	}
	writeJSON(w, status, e)
}

func backendErr(name string, err error, stderr *bytes.Buffer) string {
	msg := fmt.Sprintf("backend %q failed: %v", name, err)
	if s := strings.TrimSpace(stderr.String()); s != "" {
		msg += ": " + tail(s, 1000)
	}
	return msg
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func genID(chat bool) string {
	b := make([]byte, 12)
	rand.Read(b)
	if chat {
		return "chatcmpl-" + hex.EncodeToString(b)
	}
	return "cmpl-" + hex.EncodeToString(b)
}

// estTokens is a rough estimate (~4 bytes per token); the CLIs don't report
// real usage.
func estTokens(s string) int {
	return (len(s) + 3) / 4
}
