package main

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// promptMode is how the flattened prompt is handed to the CLI.
type promptMode int

const (
	viaFile  promptMode = iota // written to a temp file, path passed via flag
	viaStdin                   // piped to stdin
	viaArg                     // appended as the last argument
)

type Backend struct {
	Exe  string
	Mode promptMode
	// Args builds the CLI arguments. model is the pass-through model (may be
	// empty), promptFile is the temp prompt file path (set only for viaFile).
	Args func(model, promptFile string) []string
}

var backends = map[string]Backend{
	"devin": {
		Exe: "devin", Mode: viaFile,
		Args: func(model, promptFile string) []string {
			args := []string{"-p", "--prompt-file", promptFile}
			if model != "" {
				args = append(args, "--model", model)
			}
			return args
		},
	},
	"grok": {
		Exe: "grok", Mode: viaFile,
		Args: func(model, promptFile string) []string {
			args := []string{"--prompt-file", promptFile}
			if model != "" {
				args = append(args, "-m", model)
			}
			return args
		},
	},
	"claude": {
		Exe: "claude", Mode: viaStdin,
		Args: func(model, _ string) []string {
			args := []string{"-p"}
			if model != "" {
				args = append(args, "--model", model)
			}
			return args
		},
	},
	"codex": {
		Exe: "codex", Mode: viaStdin,
		Args: func(model, _ string) []string {
			args := []string{"exec"}
			if model != "" {
				args = append(args, "-m", model)
			}
			return append(args, "-") // "-" = read prompt from stdin
		},
	},
	"opencode": {
		Exe: "opencode", Mode: viaArg,
		Args: func(model, _ string) []string {
			args := []string{"run"}
			if model != "" {
				args = append(args, "--model", model)
			}
			return args
		},
	},
	"gemini": {
		Exe: "gemini", Mode: viaArg,
		Args: func(model, _ string) []string {
			var args []string
			if model != "" {
				args = append(args, "-m", model)
			}
			return append(args, "-p")
		},
	},
}

// splitModel splits "devin:claude-opus-4.6" into the backend name ("devin")
// and the pass-through model ("claude-opus-4.6").
func splitModel(model string) (string, string) {
	name, rest, _ := strings.Cut(model, ":")
	return name, rest
}

func knownBackends() []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func availableBackends() []string {
	var names []string
	for name, b := range backends {
		if _, err := exec.LookPath(b.Exe); err == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// buildPrompt flattens an OpenAI messages array into a single prompt string.
func buildPrompt(msgs []ChatMessage) string {
	if len(msgs) == 1 && msgs[0].Role == "user" {
		return msgs[0].Text()
	}
	if len(msgs) == 2 && msgs[0].Role == "system" && msgs[1].Role == "user" {
		return msgs[0].Text() + "\n\n" + msgs[1].Text()
	}
	var b strings.Builder
	b.WriteString("The following is a conversation transcript. Continue it by replying to the last user message; output only your reply.\n\n")
	for _, m := range msgs {
		b.WriteString("[" + m.Role + "]\n" + m.Text() + "\n\n")
	}
	b.WriteString("[assistant]\n")
	return b.String()
}

// buildCmd prepares the CLI invocation for a backend. The returned cleanup
// must be called after the command finishes.
func buildCmd(ctx context.Context, b Backend, model, prompt string) (*exec.Cmd, func(), error) {
	cleanup := func() {}
	promptFile := ""
	if b.Mode == viaFile {
		f, err := os.CreateTemp("", "agent-prompt-*.txt")
		if err != nil {
			return nil, cleanup, err
		}
		_, werr := f.WriteString(prompt)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		promptFile = f.Name()
		cleanup = func() { os.Remove(promptFile) }
		if werr != nil {
			return nil, cleanup, werr
		}
	}
	args := b.Args(model, promptFile)
	if b.Mode == viaArg {
		args = append(args, prompt)
	}
	cmd := exec.CommandContext(ctx, b.Exe, args...)
	if b.Mode == viaStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}
	cmd.Dir = cfg.workdir
	cmd.Env = childEnv()
	return cmd, cleanup, nil
}

// childEnv returns the environment for backend CLIs, overriding proxy
// variables with the configured proxy (if any).
func childEnv() []string {
	env := os.Environ()
	if cfg.proxy == "" {
		return env
	}
	out := make([]string, 0, len(env)+6)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToLower(k) {
		case "all_proxy", "http_proxy", "https_proxy":
			continue
		}
		out = append(out, kv)
	}
	for _, k := range []string{"all_proxy", "http_proxy", "https_proxy"} {
		out = append(out, k+"="+cfg.proxy, strings.ToUpper(k)+"="+cfg.proxy)
	}
	return out
}
