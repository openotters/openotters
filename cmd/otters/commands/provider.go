package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/huh"
	"github.com/merlindorin/go-shared/pkg/cmd"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

// providerNameRE pins the shape we route on: lowercase ASCII +
// digits + hyphen, starting with a letter. Same charset that
// resolves the <name>/<model> agentfile prefix and the
// <NAME>_API_KEY env mapping the runtime expects.
var providerNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// providerPreset is one row in the form's preset Select. Models is
// the IDs Catwalk lists for the provider — surfaced verbatim in the
// details form so the user sees the real names available before they
// type into the `models:` allow-list field. Empty for the static
// fallback.
type providerPreset struct {
	ID      string
	Display string
	APIBase string
	Models  []string
}

//nolint:gochecknoglobals // immutable defaults table, used as a constant
var canonicalEndpoints = map[string]string{
	"anthropic": "https://api.anthropic.com",
	"openai":    "https://api.openai.com/v1",
}

func resolveAPIEndpoint(id, raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "$") {
		return raw
	}

	if expanded := os.ExpandEnv(raw); expanded != "" && expanded != raw {
		return expanded
	}

	if v, ok := canonicalEndpoints[id]; ok {
		return v
	}

	return ""
}

const customPresetSentinel = "__custom__"

const catwalkFormFetchTimeout = 5 * time.Second

//nolint:gochecknoglobals // immutable preset list, used as a constant
var staticProviderPresets = []providerPreset{
	{ID: "anthropic", Display: "Anthropic", APIBase: "https://api.anthropic.com"},
	{ID: "openai", Display: "OpenAI", APIBase: "https://api.openai.com/v1"},
	{ID: "openrouter", Display: "OpenRouter", APIBase: "https://openrouter.ai/api/v1"},
	{ID: "gemini", Display: "Google Gemini", APIBase: "https://generativelanguage.googleapis.com/v1beta/openai/"},
	{ID: "azure", Display: "Azure OpenAI", APIBase: ""},
	{ID: "bedrock", Display: "AWS Bedrock", APIBase: ""},
	{ID: "vercel", Display: "Vercel AI Gateway", APIBase: "https://ai-gateway.vercel.sh/v1"},
	{ID: "ollama", Display: "Ollama (local)", APIBase: "http://localhost:11434/v1"},
}

// catwalkTypeSupported gates which Catwalk provider types make it into
// the interactive `otters provider add` preset list. The set tracks
// what the runtime's createProvider switch can actually route — adding
// a type here that the runtime can't speak yields a configured-but-
// dead provider, which is worse than not surfacing it at all.
//
// Runtime support matrix (workspace/runtime/pkg/agent/agent.go):
//
//	TypeAnthropic     → fantasy/providers/anthropic    (native)
//	TypeOpenAI        → fantasy/providers/openai       (native)
//	TypeOpenAICompat  → fantasy/providers/openaicompat (default fallback)
//	TypeOpenRouter    → fantasy/providers/openrouter   (native)
//	TypeGoogle        → fantasy/providers/google       (native, Gemini API key path)
//	TypeAzure         → fantasy/providers/azure        (native)
//	TypeBedrock       → fantasy/providers/bedrock      (native)
//	TypeVercel        → fantasy/providers/vercel       (native)
//	TypeVertexAI      → ❌  needs WithVertex(project, location) not WithGeminiAPIKey;
//	                       leave excluded until the runtime exposes a
//	                       separate vertex slug with the right auth shape
func catwalkTypeSupported(t catwalk.Type) bool {
	switch t {
	case catwalk.TypeAnthropic,
		catwalk.TypeOpenAI,
		catwalk.TypeOpenAICompat,
		catwalk.TypeOpenRouter,
		catwalk.TypeGoogle,
		catwalk.TypeAzure,
		catwalk.TypeBedrock,
		catwalk.TypeVercel:
		return true
	case catwalk.TypeVertexAI:
		return false
	}

	return false
}

func fetchProviderPresets(ctx context.Context) []providerPreset {
	fetchCtx, cancel := context.WithTimeout(ctx, catwalkFormFetchTimeout)
	defer cancel()

	url := os.Getenv("OTTERS_CATWALK_URL")

	var client *catwalk.Client
	if url == "" {
		client = catwalk.NewWithURL("https://catwalk.charm.sh")
	} else {
		client = catwalk.NewWithURL(url)
	}

	providers, err := client.GetProviders(fetchCtx, "")
	if err != nil {
		return nil
	}

	out := make([]providerPreset, 0, len(providers))

	for _, p := range providers {
		if !catwalkTypeSupported(p.Type) {
			continue
		}

		id := string(p.ID)
		if !providerNameRE.MatchString(id) {
			continue
		}

		preset := providerPreset{
			ID:      id,
			Display: p.Name,
			APIBase: resolveAPIEndpoint(id, p.APIEndpoint),
		}

		for _, m := range p.Models {
			preset.Models = append(preset.Models, m.ID)
		}

		out = append(out, preset)
	}

	return out
}

func presetByID(presets []providerPreset, id string) *providerPreset {
	for i := range presets {
		if presets[i].ID == id {
			return &presets[i]
		}
	}

	return nil
}

// ProviderAdd talks to the daemon's AddProvider RPC instead of editing
// `~/.otters/providers.yaml` directly. The daemon owns the file; the
// CLI only carries form/flag input over the wire.
type ProviderAdd struct {
	Offline bool   `help:"Skip the Catwalk fetch and use the static preset list (anthropic / openai / openrouter / ollama). Interactive mode only." default:"false"`
	Name    string `help:"Provider name (lowercase, matches the <provider>/<model> prefix). Setting this skips the interactive form." default:""`
	APIKey  string `help:"API key. Goes through argv — pipe the key on stdin instead for secret-hygiene in scripts." default:""`
	APIBase string `help:"API base URL. Optional — leave empty to use the upstream provider default." default:""`
	Models  string `help:"Comma-separated allow-list of model IDs. Empty or '*' allows any." default:""`
}

func (a *ProviderAdd) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	cfg, ok, err := a.nonInteractiveConfig(os.Stdin, stdinIsPiped())
	if err != nil {
		return err
	}

	if !ok {
		cfg, ok, err = a.interactiveConfig(ctx)
		if err != nil {
			return err
		}

		if !ok {
			return nil
		}
	}

	// AddProvider rejects duplicates with codes.AlreadyExists; fall back
	// to UpdateProvider so `provider add` continues to behave as
	// "ensure-this-config-is-present" the way the file-direct version
	// did via UpsertProvider.
	verb := "added"
	if _, addErr := c.AddProvider(ctx, &daemonv1.AddProviderRequest{Provider: cfg}); addErr != nil {
		if isAlreadyExists(addErr) {
			if _, upErr := c.UpdateProvider(ctx, &daemonv1.UpdateProviderRequest{Provider: cfg}); upErr != nil {
				return fmt.Errorf("update provider: %w", unwrapRPC(upErr))
			}

			verb = "updated"
		} else {
			return fmt.Errorf("add provider: %w", unwrapRPC(addErr))
		}
	}

	_, _ = common.Printer().Printf("%s provider %q\n", verb, cfg.GetName())

	return nil
}

func (a *ProviderAdd) nonInteractiveConfig(stdin io.Reader, stdinPiped bool) (*daemonv1.Provider, bool, error) {
	if a.Name == "" {
		return nil, false, nil
	}

	if err := validateProviderName(a.Name); err != nil {
		return nil, false, fmt.Errorf("--name %q: %w", a.Name, err)
	}

	apiKey := a.APIKey

	if apiKey == "" && stdinPiped {
		raw, readErr := io.ReadAll(stdin)
		if readErr != nil {
			return nil, false, fmt.Errorf("reading api-key from stdin: %w", readErr)
		}

		apiKey = strings.TrimRight(string(raw), "\r\n \t")
	}

	return &daemonv1.Provider{
		Name:    a.Name,
		ApiKey:  apiKey,
		ApiBase: a.APIBase,
		Models:  parseModelsCSV(a.Models),
	}, true, nil
}

func stdinIsPiped() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice == 0
}

func (a *ProviderAdd) interactiveConfig(ctx context.Context) (*daemonv1.Provider, bool, error) {
	presets := staticProviderPresets

	if !a.Offline {
		if fetched := fetchProviderPresets(ctx); len(fetched) > 0 {
			presets = fetched
		} else {
			_, _ = fmt.Fprintln(os.Stderr,
				"warning: could not reach catwalk; falling back to the static preset list")
		}
	}

	preset, err := runPresetForm(presets)
	if err != nil {
		return nil, false, err
	}

	if preset == "" {
		return nil, false, nil
	}

	fields := defaultsForPreset(presets, preset)

	if runErr := buildDetailsForm(fields).Run(); runErr != nil {
		if errors.Is(runErr, huh.ErrUserAborted) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("provider add details form: %w", runErr)
	}

	return fields.toProvider(), true, nil
}

type addFormFields struct {
	name            string
	apiKey          string
	apiBase         string
	modelsCSV       string
	availableModels []string
}

// toProvider produces the proto Provider sent to the daemon. The form
// captures everything as plain strings; any normalisation (trim,
// lowercase) happens here so the daemon receives a clean payload.
func (f *addFormFields) toProvider() *daemonv1.Provider {
	return &daemonv1.Provider{
		Name:    strings.TrimSpace(f.name),
		ApiKey:  strings.TrimSpace(f.apiKey),
		ApiBase: strings.TrimSpace(f.apiBase),
		Models:  parseModelsCSV(f.modelsCSV),
	}
}

const modelsAllowAllSentinel = "*"

func modelsFilterDescription(available []string) string {
	const trailer = "`*` or empty allows any model the provider serves."

	if len(available) == 0 {
		return "Comma-separated allow-list (e.g. `claude-sonnet-4-5, claude-haiku-4-5`). " + trailer
	}

	return "Comma-separated allow-list. Available: " +
		strings.Join(available, ", ") + ". " + trailer
}

func runPresetForm(presets []providerPreset) (string, error) {
	presetOpts := make([]huh.Option[string], 0, len(presets)+1)
	for _, p := range presets {
		label := p.Display
		if label == "" {
			label = p.ID
		}

		presetOpts = append(presetOpts, huh.NewOption(label, p.ID))
	}

	presetOpts = append(presetOpts, huh.NewOption("Custom (enter name + base manually)", customPresetSentinel))

	var preset string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Pick a provider preset").
				Options(presetOpts...).
				Value(&preset),
		),
	)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}

		return "", fmt.Errorf("provider add preset form: %w", err)
	}

	return preset, nil
}

func defaultsForPreset(presets []providerPreset, preset string) *addFormFields {
	if preset == customPresetSentinel {
		return &addFormFields{modelsCSV: modelsAllowAllSentinel}
	}

	if p := presetByID(presets, preset); p != nil {
		return &addFormFields{
			name:            p.ID,
			apiBase:         p.APIBase,
			modelsCSV:       modelsAllowAllSentinel,
			availableModels: p.Models,
		}
	}

	return &addFormFields{modelsCSV: modelsAllowAllSentinel}
}

func buildDetailsForm(fields *addFormFields) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Name").
				Description("Lowercase id used as the <provider>/<model> prefix.").
				Value(&fields.name).
				Validate(func(s string) error { return validateProviderName(strings.TrimSpace(s)) }),
			huh.NewInput().
				Title("API base URL").
				Description("Empty uses the upstream default.").
				Value(&fields.apiBase),
			huh.NewInput().
				Title("API key").
				Description("Plain value or `${ENV_VAR}` reference.").
				Value(&fields.apiKey).
				EchoMode(huh.EchoModePassword),
			huh.NewInput().
				Title("Models filter").
				Description(modelsFilterDescription(fields.availableModels)).
				Value(&fields.modelsCSV),
		),
	)
}

func validateProviderName(s string) error {
	if s == "" {
		return errors.New("required")
	}

	if !providerNameRE.MatchString(s) {
		return errors.New("must be lowercase letters, digits, hyphens; first char a letter")
	}

	return nil
}

func parseModelsCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == modelsAllowAllSentinel {
		return nil
	}

	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}

	return out
}

// ProviderEdit calls UpdateProvider after merging flag/form input on
// top of the daemon's current state — partial updates stay scriptable
// because we ListProviders first to find the row and only overwrite
// fields the user actually set.
type ProviderEdit struct {
	Name    string `help:"Provider to edit. Setting this skips the interactive picker." default:""`
	APIKey  string `help:"New API key. Empty + piped stdin reads the key from stdin; empty + no stdin keeps the current value." default:""`
	APIBase string `help:"New API base URL. Empty keeps the current value." default:""`
	Models  string `help:"New comma-separated allow-list. Empty keeps the current value; '*' clears any existing allow-list." default:""`
}

func (e *ProviderEdit) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	listResp, err := c.ListProviders(ctx, &daemonv1.ListProvidersRequest{})
	if err != nil {
		return fmt.Errorf("list providers: %w", unwrapRPC(err))
	}

	providers := listResp.GetProviders()

	if len(providers) == 0 {
		_, _ = common.Printer().Println("no providers configured (run `otters provider add`)")

		return nil
	}

	cfg, ok, err := e.nonInteractiveEdit(providers, os.Stdin, stdinIsPiped())
	if err != nil {
		return err
	}

	if !ok {
		cfg, ok, err = e.interactiveEdit(providers)
		if err != nil {
			return err
		}

		if !ok {
			return nil
		}
	}

	if _, upErr := c.UpdateProvider(ctx, &daemonv1.UpdateProviderRequest{Provider: cfg}); upErr != nil {
		return fmt.Errorf("update provider: %w", unwrapRPC(upErr))
	}

	_, _ = common.Printer().Printf("updated provider %q\n", cfg.GetName())

	return nil
}

func (e *ProviderEdit) nonInteractiveEdit(
	providers []*daemonv1.Provider, stdin io.Reader, stdinPiped bool,
) (*daemonv1.Provider, bool, error) {
	if e.Name == "" {
		return nil, false, nil
	}

	current := findProviderByName(providers, e.Name)
	if current == nil {
		return nil, false, fmt.Errorf("provider %q not configured", e.Name)
	}

	cfg := cloneProvider(current)

	if e.APIKey != "" {
		cfg.ApiKey = e.APIKey
	} else if stdinPiped {
		raw, readErr := io.ReadAll(stdin)
		if readErr != nil {
			return nil, false, fmt.Errorf("reading api-key from stdin: %w", readErr)
		}

		if trimmed := strings.TrimRight(string(raw), "\r\n \t"); trimmed != "" {
			cfg.ApiKey = trimmed
		}
	}

	if e.APIBase != "" {
		cfg.ApiBase = e.APIBase
	}

	switch {
	case e.Models == "":
		// keep
	case strings.TrimSpace(e.Models) == modelsAllowAllSentinel:
		cfg.Models = nil
	default:
		cfg.Models = parseModelsCSV(e.Models)
	}

	return cfg, true, nil
}

func (e *ProviderEdit) interactiveEdit(providers []*daemonv1.Provider) (*daemonv1.Provider, bool, error) {
	target, err := pickProvider(providers)
	if err != nil {
		return nil, false, err
	}

	if target == "" {
		return nil, false, nil
	}

	current := findProviderByName(providers, target)
	if current == nil {
		return nil, false, fmt.Errorf("provider %q vanished mid-edit", target)
	}

	fields := &addFormFields{
		name:      current.GetName(),
		apiKey:    current.GetApiKey(),
		apiBase:   current.GetApiBase(),
		modelsCSV: modelsCSVFromList(current.GetModels()),
	}

	if runErr := buildDetailsForm(fields).Run(); runErr != nil {
		if errors.Is(runErr, huh.ErrUserAborted) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("provider edit details form: %w", runErr)
	}

	return fields.toProvider(), true, nil
}

func pickProvider(providers []*daemonv1.Provider) (string, error) {
	opts := make([]huh.Option[string], 0, len(providers))
	for _, p := range providers {
		label := p.GetName()
		if p.GetApiBase() != "" {
			label = p.GetName() + "  (" + p.GetApiBase() + ")"
		}

		opts = append(opts, huh.NewOption(label, p.GetName()))
	}

	var selected string
	if len(providers) > 0 {
		selected = providers[0].GetName()
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Edit which provider?").
				Options(opts...).
				Value(&selected),
		),
	)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}

		return "", fmt.Errorf("provider edit picker: %w", err)
	}

	return selected, nil
}

func modelsCSVFromList(models []string) string {
	if len(models) == 0 {
		return modelsAllowAllSentinel
	}

	return strings.Join(models, ", ")
}

// ProviderRm calls RemoveProvider once per selected entry. The
// previous implementation removed multiple in-memory and re-wrote the
// file in one shot; the daemon's RPC is single-name, so we loop. The
// daemon serialises writes internally so two CLI invocations racing
// are still safe.
type ProviderRm struct {
	Name []string `help:"Provider name(s) to remove. Repeat the flag or comma-separate. Setting this skips the interactive form." default:""`
}

func (r *ProviderRm) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	listResp, err := c.ListProviders(ctx, &daemonv1.ListProvidersRequest{})
	if err != nil {
		return fmt.Errorf("list providers: %w", unwrapRPC(err))
	}

	providers := listResp.GetProviders()

	if len(providers) == 0 {
		_, _ = common.Printer().Println("no providers configured")

		return nil
	}

	selected, ok, err := r.nonInteractiveSelection(providers)
	if err != nil {
		return err
	}

	if !ok {
		selected, ok, err = r.interactiveSelection(providers)
		if err != nil {
			return err
		}

		if !ok {
			_, _ = common.Printer().Println("cancelled")

			return nil
		}
	}

	removed := 0

	for _, name := range selected {
		if _, rmErr := c.RemoveProvider(ctx, &daemonv1.RemoveProviderRequest{Name: name}); rmErr != nil {
			return fmt.Errorf("remove provider %q: %w", name, unwrapRPC(rmErr))
		}

		removed++
	}

	_, _ = common.Printer().Printf("removed %d provider(s)\n", removed)

	return nil
}

func (r *ProviderRm) nonInteractiveSelection(providers []*daemonv1.Provider) ([]string, bool, error) {
	if len(r.Name) == 0 {
		return nil, false, nil
	}

	known := make(map[string]struct{}, len(providers))
	for _, p := range providers {
		known[p.GetName()] = struct{}{}
	}

	out := make([]string, 0, len(r.Name))

	for _, raw := range r.Name {
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}

			if _, ok := known[name]; !ok {
				return nil, false, fmt.Errorf("provider %q not configured", name)
			}

			out = append(out, name)
		}
	}

	if len(out) == 0 {
		return nil, false, errors.New("--name produced no valid entries")
	}

	return out, true, nil
}

func (r *ProviderRm) interactiveSelection(providers []*daemonv1.Provider) ([]string, bool, error) {
	opts := make([]huh.Option[string], 0, len(providers))
	for _, p := range providers {
		label := p.GetName()
		if p.GetApiBase() != "" {
			label = p.GetName() + "  (" + p.GetApiBase() + ")"
		}

		opts = append(opts, huh.NewOption(label, p.GetName()))
	}

	var (
		selected  []string
		confirmed bool
	)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Remove which providers?").
				Description("Toggle with space, confirm with enter.").
				Options(opts...).
				Value(&selected).
				Validate(func(v []string) error {
					if len(v) == 0 {
						return errors.New("select at least one")
					}

					return nil
				}),
		),
		huh.NewGroup(
			huh.NewConfirm().
				TitleFunc(func() string {
					return fmt.Sprintf("Remove %d provider(s)?", len(selected))
				}, &selected).
				Affirmative("Remove").
				Negative("Cancel").
				Value(&confirmed),
		),
	)

	if runErr := form.Run(); runErr != nil {
		if errors.Is(runErr, huh.ErrUserAborted) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("provider rm form: %w", runErr)
	}

	if !confirmed {
		return nil, false, nil
	}

	return selected, true, nil
}

// ProviderLs prints the configured providers as a table — the secret
// column is masked unless --reveal is set.
type ProviderLs struct {
	Reveal bool `help:"Show full api-key values instead of a length-only mask." default:"false"`
}

func (l *ProviderLs) Run(ctx context.Context, common *cmd.Commons, d *Daemon) error {
	c, conn, err := d.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	listResp, err := c.ListProviders(ctx, &daemonv1.ListProvidersRequest{})
	if err != nil {
		return fmt.Errorf("list providers: %w", unwrapRPC(err))
	}

	providers := listResp.GetProviders()

	if len(providers) == 0 {
		_, _ = common.Printer().Println("no providers configured (run `otters provider add`)")

		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "NAME\tAPI BASE\tMODELS\tAPI KEY")

	for _, p := range providers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			p.GetName(),
			fallback(p.GetApiBase(), "-"),
			modelsCell(p.GetModels()),
			renderAPIKey(p.GetApiKey(), l.Reveal),
		)
	}

	return w.Flush()
}

func modelsCell(models []string) string {
	if len(models) == 0 {
		return "*"
	}

	return strings.Join(models, ", ")
}

func renderAPIKey(key string, reveal bool) string {
	if key == "" {
		return "-"
	}

	if reveal {
		return key
	}

	if strings.HasPrefix(key, "$") {
		return key
	}

	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}

	return key[:6] + strings.Repeat("•", len(key)-6)
}

// findProviderByName scans a flat slice for an exact name match. Used
// by ProviderEdit / ProviderRm to validate a user-supplied --name
// before sending an UpdateProvider / RemoveProvider RPC. The daemon
// also enforces existence (returning CodeNotFound), but a client-side
// check produces a tighter error message and avoids a round trip on
// typos.
func findProviderByName(providers []*daemonv1.Provider, name string) *daemonv1.Provider {
	for _, p := range providers {
		if p.GetName() == name {
			return p
		}
	}

	return nil
}

// cloneProvider returns a value copy with the Models slice deep-copied
// so the caller can mutate fields without aliasing the slice from the
// daemon's response. Maps and proto-internal state aren't copied; we
// treat the returned proto strictly as a transport DTO from here.
func cloneProvider(p *daemonv1.Provider) *daemonv1.Provider {
	models := make([]string, len(p.GetModels()))
	copy(models, p.GetModels())

	return &daemonv1.Provider{
		Name:    p.GetName(),
		ApiKey:  p.GetApiKey(),
		ApiBase: p.GetApiBase(),
		Models:  models,
	}
}

// isAlreadyExists reports whether err is the daemon's
// CodeAlreadyExists response. ProviderAdd uses this to fall through to
// UpdateProvider, preserving the file-direct version's
// "ensure-this-config-is-present" semantics. Decoded via
// google.golang.org/grpc/status because the CLI dials gRPC; a Connect
// daemon returning CodeAlreadyExists serializes to the same gRPC code.
func isAlreadyExists(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if msg := strings.ToLower(err.Error()); strings.Contains(msg, "already exists") {
			return true
		}
	}

	return false
}
