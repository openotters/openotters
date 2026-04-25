//nolint:cyclop // CLI bootstrap; complexity is in Serve.Run, justified there
package commands

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/openotters/api/v1"
	"github.com/openotters/openotters/internal"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type Serve struct {
	SocketPath   string `help:"Unix socket path" default:""`
	Runtime      string `help:"Path to a local runtime binary (skips pulling the runtime image from OCI)" default:""`
	RegistryAddr string `help:"TCP bind address for the embedded OCI registry (overrides OTTERS_REGISTRY_ADDR)" default:""`
}

//nolint:funlen // single-shot daemon bootstrap reads more clearly straight-through
func (d *Serve) Run(ctx context.Context, common *cmd.Commons, sqlite *cmd.SQLite) error {
	logger := common.MustLogger().Named("daemon")

	socketPath := d.SocketPath
	if socketPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		socketPath = filepath.Join(home, ".otters", "otters.sock")
	}

	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}

	_ = os.Remove(socketPath)

	providers, err := internal.LoadProviders()
	if err != nil {
		return fmt.Errorf("loading providers: %w", err)
	}

	providers.Each(func(p *internal.ProviderConfig) {
		logger.Info("provider loaded",
			zap.String("name", p.Name),
			zap.String("api_base", p.APIBase),
			zap.Int("models", len(p.Models)),
		)
	})

	reg := internal.NewEmbeddedRegistry(logger, internal.WithRegistryAddr(d.RegistryAddr))
	if err = reg.Start(ctx); err != nil {
		return fmt.Errorf("starting embedded registry: %w", err)
	}

	defer reg.Stop()

	if sqlite.Path == ":memory:" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return homeErr
		}

		sqlite.Path = filepath.Join(home, ".otters", "daemon.db")
	}

	db, err := sqlite.Open()
	if err != nil {
		return fmt.Errorf("opening state database: %w", err)
	}
	defer db.Close()

	state, err := internal.NewStateStore(ctx, db)
	if err != nil {
		return fmt.Errorf("creating state store: %w", err)
	}

	daemonOpts := []internal.DaemonOption{
		internal.WithSocket(socketPath),
		internal.WithBuildInfo(
			common.Version.Version(),
			common.Version.Commit(),
			common.Version.Date(),
		),
	}

	if d.Runtime != "" {
		if _, statErr := os.Stat(d.Runtime); statErr != nil {
			return fmt.Errorf("runtime binary %s: %w", d.Runtime, statErr)
		}

		daemonOpts = append(daemonOpts, internal.WithLocalRuntime(d.Runtime))
		logger.Info("using local runtime binary", zap.String("path", d.Runtime))
	}

	dm := internal.NewDaemon(providers, reg, state, logger, daemonOpts...)
	if runErr := dm.Run(ctx); runErr != nil {
		return runErr
	}

	if restoreErr := dm.Restore(ctx); restoreErr != nil {
		logger.Warn("failed to restore agents", zap.Error(restoreErr))
	}

	srv := grpc.NewServer()
	daemonv1.RegisterRuntimeServer(srv, internal.NewGRPCServer(dm))
	reflection.Register(srv)

	lc := net.ListenConfig{}
	lis, err := lc.Listen(ctx, "unix", socketPath)
	if err != nil {
		return err
	}

	logger.Info("daemon listening", zap.String("socket", socketPath))

	go func() {
		<-ctx.Done()
		logger.Info("shutting down daemon")
		srv.GracefulStop()
	}()

	return srv.Serve(lis)
}
