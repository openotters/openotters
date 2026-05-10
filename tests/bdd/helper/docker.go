package helper

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// DockerAvailable returns true when the `docker` CLI is on PATH AND
// `docker info` answers quickly. Used to decide whether to skip the
// docker-executor BDD suites on hosts that don't have Docker (or have
// it stopped — common on dev laptops with Colima not started).
func DockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "info").Run() == nil
}

// DockerHost returns the daemon socket the `docker` CLI is currently
// pointed at — typically /var/run/docker.sock on Linux/Docker Desktop,
// or ~/.colima/default/docker.sock on Colima. ottersd's docker
// executor hardcodes /var/run/docker.sock unless DOCKER_HOST is set,
// so the BDD helper exports this value into the daemon's env to
// match whatever surface the user already has working.
//
// Returns "" when no context is configured or the docker CLI errors.
func DockerHost() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "context", "inspect",
		"--format", "{{.Endpoints.docker.Host}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
