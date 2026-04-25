package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// Prompt is the `otters prompt <agent> [prompt]` subcommand — a
// non-interactive alternative to `otters chat`. One RPC in, the
// assistant's final answer out; nothing else lands on stdout so the
// command is safe to pipe through `jq`, `pbcopy`, etc.
//
// Prompt sources (first match wins):
//  1. explicit positional prompt text;
//  2. positional "-" → read stdin;
//  3. no positional → read stdin.
//
// When --schema or --schema-file is set, the command switches to
// structured-output mode: the daemon dispatches a stateless
// GenerateObject call and emits valid JSON on stdout. --session has
// no effect in that mode.
//
// Sessions are ephemeral unless --session is set; a one-shot call
// shouldn't pollute other conversations' memory by default.
type Prompt struct {
	Ref        string `arg:"" name:"agent" help:"Agent id or name"`
	PromptArg  string `arg:"" name:"prompt" optional:"" help:"Prompt text; '-' or absent reads from stdin"`
	SessionID  string `short:"s" name:"session" help:"Reuse a named session (default: fresh ephemeral session per invocation; ignored in structured-output mode)" default:""`
	Schema     string `name:"schema" help:"Inline JSON schema string. Switches to structured-output mode; stateless, no session memory."`
	SchemaFile string `name:"schema-file" help:"Path to a JSON schema file. Switches to structured-output mode; stateless, no session memory."`
	SchemaName string `name:"schema-name" help:"Optional label for the schema (surfaces in tool-mode providers)"`
	SchemaDesc string `name:"schema-desc" help:"Optional human description of the schema"`
	Pretty     bool   `name:"pretty" help:"Indent JSON output (structured mode only)"`
}

func (p *Prompt) Run(ctx context.Context, _ *cmd.Commons, d *Daemon) error {
	if p.Schema != "" && p.SchemaFile != "" {
		return fmt.Errorf("--schema and --schema-file are mutually exclusive")
	}

	prompt, err := readPrompt(p.PromptArg)
	if err != nil {
		return err
	}

	if prompt == "" {
		return fmt.Errorf("prompt is empty")
	}

	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	schemaBytes, err := resolveSchema(p.Schema, p.SchemaFile)
	if err != nil {
		return err
	}

	if schemaBytes != nil {
		resp, objErr := c.PromptObject(ctx, &daemonv1.PromptObjectRequest{
			Ref:        p.Ref,
			Prompt:     prompt,
			SchemaJson: schemaBytes,
			SchemaName: p.SchemaName,
			SchemaDesc: p.SchemaDesc,
		})
		if objErr != nil {
			return fmt.Errorf("prompt object failed: %w", unwrapRPC(objErr))
		}

		return writeObject(os.Stdout, resp.GetObjectJson(), p.Pretty)
	}

	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = "cli:prompt:" + uuid.NewString()
	}

	resp, err := c.ChatWithAgent(ctx, &daemonv1.ChatRequest{
		Ref:       p.Ref,
		SessionId: sessionID,
		Prompt:    prompt,
	})
	if err != nil {
		return fmt.Errorf("prompt failed: %w", unwrapRPC(err))
	}

	out := strings.TrimRight(resp.GetResponse(), "\n")
	fmt.Fprintln(os.Stdout, out)

	return nil
}

// readPrompt resolves the prompt text from the positional arg or
// stdin. Kept pure (no Daemon / gRPC deps) so it's trivially
// testable. Empty output is fine here — the caller decides whether
// empty prompts are an error.
func readPrompt(pos string) (string, error) {
	if pos != "" && pos != "-" {
		return pos, nil
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}

	return strings.TrimRight(string(data), "\n"), nil
}

// resolveSchema returns the schema bytes from the inline string or
// the file path, or nil if neither is set (text mode). Stdin is
// reserved for the prompt. Callers must ensure inline and path are
// not both set.
func resolveSchema(inline, path string) ([]byte, error) {
	if inline != "" {
		return []byte(inline), nil
	}

	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}

	return data, nil
}

func writeObject(w io.Writer, obj []byte, pretty bool) error {
	if pretty {
		var buf bytes.Buffer
		if err := json.Indent(&buf, obj, "", "  "); err != nil {
			return fmt.Errorf("indenting object: %w", err)
		}

		if _, err := w.Write(buf.Bytes()); err != nil {
			return err
		}
	} else {
		if _, err := w.Write(obj); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintln(w)

	return err
}
