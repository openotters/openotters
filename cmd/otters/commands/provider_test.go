//nolint:testpackage // exercises unexported helpers
package commands

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"

	"github.com/openotters/openotters/internal"
)

func internalProvidersFixture() internal.ProvidersFile {
	return internal.ProvidersFile{
		Providers: []internal.ProviderConfig{
			{Name: "anthropic", APIKey: "sk-a"},
			{Name: "openai", APIKey: "sk-o"},
			{Name: "groq", APIKey: "sk-g"},
		},
	}
}

func TestCatwalkTypeSupported(t *testing.T) {
	t.Parallel()

	cases := map[catwalk.Type]bool{
		catwalk.TypeAnthropic:    true,
		catwalk.TypeOpenAI:       true,
		catwalk.TypeOpenAICompat: true,
		catwalk.TypeOpenRouter:   true,
		catwalk.TypeAzure:        false,
		catwalk.TypeBedrock:      false,
		catwalk.TypeGoogle:       false,
		catwalk.TypeVercel:       false,
		catwalk.TypeVertexAI:     false,
	}

	for tp, want := range cases {
		if got := catwalkTypeSupported(tp); got != want {
			t.Errorf("catwalkTypeSupported(%q) = %v, want %v", tp, got, want)
		}
	}
}

func TestPresetByID(t *testing.T) {
	t.Parallel()

	presets := []providerPreset{
		{ID: "anthropic", APIBase: "https://api.anthropic.com"},
		{ID: "groq", APIBase: "https://api.groq.com/openai/v1"},
	}

	if got := presetByID(presets, "groq"); got == nil || got.APIBase != "https://api.groq.com/openai/v1" {
		t.Fatalf("presetByID('groq') = %+v, want non-nil with groq endpoint", got)
	}

	if got := presetByID(presets, "missing"); got != nil {
		t.Fatalf("presetByID('missing') = %+v, want nil", got)
	}
}

func TestDefaultsForPreset(t *testing.T) {
	t.Parallel()

	presets := []providerPreset{
		{ID: "groq", APIBase: "https://api.groq.com/openai/v1"},
		{ID: "openai", APIBase: "https://api.openai.com/v1"},
	}

	t.Run("named preset seeds name + base + models", func(t *testing.T) {
		t.Parallel()

		fields := defaultsForPreset(presets, "groq")
		if fields.name != "groq" || fields.apiBase != "https://api.groq.com/openai/v1" ||
			fields.modelsCSV != modelsAllowAllSentinel {
			t.Fatalf("defaultsForPreset(groq) = %+v", fields)
		}
	})

	t.Run("custom sentinel leaves name + base blank", func(t *testing.T) {
		t.Parallel()

		fields := defaultsForPreset(presets, customPresetSentinel)
		if fields.name != "" || fields.apiBase != "" {
			t.Errorf("custom seeded name/apiBase: %+v", fields)
		}

		if fields.modelsCSV != modelsAllowAllSentinel {
			t.Errorf("modelsCSV = %q, want %q", fields.modelsCSV, modelsAllowAllSentinel)
		}
	})

	t.Run("unknown preset id falls back to blank inputs", func(t *testing.T) {
		t.Parallel()

		fields := defaultsForPreset(presets, "missing")
		if fields.name != "" || fields.apiBase != "" {
			t.Errorf("unknown preset seeded fields: %+v", fields)
		}

		// modelsCSV still gets the sane default — third input never opens empty.
		if fields.modelsCSV != modelsAllowAllSentinel {
			t.Errorf("modelsCSV = %q, want %q", fields.modelsCSV, modelsAllowAllSentinel)
		}
	})
}

func TestParseModelsCSV(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"":                         nil,
		"  ":                       nil,
		"*":                        nil,
		" * ":                      nil,
		"a":                        {"a"},
		"a,b,c":                    {"a", "b", "c"},
		" a , b , c ":              {"a", "b", "c"},
		"a,,b":                     {"a", "b"},
		",":                        nil,
		"claude-sonnet-4-5, gpt-4": {"claude-sonnet-4-5", "gpt-4"},
	}

	for in, want := range cases {
		got := parseModelsCSV(in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseModelsCSV(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestValidateProviderName(t *testing.T) {
	t.Parallel()

	good := []string{"a", "anthropic", "anthropic-prod", "x9", "abc-123"}
	for _, s := range good {
		if err := validateProviderName(s); err != nil {
			t.Errorf("validateProviderName(%q) = %v, want nil", s, err)
		}
	}

	bad := []string{"", "  ", "Anthropic", "1abc", "-anthropic", "anthropic_prod", "anthropic.prod"}
	for _, s := range bad {
		if err := validateProviderName(s); err == nil {
			t.Errorf("validateProviderName(%q) = nil, want error", s)
		}
	}
}

func TestResolveAPIEndpoint(t *testing.T) {
	// t.Setenv mutates process-global state — incompatible with t.Parallel.
	t.Setenv("ANTHROPIC_API_ENDPOINT", "")
	t.Setenv("OPENAI_API_ENDPOINT", "https://internal-proxy.example.com/openai/v1")

	cases := []struct {
		id, raw, want string
	}{
		// Direct URLs pass through.
		{"groq", "https://api.groq.com/openai/v1", "https://api.groq.com/openai/v1"},

		// $VAR with the env unset → fallback to canonicalEndpoints.
		{"anthropic", "$ANTHROPIC_API_ENDPOINT", "https://api.anthropic.com"},

		// $VAR with the env set → env wins over the canonical map.
		{"openai", "$OPENAI_API_ENDPOINT", "https://internal-proxy.example.com/openai/v1"},

		// $VAR for a provider we don't have a fallback for → empty
		// (lets the runtime SDK pick its own default rather than
		// leaking the placeholder into providers.yaml).
		{"weird", "$WEIRD_ENDPOINT", ""},

		// Empty stays empty.
		{"groq", "", ""},
	}

	for _, tc := range cases {
		if got := resolveAPIEndpoint(tc.id, tc.raw); got != tc.want {
			t.Errorf("resolveAPIEndpoint(%q, %q) = %q, want %q", tc.id, tc.raw, got, tc.want)
		}
	}
}

func TestModelsFilterDescription(t *testing.T) {
	t.Parallel()

	t.Run("falls back to a generic example without an available list", func(t *testing.T) {
		t.Parallel()

		got := modelsFilterDescription(nil)
		if !strings.Contains(got, "claude-sonnet-4-5") {
			t.Errorf("missing fallback example: %q", got)
		}
	})

	t.Run("splices the full list, no truncation even when long", func(t *testing.T) {
		t.Parallel()

		// 30 entries — all of them must show up. The form's renderer
		// wraps lines on its own; we don't pre-truncate so the user
		// can still scan the full set.
		models := make([]string, 30)
		for i := range models {
			models[i] = fmt.Sprintf("model-%02d", i)
		}

		got := modelsFilterDescription(models)
		for _, want := range models {
			if !strings.Contains(got, want) {
				t.Errorf("description missing %q: %q", want, got)
			}
		}

		if strings.Contains(got, "more") {
			t.Errorf("unexpected truncation suffix: %q", got)
		}
	})
}

func TestNonInteractiveConfig(t *testing.T) {
	t.Parallel()

	t.Run("empty name → falls back to interactive", func(t *testing.T) {
		t.Parallel()

		var a ProviderAdd

		_, ok, err := a.nonInteractiveConfig(strings.NewReader(""), false)
		if ok || err != nil {
			t.Fatalf("ok=%v err=%v, want (false, nil)", ok, err)
		}
	})

	t.Run("flag values populate the config", func(t *testing.T) {
		t.Parallel()

		a := ProviderAdd{
			Name:    "groq",
			APIKey:  "sk-groq",
			APIBase: "https://api.groq.com/openai/v1",
			Models:  "llama-3.3-70b, mixtral-8x7b",
		}

		cfg, ok, err := a.nonInteractiveConfig(strings.NewReader(""), false)
		if !ok || err != nil {
			t.Fatalf("ok=%v err=%v, want (true, nil)", ok, err)
		}

		if cfg.Name != "groq" || cfg.APIKey != "sk-groq" || cfg.APIBase != "https://api.groq.com/openai/v1" {
			t.Errorf("cfg = %+v", cfg)
		}

		if !reflect.DeepEqual(cfg.Models, []string{"llama-3.3-70b", "mixtral-8x7b"}) {
			t.Errorf("models = %v", cfg.Models)
		}
	})

	t.Run("piped stdin supplies the api-key when --api-key is empty", func(t *testing.T) {
		t.Parallel()

		a := ProviderAdd{Name: "anthropic"}

		cfg, ok, err := a.nonInteractiveConfig(strings.NewReader("sk-ant-from-stdin\n"), true)
		if !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}

		if cfg.APIKey != "sk-ant-from-stdin" {
			t.Errorf("APIKey = %q (trailing newline not trimmed?)", cfg.APIKey)
		}
	})

	t.Run("flag wins over piped stdin", func(t *testing.T) {
		t.Parallel()

		a := ProviderAdd{Name: "anthropic", APIKey: "sk-from-flag"}

		cfg, _, err := a.nonInteractiveConfig(strings.NewReader("sk-from-stdin"), true)
		if err != nil {
			t.Fatalf("err = %v", err)
		}

		if cfg.APIKey != "sk-from-flag" {
			t.Errorf("APIKey = %q, want sk-from-flag", cfg.APIKey)
		}
	})

	t.Run("no flag, no piped stdin → empty key (e.g. local Ollama)", func(t *testing.T) {
		t.Parallel()

		a := ProviderAdd{Name: "ollama", APIBase: "http://localhost:11434/v1"}

		cfg, _, err := a.nonInteractiveConfig(strings.NewReader(""), false)
		if err != nil {
			t.Fatalf("err = %v", err)
		}

		if cfg.APIKey != "" {
			t.Errorf("APIKey = %q, want empty", cfg.APIKey)
		}
	})

	t.Run("invalid name surfaces as validation error", func(t *testing.T) {
		t.Parallel()

		a := ProviderAdd{Name: "Bad-Name"}

		_, _, err := a.nonInteractiveConfig(strings.NewReader(""), false)
		if err == nil {
			t.Fatal("expected validation error for invalid name")
		}
	})
}

func TestProviderEdit_NonInteractive(t *testing.T) {
	t.Parallel()

	original := func() internal.ProvidersFile {
		return internal.ProvidersFile{
			Providers: []internal.ProviderConfig{
				{
					Name:    "anthropic",
					APIKey:  "sk-old",
					APIBase: "https://api.anthropic.com",
					Models:  []string{"claude-old"},
				},
			},
		}
	}

	t.Run("empty --name → falls back to interactive", func(t *testing.T) {
		t.Parallel()

		var e ProviderEdit

		_, ok, err := e.nonInteractiveEdit(original(), strings.NewReader(""), false)
		if ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})

	t.Run("unknown name surfaces as error", func(t *testing.T) {
		t.Parallel()

		e := ProviderEdit{Name: "missing"}

		_, _, err := e.nonInteractiveEdit(original(), strings.NewReader(""), false)
		if err == nil {
			t.Fatal("expected error for unknown provider")
		}
	})

	t.Run("empty flags preserve current values", func(t *testing.T) {
		t.Parallel()

		e := ProviderEdit{Name: "anthropic"}

		cfg, ok, err := e.nonInteractiveEdit(original(), strings.NewReader(""), false)
		if !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}

		if cfg.APIKey != "sk-old" || cfg.APIBase != "https://api.anthropic.com" {
			t.Errorf("non-empty preservation failed: %+v", cfg)
		}

		if !reflect.DeepEqual(cfg.Models, []string{"claude-old"}) {
			t.Errorf("models clobbered: %v", cfg.Models)
		}
	})

	t.Run("--api-key flag replaces", func(t *testing.T) {
		t.Parallel()

		e := ProviderEdit{Name: "anthropic", APIKey: "sk-new"}

		cfg, _, err := e.nonInteractiveEdit(original(), strings.NewReader(""), false)
		if err != nil {
			t.Fatalf("err=%v", err)
		}

		if cfg.APIKey != "sk-new" {
			t.Errorf("APIKey = %q, want sk-new", cfg.APIKey)
		}
	})

	t.Run("piped stdin replaces api-key when --api-key empty", func(t *testing.T) {
		t.Parallel()

		e := ProviderEdit{Name: "anthropic"}

		cfg, _, err := e.nonInteractiveEdit(original(), strings.NewReader("sk-from-stdin\n"), true)
		if err != nil {
			t.Fatalf("err=%v", err)
		}

		if cfg.APIKey != "sk-from-stdin" {
			t.Errorf("APIKey = %q", cfg.APIKey)
		}
	})

	t.Run("flag wins over piped stdin on api-key", func(t *testing.T) {
		t.Parallel()

		e := ProviderEdit{Name: "anthropic", APIKey: "sk-from-flag"}

		cfg, _, err := e.nonInteractiveEdit(original(), strings.NewReader("sk-from-stdin"), true)
		if err != nil {
			t.Fatalf("err=%v", err)
		}

		if cfg.APIKey != "sk-from-flag" {
			t.Errorf("APIKey = %q, want sk-from-flag", cfg.APIKey)
		}
	})

	t.Run("--models * clears the allow-list", func(t *testing.T) {
		t.Parallel()

		e := ProviderEdit{Name: "anthropic", Models: "*"}

		cfg, _, err := e.nonInteractiveEdit(original(), strings.NewReader(""), false)
		if err != nil {
			t.Fatalf("err=%v", err)
		}

		if cfg.Models != nil {
			t.Errorf("Models = %v, want nil", cfg.Models)
		}
	})

	t.Run("--models replaces the allow-list", func(t *testing.T) {
		t.Parallel()

		e := ProviderEdit{Name: "anthropic", Models: "claude-haiku-4-5, claude-sonnet-4-5"}

		cfg, _, err := e.nonInteractiveEdit(original(), strings.NewReader(""), false)
		if err != nil {
			t.Fatalf("err=%v", err)
		}

		want := []string{"claude-haiku-4-5", "claude-sonnet-4-5"}
		if !reflect.DeepEqual(cfg.Models, want) {
			t.Errorf("Models = %v, want %v", cfg.Models, want)
		}
	})
}

func TestModelsCSVFromConfig(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		modelsAllowAllSentinel: nil,
		"a, b":                 {"a", "b"},
		"only-one":             {"only-one"},
	}

	for want, in := range cases {
		if got := modelsCSVFromConfig(in); got != want {
			t.Errorf("modelsCSVFromConfig(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestProviderRm_NonInteractiveSelection(t *testing.T) {
	t.Parallel()

	file := internalProvidersFixture()

	t.Run("empty --name → falls back to interactive", func(t *testing.T) {
		t.Parallel()

		r := &ProviderRm{}

		_, ok, err := r.nonInteractiveSelection(file)
		if ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	})

	t.Run("repeated --name flags accumulate", func(t *testing.T) {
		t.Parallel()

		r := &ProviderRm{Name: []string{"anthropic", "openai"}}

		got, ok, err := r.nonInteractiveSelection(file)
		if !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}

		if !reflect.DeepEqual(got, []string{"anthropic", "openai"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("comma-separated single flag works", func(t *testing.T) {
		t.Parallel()

		r := &ProviderRm{Name: []string{"anthropic,openai"}}

		got, ok, err := r.nonInteractiveSelection(file)
		if !ok || err != nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}

		if !reflect.DeepEqual(got, []string{"anthropic", "openai"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("unknown name surfaces as error", func(t *testing.T) {
		t.Parallel()

		r := &ProviderRm{Name: []string{"missing"}}

		_, _, err := r.nonInteractiveSelection(file)
		if err == nil {
			t.Fatal("expected error for unknown provider")
		}
	})

	t.Run("only whitespace entries → error", func(t *testing.T) {
		t.Parallel()

		r := &ProviderRm{Name: []string{" , , "}}

		_, _, err := r.nonInteractiveSelection(file)
		if err == nil {
			t.Fatal("expected error for empty selection")
		}
	})
}

func TestRenderAPIKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key    string
		reveal bool
		want   string
	}{
		{"", false, "-"},
		{"$ANTHROPIC_API_KEY", false, "$ANTHROPIC_API_KEY"},
		{"sk-ant-fixture", true, "sk-ant-fixture"},
		{"short", false, "•••••"},
		{"sk-ant-very-long-secret-do-not-leak", false, "sk-ant•••••••••••••••••••••••••••••"},
	}

	for _, tc := range cases {
		if got := renderAPIKey(tc.key, tc.reveal); got != tc.want {
			t.Errorf("renderAPIKey(%q, %v) = %q, want %q", tc.key, tc.reveal, got, tc.want)
		}
	}
}
