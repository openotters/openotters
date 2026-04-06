package commands

import (
	"fmt"

	daemonv1 "github.com/openotters/cli/api/v1"
	"github.com/openotters/cli/pkg"
	"google.golang.org/grpc"
)

type Daemon struct {
	Socket string `name:"socket" help:"Daemon socket path" default:""`
}

func NewDaemon() *Daemon {
	return &Daemon{}
}

func (d *Daemon) Connect() (daemonv1.RuntimeClient, *grpc.ClientConn, error) {
	socketPath := d.Socket
	if socketPath == "" {
		socketPath = pkg.DefaultSocketPath()
	}

	c, conn, err := pkg.Connect(socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to daemon: %w", err)
	}

	return c, conn, nil
}
