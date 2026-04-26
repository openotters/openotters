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

	"github.com/openotters/openotters/internal"
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
	ID      string // canonical lowercase id (becomes the `name:` field)
	Display string // human label shown in the Select
	APIBase string // pre-filled api-base
	Models  []string
}

// canonicalEndpoints supplies a real URL for catwalk providers that
// declare api_endpoint as `$VAR`. We try the env var first; when it's
// unset we fall back to this hardcoded list rather than letting a
// raw `$OPENAI_API_ENDPOINT` placeholder land in providers.yaml. Only
// providers our runtime supports need entries here — Azure / Vertex /
// Bedrock / Google / Vercel are filtered out upstream.
//
//nolint:gochecknoglobals // immutable defaults table, used as a constant
var canonicalEndpoints = map[string]string{
	"anthropic": "https://api.anthropic.com",
	"openai":    "https://api.openai.com/v1",
}

// resolveAPIEndpoint substitutes catwalk's `$VAR` indirection with a
// concrete URL. Falls back to the canonicalEndpoints map when the env
// var is unset, then to the empty string (which lets the runtime SDK
// pick its default).
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

// catwalkFetchTimeout caps how long the form waits for Catwalk
// before falling back to the static preset list. Five seconds is
// long enough to ride out a slow first connection without making
// `provider add` feel hung when the user is offline.
const catwalkFormFetchTimeout = 5 * time.Second

// staticProviderPresets is the offline fallback used when Catwalk
// can't be reached. Covers the four providers most users will hit
// without a network round-trip.
//
//nolint:gochecknoglobals // immutable preset list, used as a constant
var staticProviderPresets = []providerPreset{
	{ID: "anthropic", Display: "Anthropic", APIBase: "https://api.anthropic.com"},
	{ID: "openai", Display: "OpenAI", APIBase: "https://api.openai.com/v1"},
	{ID: "openrouter", Display: "OpenRouter", APIBase: "https://openrouter.ai/api/v1"},
	{ID: "ollama", Display: "Ollama (local)", APIBase: "http://localhost:11434/v1"},
}

// catwalkTypeSupported says whether the openotters runtime can
// actually drive a provider of the given catwalk type. Anthropic
// native, OpenAI native, the openai-compat fallback (covers Groq,
// Together, DeepSeek, xAI, Mistral, Cerebras, etc.), and the
// OpenRouter facade all map onto an adapter in
// workspace/runtime/pkg/agent/agent.go:createProvider. Bedrock,
// Vertex, Azure, Google, Vercel have provider-specific auth flows
// the runtime hasn't implemented yet — surface them in the form
// would let the user save a config that fails at agent-start time.
// Switch this to a switch (rather than a map) so exhaustive linting
// catches new catwalk types we haven't decided on yet.
func catwalkTypeSupported(t catwalk.Type) bool {
	switch t {
	case catwalk.TypeAnthropic,
		catwalk.TypeOpenAI,
		catwalk.TypeOpenAICompat,
		catwalk.TypeOpenRouter:
		return true
	case catwalk.TypeAzure,
		catwalk.TypeBedrock,
		catwalk.TypeGoogle,
		catwalk.TypeVercel,
		catwalk.TypeVertexAI:
		return false
	default:
		return false
	}
}

// fetchProviderPresets pulls the live provider list from Catwalk and
// filters to the types our runtime supports. Network failures are
// silent — the caller falls back to staticProviderPresets and the
// form still works offline.
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
			// Skip provider IDs that wouldn't pass our naming rule —
			// they couldn't be saved to providers.yaml anyway.
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

// presetByID returns the preset matching id, or nil. Lets the form
// look up the selected preset's defaults without re-iterating.
func presetByID(presets []providerPreset, id string) *providerPreset {
	for i := range presets {
		if presets[i].ID == id {
			return &presets[i]
		}
	}

	return nil
}

// ProviderAdd appends a new entry to ~/.otters/providers.yaml. Two
// modes:
//
//   - Interactive (default): a two-form Catwalk-driven preset picker
//     plus a details form. Used when no `--name` flag is given.
//
//   - Non-interactive: when `--name` is set, every field is taken
//     from flags so the command is scriptable. The api-key can come
//     from --api-key (visible in argv) or, when stdin is piped, from
//     stdin (no flag required) — secret-hygiene-friendly:
//
//     echo $ANTHROPIC_KEY | otters provider add \
//     --name anthropic --api-base https://api.anthropic.com
//
// Reuses the daemon's lazy-reload contract: no daemon restart
// required — the running ottersd picks the new provider up on the
// next call to Resolve / Each.
type ProviderAdd struct {
	// Interactive-only knob.
	Offline bool `help:"Skip the Catwalk fetch and use the static preset list (anthropic / openai / openrouter / ollama). Interactive mode only." default:"false"`

	// Non-interactive flags. Setting --name switches to script mode.
	// The api-key can come from --api-key (visible in argv) or from
	// stdin when stdin is piped — `echo $KEY | otters provider add
	// --name anthropic` uses the piped value automatically, no extra
	// flag needed.
	Name    string `help:"Provider name (lowercase, matches the <provider>/<model> prefix). Setting this skips the interactive form." default:""`
	APIKey  string `help:"API key. Goes through argv — pipe the key on stdin instead for secret-hygiene in scripts." default:""`
	APIBase string `help:"API base URL. Optional — leave empty to use the upstream provider default." default:""`
	Models  string `help:"Comma-separated allow-list of model IDs. Empty or '*' allows any." default:""`
}

func (a *ProviderAdd) Run(ctx context.Context, common *cmd.Commons) error {
	path, err := internal.DefaultProvidersPath()
	if err != nil {
		return err
	}

	file, err := internal.ReadProvidersFile(path)
	if err != nil {
		return err
	}

	cfg, ok, err := a.nonInteractiveConfig(os.Stdin, stdinIsPiped())
	if err != nil {
		return err
	}

	if !ok {
		// Interactive flow.
		cfg, ok, err = a.interactiveConfig(ctx)
		if err != nil {
			return err
		}

		if !ok {
			return nil // user aborted
		}
	}

	replaced := internal.UpsertProvider(&file, cfg)

	if writeErr := internal.WriteProvidersFile(path, file); writeErr != nil {
		return writeErr
	}

	verb := "added"
	if replaced {
		verb = "updated"
	}

	_, _ = common.Printer().Printf("%s provider %q (%s)\n", verb, cfg.Name, path)

	return nil
}

// nonInteractiveConfig returns the parsed config when --name is set.
// The api-key resolves in this order:
//   - --api-key flag value, when non-empty
//   - stdin contents (with trailing whitespace trimmed) when stdin
//     is piped (not a TTY) and the flag is empty
//   - empty otherwise (the caller can leave the field blank for
//     keyless providers like local Ollama)
//
// Returns (cfg, false, nil) when --name is empty so the caller falls
// back to the interactive form.
func (a *ProviderAdd) nonInteractiveConfig(stdin io.Reader, stdinPiped bool) (internal.ProviderConfig, bool, error) {
	if a.Name == "" {
		return internal.ProviderConfig{}, false, nil
	}

	if err := validateProviderName(a.Name); err != nil {
		return internal.ProviderConfig{}, false, fmt.Errorf("--name %q: %w", a.Name, err)
	}

	apiKey := a.APIKey

	if apiKey == "" && stdinPiped {
		raw, readErr := io.ReadAll(stdin)
		if readErr != nil {
			return internal.ProviderConfig{}, false, fmt.Errorf("reading api-key from stdin: %w", readErr)
		}
		// Trim only trailing whitespace/newlines so an explicit
		// `echo $KEY | …` (which appends a newline) works without
		// fuss. Leading whitespace would be a real typo and stays.
		apiKey = strings.TrimRight(string(raw), "\r\n \t")
	}

	cfg := internal.ProviderConfig{
		Name:    a.Name,
		APIKey:  apiKey,
		APIBase: a.APIBase,
		Models:  parseModelsCSV(a.Models),
	}

	return cfg, true, nil
}

// stdinIsPiped reports whether os.Stdin is connected to a pipe or
// file (i.e. another process is feeding it) rather than a TTY. Used
// to auto-detect "the user piped the api-key in" without requiring a
// dedicated flag.
func stdinIsPiped() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice == 0
}

// interactiveConfig drives the two-form Catwalk picker → details
// flow. Returns (cfg, true, nil) on submit, (zero, false, nil) on
// user abort.
func (a *ProviderAdd) interactiveConfig(ctx context.Context) (internal.ProviderConfig, bool, error) {
	presets := staticProviderPresets

	if !a.Offline {
		if fetched := fetchProviderPresets(ctx); len(fetched) > 0 {
			presets = fetched
		} else {
			_, _ = fmt.Fprintln(os.Stderr,
				"warning: could not reach catwalk; falling back to the static preset list")
		}
	}

	// Two-form flow: pick the preset first, then build the details
	// form with that preset's defaults baked into the Input fields'
	// initial values. huh.Input.Value(&p) snapshots *p at field
	// construction, so any in-between mutation (e.g. via WithHideFunc)
	// would not appear in the rendered buffer — building the form
	// after the preset is known sidesteps that entirely.
	preset, err := runPresetForm(presets)
	if err != nil {
		return internal.ProviderConfig{}, false, err
	}

	if preset == "" {
		return internal.ProviderConfig{}, false, nil
	}

	fields := defaultsForPreset(presets, preset)

	if runErr := buildDetailsForm(fields).Run(); runErr != nil {
		if errors.Is(runErr, huh.ErrUserAborted) {
			return internal.ProviderConfig{}, false, nil
		}

		return internal.ProviderConfig{}, false, fmt.Errorf("provider add details form: %w", runErr)
	}

	return fields.toConfig(), true, nil
}

// addFormFields collects the bound variables for the details form
// (form 2). The preset itself is captured by form 1's runPresetForm
// and not bound here. availableModels is the list of model IDs the
// chosen preset advertises (from Catwalk); rendered as a description
// hint on the modelsCSV input — empty for custom / unknown presets.
type addFormFields struct {
	name            string
	apiKey          string
	apiBase         string
	modelsCSV       string
	availableModels []string
}

func (f *addFormFields) toConfig() internal.ProviderConfig {
	return internal.ProviderConfig{
		Name:    strings.TrimSpace(f.name),
		APIKey:  strings.TrimSpace(f.apiKey),
		APIBase: strings.TrimSpace(f.apiBase),
		Models:  parseModelsCSV(f.modelsCSV),
	}
}

// modelsAllowAllSentinel is the visible "allow any model" value the
// form pre-fills into the models input. parseModelsCSV treats it as
// equivalent to empty so the saved providers.yaml gets no `models:`
// allow-list (matches the registry's "no filter = allow any" rule).
const modelsAllowAllSentinel = "*"

// modelsFilterDescription renders the helper text shown under the
// "Models filter" input. When the picked preset advertises a model
// list, every ID is spliced in so the user can copy any one verbatim
// — no need to remember names or check a doc page. Falls back to a
// generic example for the custom / unknown branch.
func modelsFilterDescription(available []string) string {
	const trailer = "`*` or empty allows any model the provider serves."

	if len(available) == 0 {
		return "Comma-separated allow-list (e.g. `claude-sonnet-4-5, claude-haiku-4-5`). " + trailer
	}

	return "Comma-separated allow-list. Available: " +
		strings.Join(available, ", ") + ". " + trailer
}

// runPresetForm shows the preset Select and returns the chosen ID
// (or customPresetSentinel for the "I'll type everything" branch).
// Returns "" when the user aborts (Esc / Ctrl-C).
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
	if len(presets) > 0 {
		preset = presets[0].ID
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Provider preset").
				Description("Pre-fills name + api-base for the next step. Pick `Custom` to enter both manually.").
				Options(presetOpts...).
				Value(&preset),
		),
	)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}

		return "", fmt.Errorf("provider preset form: %w", err)
	}

	return preset, nil
}

// defaultsForPreset returns an addFormFields seeded from the preset
// the user picked. Custom / unknown presets leave name + apiBase blank
// for the user to type, but every path lands modelsCSV at the
// "allow-all" sentinel so the third Input is never empty on first
// render.
func defaultsForPreset(presets []providerPreset, preset string) *addFormFields {
	fields := &addFormFields{modelsCSV: modelsAllowAllSentinel}

	if preset == "" || preset == customPresetSentinel {
		return fields
	}

	if p := presetByID(presets, preset); p != nil {
		fields.name = p.ID
		fields.apiBase = p.APIBase
		fields.availableModels = p.Models
	}

	return fields
}

// buildDetailsForm composes the second form: name / api-key / api-base
// / models. Inputs read fields' values at construction time (huh
// snapshots the bound pointer's value into its internal textinput
// buffer), so callers MUST seed fields with the right defaults before
// calling this. defaultsForPreset is the canonical seeder.
func buildDetailsForm(fields *addFormFields) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Provider name").
				Description("Lowercase identifier; matches the <provider>/<model> prefix in agentfiles.").
				Value(&fields.name).
				Validate(validateProviderName),

			huh.NewInput().
				Title("API key").
				Description("Stored in ~/.otters/providers.yaml. Leave blank for providers without "+
					"auth (e.g. local Ollama). Use $ENV_VAR to defer to the environment.").
				EchoMode(huh.EchoModePassword).
				Value(&fields.apiKey),

			huh.NewInput().
				Title("API base URL").
				Description("Override the upstream endpoint. Leave the suggested default unless you're using a proxy.").
				Value(&fields.apiBase),

			huh.NewInput().
				Title("Models filter (optional)").
				Description(modelsFilterDescription(fields.availableModels)).
				Value(&fields.modelsCSV),
		),
	)
}

func validateProviderName(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("required")
	}

	if !providerNameRE.MatchString(s) {
		return errors.New("must be lowercase letters, digits, or '-' (start with a letter)")
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
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// ProviderEdit updates an existing entry in
// ~/.otters/providers.yaml. Two modes:
//
//   - Interactive (default): a Select picks the provider to edit, then
//     a details form opens pre-populated with the current values so
//     the user can keep, change, or clear each field.
//
//   - Non-interactive: --name selects the provider; any other flag
//     left empty preserves the current value, so partial updates are
//     scriptable. The api-key can come from --api-key or, when stdin
//     is piped and --api-key is empty, from stdin:
//
//     echo $NEW_KEY | otters provider edit --name anthropic
//
// Errors when --name doesn't match an existing provider — catches
// typos in scripts. To clear an api-base or models filter, pass the
// empty string explicitly via the interactive form (or remove +
// re-add via `provider rm` / `provider add`).
type ProviderEdit struct {
	Name    string `help:"Provider to edit. Setting this skips the interactive picker." default:""`
	APIKey  string `help:"New API key. Empty + piped stdin reads the key from stdin; empty + no stdin keeps the current value." default:""`
	APIBase string `help:"New API base URL. Empty keeps the current value." default:""`
	Models  string `help:"New comma-separated allow-list. Empty keeps the current value; '*' clears any existing allow-list." default:""`
}

func (e *ProviderEdit) Run(_ context.Context, common *cmd.Commons) error {
	path, err := internal.DefaultProvidersPath()
	if err != nil {
		return err
	}

	file, err := internal.ReadProvidersFile(path)
	if err != nil {
		return err
	}

	if len(file.Providers) == 0 {
		_, _ = common.Printer().Println("no providers configured (run `otters provider add`)")

		return nil
	}

	cfg, ok, err := e.nonInteractiveEdit(file, os.Stdin, stdinIsPiped())
	if err != nil {
		return err
	}

	if !ok {
		cfg, ok, err = e.interactiveEdit(file)
		if err != nil {
			return err
		}

		if !ok {
			return nil // user aborted
		}
	}

	internal.UpsertProvider(&file, cfg)

	if writeErr := internal.WriteProvidersFile(path, file); writeErr != nil {
		return writeErr
	}

	_, _ = common.Printer().Printf("updated provider %q (%s)\n", cfg.Name, path)

	return nil
}

// nonInteractiveEdit returns the patched config when --name is set,
// preserving any field whose flag was left empty. Returns (zero,
// false, nil) when --name is unset so the caller drops to the
// interactive picker.
func (e *ProviderEdit) nonInteractiveEdit(
	file internal.ProvidersFile, stdin io.Reader, stdinPiped bool,
) (internal.ProviderConfig, bool, error) {
	if e.Name == "" {
		return internal.ProviderConfig{}, false, nil
	}

	current := internal.FindProvider(file, e.Name)
	if current == nil {
		return internal.ProviderConfig{}, false, fmt.Errorf("provider %q not configured", e.Name)
	}

	cfg := *current

	if e.APIKey != "" {
		cfg.APIKey = e.APIKey
	} else if stdinPiped {
		raw, readErr := io.ReadAll(stdin)
		if readErr != nil {
			return internal.ProviderConfig{}, false, fmt.Errorf("reading api-key from stdin: %w", readErr)
		}

		if trimmed := strings.TrimRight(string(raw), "\r\n \t"); trimmed != "" {
			cfg.APIKey = trimmed
		}
	}

	if e.APIBase != "" {
		cfg.APIBase = e.APIBase
	}

	// Models: empty flag = keep current; "*" = clear any existing
	// allow-list (matches the form's sentinel); anything else =
	// replace.
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

// interactiveEdit picks a provider then opens the details form
// pre-populated with the current values. Returns (zero, false, nil)
// on user abort.
func (e *ProviderEdit) interactiveEdit(
	file internal.ProvidersFile,
) (internal.ProviderConfig, bool, error) {
	target, err := pickProvider(file)
	if err != nil {
		return internal.ProviderConfig{}, false, err
	}

	if target == "" {
		return internal.ProviderConfig{}, false, nil
	}

	current := internal.FindProvider(file, target)
	if current == nil {
		// Selected from the options we just built — should never miss.
		return internal.ProviderConfig{}, false, fmt.Errorf("provider %q vanished mid-edit", target)
	}

	fields := &addFormFields{
		name:      current.Name,
		apiKey:    current.APIKey,
		apiBase:   current.APIBase,
		modelsCSV: modelsCSVFromConfig(current.Models),
	}

	if runErr := buildDetailsForm(fields).Run(); runErr != nil {
		if errors.Is(runErr, huh.ErrUserAborted) {
			return internal.ProviderConfig{}, false, nil
		}

		return internal.ProviderConfig{}, false, fmt.Errorf("provider edit details form: %w", runErr)
	}

	return fields.toConfig(), true, nil
}

// pickProvider runs a single-step Select form over the configured
// providers. Returns "" on user abort.
func pickProvider(file internal.ProvidersFile) (string, error) {
	opts := make([]huh.Option[string], 0, len(file.Providers))
	for _, p := range file.Providers {
		label := p.Name
		if p.APIBase != "" {
			label = p.Name + "  (" + p.APIBase + ")"
		}

		opts = append(opts, huh.NewOption(label, p.Name))
	}

	var selected string
	if len(file.Providers) > 0 {
		selected = file.Providers[0].Name
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

// modelsCSVFromConfig formats a stored Models slice for the details
// form's modelsCSV input. Empty allow-list (= "any model") shows up
// as the explicit "*" sentinel rather than blank, keeping the input
// in sync with the form's pre-fill convention used by ProviderAdd.
func modelsCSVFromConfig(models []string) string {
	if len(models) == 0 {
		return modelsAllowAllSentinel
	}

	return strings.Join(models, ", ")
}

// ProviderRm removes one or more providers from
// ~/.otters/providers.yaml. Two modes:
//
//   - Interactive (default): a multi-select form over the configured
//     providers, plus a confirm step.
//
//   - Non-interactive: when --name is set (one or more times, or
//     comma-separated), the provider(s) are removed without prompting.
//     Useful for scripts:
//
//     otters provider rm --name anthropic --name openai
//     otters provider rm --name anthropic,openai
type ProviderRm struct {
	Name []string `help:"Provider name(s) to remove. Repeat the flag or comma-separate. Setting this skips the interactive form." default:""`
}

func (r *ProviderRm) Run(_ context.Context, common *cmd.Commons) error {
	path, err := internal.DefaultProvidersPath()
	if err != nil {
		return err
	}

	file, err := internal.ReadProvidersFile(path)
	if err != nil {
		return err
	}

	if len(file.Providers) == 0 {
		_, _ = common.Printer().Println("no providers configured")

		return nil
	}

	selected, ok, err := r.nonInteractiveSelection(file)
	if err != nil {
		return err
	}

	if !ok {
		selected, ok, err = r.interactiveSelection(file, path)
		if err != nil {
			return err
		}

		if !ok {
			_, _ = common.Printer().Println("cancelled")

			return nil
		}
	}

	removed := internal.RemoveProviders(&file, selected)

	if writeErr := internal.WriteProvidersFile(path, file); writeErr != nil {
		return writeErr
	}

	_, _ = common.Printer().Printf("removed %d provider(s)\n", removed)

	return nil
}

// nonInteractiveSelection flattens the --name flag (kong gives us one
// entry per occurrence; we additionally split each on `,` so
// `--name a,b` works the way users expect). Returns (nil, false, nil)
// when --name was not supplied so the caller falls back to the form.
// Unknown names error out — silently dropping a typo'd name would
// hide bugs in CI scripts.
func (r *ProviderRm) nonInteractiveSelection(file internal.ProvidersFile) ([]string, bool, error) {
	if len(r.Name) == 0 {
		return nil, false, nil
	}

	known := make(map[string]struct{}, len(file.Providers))
	for _, p := range file.Providers {
		known[p.Name] = struct{}{}
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

func (r *ProviderRm) interactiveSelection(
	file internal.ProvidersFile, path string,
) ([]string, bool, error) {
	opts := make([]huh.Option[string], 0, len(file.Providers))
	for _, p := range file.Providers {
		label := p.Name
		if p.APIBase != "" {
			label = p.Name + "  (" + p.APIBase + ")"
		}

		opts = append(opts, huh.NewOption(label, p.Name))
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
					return fmt.Sprintf("Remove %d provider(s) from %s?", len(selected), path)
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
// column is masked unless --reveal is set, so a casual `otters
// provider ls` over a shared screen doesn't leak keys.
type ProviderLs struct {
	Reveal bool `help:"Show full api-key values instead of a length-only mask." default:"false"`
}

func (l *ProviderLs) Run(_ context.Context, common *cmd.Commons) error {
	path, err := internal.DefaultProvidersPath()
	if err != nil {
		return err
	}

	file, err := internal.ReadProvidersFile(path)
	if err != nil {
		return err
	}

	if len(file.Providers) == 0 {
		_, _ = common.Printer().Println("no providers configured (run `otters provider add`)")

		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "NAME\tAPI BASE\tMODELS\tAPI KEY")

	for _, p := range file.Providers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			p.Name,
			fallback(p.APIBase, "-"),
			modelsCell(p.Models),
			renderAPIKey(p.APIKey, l.Reveal),
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

// renderAPIKey shows the key length plus prefix when masked, so
// operators can recognise which key is which without exposing it.
// $ENV_VAR references render verbatim — they are not the secret
// itself, just a pointer.
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
