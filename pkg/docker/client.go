package docker

import (
	"fmt"
	"sync"

	"github.com/docker/docker/client"
)

var (
	once    sync.Once
	shared  *client.Client
	initErr error
)

// Get returns the shared Docker client, initializing it on first call.
func Get() (*client.Client, error) {
	once.Do(func() {
		shared, initErr = client.NewClientWithOpts(
			client.FromEnv,
			client.WithAPIVersionNegotiation(),
		)
	})
	if initErr != nil {
		return nil, fmt.Errorf("docker client unavailable: %w", initErr)
	}
	return shared, nil
}
