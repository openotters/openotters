package internal

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/openotters/agentfile/executor"
	"github.com/openotters/agentfile/export"
	afoci "github.com/openotters/agentfile/oci"
	"github.com/openotters/agentfile/spec"
	afstore "github.com/openotters/agentfile/store"
	daemonv1 "github.com/openotters/cli/api/v1"
	"github.com/openotters/runtime/pkg/agent"
	"github.com/openotters/runtime/pkg/memory"
	"github.com/openotters/runtime/pkg/tool"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite" // sqlite driver
	"oras.land/oras-go/v2"
	orasmem "oras.land/oras-go/v2/content/memory"
)

const (
	statusStopped = "stopped"
	statusPending = "pending"
	statusRunning = "running"
	defaultTag    = "latest"
)

type runningAgent struct {
	id        string
	name      string
	agentName string
	model     string
	root      string
	tag       string
	status    string
	createdAt time.Time
	svc       *agent.Service
	lm        fantasy.LanguageModel
	cancel    context.CancelFunc
	af        *spec.Agentfile
}

type Daemon struct {
	agents    map[string]*runningAgent
	mu        sync.RWMutex
	providers *ProviderRegistry
	registry  *EmbeddedRegistry
	state     *StateStore
	logger    *zap.Logger
}

func NewDaemon(providers *ProviderRegistry, reg *EmbeddedRegistry, state *StateStore, logger *zap.Logger) *Daemon {
	return &Daemon{
		agents:    make(map[string]*runningAgent),
		providers: providers,
		registry:  reg,
		state:     state,
		logger:    logger.Named("daemon"),
	}
}

func (d *Daemon) RegistryAddr() string {
	if d.registry == nil {
		return ""
	}

	return d.registry.Addr()
}

func (d *Daemon) Restore(ctx context.Context) error {
	persisted, err := d.state.ListAgents()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	if len(persisted) == 0 {
		return nil
	}

	var running, pending, stopped int

	for _, pa := range persisted {
		root := d.agentRoot(pa.Name)

		if _, statErr := os.Stat(root); os.IsNotExist(statErr) {
			d.logger.Warn("workspace missing, skipping agent",
				zap.String("name", pa.Name), zap.String("root", root))

			continue
		}

		fullRef := d.registry.Addr() + "/" + pa.Tag

		_, af, pullErr := d.pullImage(ctx, fullRef)
		if pullErr != nil {
			d.logger.Warn("failed to pull agentfile, skipping agent",
				zap.String("name", pa.Name), zap.String("tag", pa.Tag), zap.Error(pullErr))

			continue
		}

		if af.Agent == nil {
			d.logger.Warn("no agent in image, skipping", zap.String("tag", pa.Tag))

			continue
		}

		ra := &runningAgent{
			id: pa.ID, name: pa.Name, agentName: pa.AgentName,
			model: pa.Model, root: root, tag: pa.Tag,
			createdAt: pa.CreatedAt, af: af,
		}

		switch {
		case pa.Status == statusStopped:
			ra.status = statusStopped
			stopped++
		case d.providers.ModelAvailable(pa.Model):
			svc, lm, cancel, startErr := d.startAgent(ctx, root)
			if startErr != nil {
				d.logger.Warn("failed to start agent, marking pending",
					zap.String("name", pa.Name), zap.Error(startErr))

				ra.status = statusPending
				pending++
			} else {
				ra.svc = svc
				ra.lm = lm
				ra.cancel = cancel
				ra.status = statusRunning
				running++
			}
		default:
			ra.status = statusPending
			pending++
		}

		d.mu.Lock()
		d.agents[pa.ID] = ra
		d.mu.Unlock()
	}

	d.logger.Info("agents restored",
		zap.Int("running", running),
		zap.Int("pending", pending),
		zap.Int("stopped", stopped),
	)

	return nil
}

func (d *Daemon) agentRoot(name string) string {
	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".openotters", "agents", name)
}

func (d *Daemon) Save(
	ctx context.Context, req *daemonv1.SaveAgentImageRequest,
) (*daemonv1.SaveAgentImageResponse, error) {
	store, digest, err := export.Import(req.GetOciArtifact())
	if err != nil {
		return nil, fmt.Errorf("importing artifact: %w", err)
	}

	tags := req.GetTags()

	for _, tag := range tags {
		ref := d.registry.Addr() + "/" + tag

		if _, pushErr := d.pushImage(ctx, store, ref); pushErr != nil {
			return nil, fmt.Errorf("saving %s to local registry: %w", tag, pushErr)
		}
	}

	d.logger.Info("image saved",
		zap.String("digest", digest),
		zap.Strings("tags", tags),
	)

	return &daemonv1.SaveAgentImageResponse{
		Digest: digest,
		Tags:   tags,
	}, nil
}

func (d *Daemon) Pull(
	ctx context.Context, req *daemonv1.PullRequest,
) (*daemonv1.PullResponse, error) {
	ref := req.GetRef()

	store, af, err := d.pullImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("pulling %s: %w", ref, err)
	}

	tags := req.GetTags()
	if len(tags) == 0 {
		name := "agent"
		if af.Agent != nil && af.Agent.Name != "" {
			name = af.Agent.Name
		}

		tag := afoci.ParseTag(ref)
		if tag == "" {
			tag = defaultTag
		}

		tags = []string{name + ":" + tag}
	}

	var digest string

	for _, tag := range tags {
		localRef := d.registry.Addr() + "/" + tag

		dig, pushErr := d.pushImage(ctx, store, localRef)
		if pushErr != nil {
			return nil, fmt.Errorf("saving %s to local registry: %w", tag, pushErr)
		}

		digest = dig
	}

	d.logger.Info("image pulled",
		zap.String("ref", ref),
		zap.Strings("tags", tags),
	)

	return &daemonv1.PullResponse{
		Digest: digest,
		Tags:   tags,
	}, nil
}

func (d *Daemon) Push(
	ctx context.Context, req *daemonv1.PushRequest,
) (*daemonv1.PushResponse, error) {
	ref := req.GetRef()

	parts := strings.SplitN(ref, "/", 2)
	localTag := parts[len(parts)-1]
	localRef := d.registry.Addr() + "/" + localTag

	d.logger.Info("pulling from local registry", zap.String("local", localRef))

	store, _, err := d.pullImage(ctx, localRef)
	if err != nil {
		return nil, fmt.Errorf("pulling from local registry: %w", err)
	}

	d.logger.Info("pushing to remote", zap.String("ref", ref))

	digest, err := d.pushImage(ctx, store, ref)
	if err != nil {
		return nil, fmt.Errorf("pushing to %s: %w", ref, err)
	}

	return &daemonv1.PushResponse{
		Digest: digest,
		Ref:    ref,
	}, nil
}

func (d *Daemon) Create(
	ctx context.Context, req *daemonv1.CreateAgentRequest,
) (*daemonv1.CreateAgentResponse, error) {
	ref := req.GetRef()

	if !strings.Contains(ref, "/") {
		ref = d.registry.Addr() + "/" + ref
	}

	store, af, err := d.pullImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("pulling agent image %s: %w", req.GetRef(), err)
	}

	if af.Agent == nil {
		return nil, fmt.Errorf("no agent defined in image %s", req.GetRef())
	}

	id := generateID()

	name := req.GetName()
	if name == "" {
		name = generateName()
	}

	agentName := af.Agent.Name
	if agentName == "" {
		agentName = name
	}

	root := d.agentRoot(name)

	e := executor.NewFileExecutor(root)
	e.BinPuller = afoci.RemotePuller()

	if _, execErr := e.Execute(ctx, store, defaultTag); execErr != nil {
		return nil, fmt.Errorf("setting up workspace: %w", execErr)
	}

	ra := &runningAgent{
		id: id, name: name, agentName: agentName,
		model: af.Agent.Model, root: root, tag: extractTag(ref),
		createdAt: time.Now(), af: af,
	}

	if d.providers.ModelAvailable(af.Agent.Model) {
		svc, lm, cancel, startErr := d.startAgent(ctx, root)
		if startErr != nil {
			return nil, startErr
		}

		ra.svc = svc
		ra.lm = lm
		ra.cancel = cancel
		ra.status = statusRunning
	} else {
		ra.status = statusPending
		d.logger.Warn("model not available, agent pending",
			zap.String("model", af.Agent.Model),
			zap.String("name", name),
		)
	}

	d.mu.Lock()
	d.agents[id] = ra
	d.mu.Unlock()

	if saveErr := d.state.SaveAgent(persistedAgent{
		ID: id, Name: name, AgentName: agentName,
		Model: af.Agent.Model, Tag: ra.tag, Status: ra.status,
		CreatedAt: ra.createdAt,
	}); saveErr != nil {
		d.logger.Warn("failed to persist agent", zap.Error(err))
	}

	d.logger.Info("agent created",
		zap.String("id", id),
		zap.String("name", name),
		zap.String("agent", agentName),
		zap.String("model", af.Agent.Model),
		zap.String("status", ra.status),
	)

	return &daemonv1.CreateAgentResponse{
		Id: id, Name: name, Status: ra.status,
	}, nil
}

type agentConfig struct {
	Name    string            `yaml:"name"`
	Model   string            `yaml:"model"`
	Configs map[string]string `yaml:"configs,omitempty"`
	Tools   []agentConfigTool `yaml:"tools,omitempty"`
}

type agentConfigTool struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Binary      string `yaml:"binary"`
}

func (d *Daemon) startAgent(
	ctx context.Context, root string,
) (*agent.Service, fantasy.LanguageModel, context.CancelFunc, error) {
	cfg, err := d.loadAgentConfig(root)
	if err != nil {
		return nil, nil, nil, err
	}

	provider, modelName := parseModel(cfg.Model)
	if provider == "" {
		return nil, nil, nil, fmt.Errorf("invalid model format: expected 'provider/model'")
	}

	contextDir := filepath.Join(root, "etc", "context")

	contextFiles, err := discoverContextFiles(contextDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("discovering context files: %w", err)
	}

	systemPrompt, err := agent.BuildSystemPrompt(contextDir, contextFiles)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building system prompt: %w", err)
	}

	agentTools, err := d.loadTools(cfg.Tools, root)
	if err != nil {
		return nil, nil, nil, err
	}

	apiKey, apiBase, resolveErr := d.providers.Resolve(cfg.Model)
	if resolveErr != nil {
		return nil, nil, nil, resolveErr
	}

	agentCtx, cancel := context.WithCancel(ctx)

	maxTokens := configInt(cfg.Configs, "max-tokens", 4096)
	maxIterations := configInt(cfg.Configs, "max-iterations", 20)

	fantasyAgent, lm, createErr := agent.CreateAgent(agentCtx, agent.Config{
		Provider: provider, ModelName: modelName,
		APIKey: apiKey, APIBase: apiBase,
		MaxTokens: maxTokens, MaxIterations: maxIterations,
	}, systemPrompt, agentTools, d.logger)
	if createErr != nil {
		cancel()

		return nil, nil, nil, fmt.Errorf("creating agent: %w", createErr)
	}

	dbPath := filepath.Join(root, "var", "lib", "memory.db")

	db, dbErr := sql.Open("sqlite", dbPath)
	if dbErr != nil {
		cancel()

		return nil, nil, nil, fmt.Errorf("opening sqlite: %w", dbErr)
	}

	memStore, storeErr := memory.NewStore(db)
	if storeErr != nil {
		cancel()
		db.Close()

		return nil, nil, nil, fmt.Errorf("creating memory store: %w", storeErr)
	}

	memStrategy := configStr(cfg.Configs, "memory-strategy", "summarize")
	memMaxMsgs := configInt(cfg.Configs, "memory-max-messages", 20)

	compactor := memory.NewCompactor(memory.Config{
		Strategy:    memStrategy,
		MaxMessages: memMaxMsgs,
	}, d.logger)

	svc := agent.NewService(fantasyAgent, lm, memStore, compactor, d.logger)

	return svc, lm, cancel, nil
}

func (d *Daemon) loadAgentConfig(root string) (*agentConfig, error) {
	path := filepath.Join(root, "etc", "agent.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading agent config: %w", err)
	}

	var cfg agentConfig
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing agent config: %w", err)
	}

	return &cfg, nil
}

func (d *Daemon) loadTools(
	tools []agentConfigTool, root string,
) ([]fantasy.AgentTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	defs := make([]tool.Def, len(tools))
	for i, t := range tools {
		binary := t.Binary
		if !filepath.IsAbs(binary) {
			binary = filepath.Join(root, binary)
		}

		defs[i] = tool.Def{
			Name: t.Name, Description: t.Description,
			Binary: binary,
		}
	}

	dataDir := filepath.Join(root, "etc", "data")

	return tool.LoadTools(defs, dataDir, d.logger)
}

func (d *Daemon) List() []*daemonv1.AgentInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	infos := make([]*daemonv1.AgentInfo, 0, len(d.agents))
	for _, ra := range d.agents {
		infos = append(infos, &daemonv1.AgentInfo{
			Id: ra.id, Name: ra.name, Model: ra.model,
			Status: ra.status, Root: ra.root,
			CreatedAt: ra.createdAt.Unix(),
		})
	}

	return infos
}

func (d *Daemon) Stop(ref string) error {
	ra, err := d.resolve(ref)
	if err != nil {
		return err
	}

	if ra.cancel != nil {
		ra.cancel()
	}

	ra.status = statusStopped

	if updateErr := d.state.UpdateStatus(ra.id, statusStopped); updateErr != nil {
		d.logger.Warn("failed to persist stop", zap.Error(updateErr))
	}

	d.logger.Info("agent stopped", zap.String("id", ra.id), zap.String("name", ra.name))

	return nil
}

func (d *Daemon) Remove(ref string) error {
	ra, err := d.resolve(ref)
	if err != nil {
		return err
	}

	if ra.cancel != nil {
		ra.cancel()
	}

	d.mu.Lock()
	delete(d.agents, ra.id)
	d.mu.Unlock()

	if rmErr := d.state.RemoveAgent(ra.id); rmErr != nil {
		d.logger.Warn("failed to persist removal", zap.Error(err))
	}

	if err = os.RemoveAll(ra.root); err != nil {
		d.logger.Warn("failed to remove root", zap.String("root", ra.root), zap.Error(err))
	}

	d.logger.Info("agent removed", zap.String("id", ra.id), zap.String("name", ra.name))

	return nil
}

func (d *Daemon) resolve(ref string) (*runningAgent, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if ra, ok := d.agents[ref]; ok {
		return ra, nil
	}

	for _, ra := range d.agents {
		if ra.name == ref {
			return ra, nil
		}
	}

	for id, ra := range d.agents {
		if strings.HasPrefix(id, ref) {
			return ra, nil
		}
	}

	return nil, fmt.Errorf("agent %q not found", ref)
}

func parseModel(model string) (string, string) {
	if idx := strings.Index(model, "/"); idx > 0 {
		return model[:idx], model[idx+1:]
	}

	return "", model
}

func discoverContextFiles(contextDir string) ([]string, error) {
	entries, err := os.ReadDir(contextDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e.Name())
		}
	}

	return files, nil
}

func configStr(configs map[string]string, key, defaultVal string) string {
	if v, ok := configs[key]; ok && v != "" {
		return v
	}

	return defaultVal
}

func configInt(configs map[string]string, key string, defaultVal int) int {
	if v, ok := configs[key]; ok && v != "" {
		var n int
		if _, scanErr := fmt.Sscanf(v, "%d", &n); scanErr == nil {
			return n
		}
	}

	return defaultVal
}

func (d *Daemon) pullImage(ctx context.Context, ref string) (*orasmem.Store, *spec.Agentfile, error) {
	var opts []afoci.RemoteRepositoryOption
	if d.registry != nil && strings.HasPrefix(ref, d.registry.Addr()) {
		opts = append(opts, afoci.WithPlainHTTP)
	}

	repo, err := afoci.NewRemoteRepository(ref, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("creating repository for %s: %w", ref, err)
	}

	store := orasmem.New()

	tag := afoci.ParseTag(ref)
	if tag == "" {
		tag = defaultTag
	}

	_, err = oras.Copy(ctx, repo, tag, store, defaultTag, oras.CopyOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("copying %s: %w", ref, err)
	}

	af, err := afstore.LoadWithLayers(store, defaultTag)
	if err != nil {
		return nil, nil, fmt.Errorf("loading agentfile from %s: %w", ref, err)
	}

	return store, af, nil
}

func (d *Daemon) pushImage(ctx context.Context, store *orasmem.Store, ref string) (string, error) {
	var opts []afoci.RemoteRepositoryOption
	if d.registry != nil && strings.HasPrefix(ref, d.registry.Addr()) {
		opts = append(opts, afoci.WithPlainHTTP)
	}

	repo, err := afoci.NewRemoteRepository(ref, opts...)
	if err != nil {
		return "", fmt.Errorf("creating repository for %s: %w", ref, err)
	}

	tag := afoci.ParseTag(ref)
	if tag == "" {
		tag = defaultTag
	}

	desc, err := oras.Copy(ctx, store, defaultTag, repo, tag, oras.CopyOptions{})
	if err != nil {
		return "", err
	}

	return desc.Digest.String(), nil
}
