package commands

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/merlindorin/go-shared/pkg/cmd"
	daemonv1 "github.com/openotters/cli/api/v1"
	"github.com/openotters/cli/internal"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type Serve struct {
	SocketPath string `help:"Unix socket path" default:""`
}

func (d *Serve) Run(ctx context.Context, common *cmd.Commons, sqlite *cmd.SQLite) error {
	logger := common.MustLogger().Named("daemon")

	socketPath := d.SocketPath
	if socketPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		socketPath = filepath.Join(home, ".openotters", "openotters.sock")
	}

	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return err
	}

	_ = os.Remove(socketPath)

	providers, err := internal.LoadProviders()
	if err != nil {
		return fmt.Errorf("loading providers: %w", err)
	}

	logger.Info("providers loaded", zap.Int("count", providers.Count()))

	reg := internal.NewEmbeddedRegistry(logger)
	if err = reg.Start(); err != nil {
		return fmt.Errorf("starting embedded registry: %w", err)
	}

	defer reg.Stop()

	if sqlite.Path == ":memory:" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return homeErr
		}

		sqlite.Path = filepath.Join(home, ".openotters", "daemon.db")
	}

	db, err := sqlite.Open()
	if err != nil {
		return fmt.Errorf("opening state database: %w", err)
	}
	defer db.Close()

	state, err := internal.NewStateStore(db)
	if err != nil {
		return fmt.Errorf("creating state store: %w", err)
	}

	dm := internal.NewDaemon(providers, reg, state, logger)

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
