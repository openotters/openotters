package pkg

import (
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	daemonv1 "github.com/openotters/openotters/api/v1"
)

func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".otters", "otters.sock")
}

func Connect(socketPath string) (daemonv1.RuntimeClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient("unix:"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}

	return daemonv1.NewRuntimeClient(conn), conn, nil
}
