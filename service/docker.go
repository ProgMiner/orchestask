package service

import (
	"context"
	"fmt"
	"math/rand"
	"os"
)

import (
	dockerContainer "github.com/docker/docker/api/types/container"
	// dockerNetwork "github.com/docker/docker/api/types/network"
	dockerFilter "github.com/docker/docker/api/types/filters"
	dockerImage "github.com/docker/docker/api/types/image"
	dockerClient "github.com/docker/docker/client"
)

type Docker struct {
	images []string
}

const (
	dockerContainerNanoCPUs    = 1_000_000_000    // 1 CPU
	dockerContainerMemoryBytes = 24 * 1024 * 1024 // 24 MiB
)

func NewDocker(ctx context.Context, image string) (*Docker, error) {
	return withDockerClient(func(client *dockerClient.Client) (*Docker, error) {
		res, err := client.ImageList(ctx, dockerImage.ListOptions{
			Filters: dockerFilter.NewArgs(dockerFilter.KeyValuePair{"reference", image}),
		})

		if err != nil {
			return nil, err
		}

		if len(res) == 0 {
			return nil, fmt.Errorf("no Docker images found for \"%s\"", image)
		}

		images := make([]string, len(res))
		for i, img := range res {
			images[i] = img.ID
		}

		return &Docker{images}, nil
	})
}

func (service *Docker) InitContainer(ctx context.Context, hostname string) (string, error) {
	image := service.images[rand.Intn(len(service.images))]

	fmt.Printf("[%s] Pick random image: %s\n", hostname, image)

	return withDockerClient(func(client *dockerClient.Client) (string, error) {
		create, err := client.ContainerCreate(
			ctx,
			&dockerContainer.Config{
				Hostname: hostname,
				Image:    image,
			},
			&dockerContainer.HostConfig{
				Resources: dockerContainer.Resources{
					Memory:   dockerContainerMemoryBytes,
					NanoCPUs: dockerContainerNanoCPUs,
				},
			},
			nil,
			nil,
			"",
		)

		if err != nil {
			return "", err
		}

		for _, warn := range create.Warnings {
			fmt.Fprintf(os.Stderr, "[%s] [%s] Warning: %s\n", hostname, create.ID, warn)
		}

		return create.ID, nil
	})
}

func (service *Docker) EnsureContainer(ctx context.Context, id string) (string, error) {
	return withDockerClient(func(client *dockerClient.Client) (string, error) {
		inspect, err := client.ContainerInspect(ctx, id)
		if err != nil {
			return "", err
		}

		if !inspect.State.Running {
			err := client.ContainerStart(ctx, id, dockerContainer.StartOptions{})
			if err != nil {
				return "", err
			}

			inspect, err = client.ContainerInspect(ctx, id)
			if err != nil {
				return "", err
			}
		}

		ip := ""
		for _, endpoint := range inspect.NetworkSettings.Networks {
			ip = endpoint.IPAddress

			if ip != "" {
				break
			}
		}

		if ip == "" {
			return "", fmt.Errorf("container isn't accessible: %s", id)
		}

		return ip, nil
	})
}

func withDockerClient[T any](f func(client *dockerClient.Client) (T, error)) (T, error) {
	client, err := dockerClient.NewClientWithOpts(
		dockerClient.FromEnv,
		dockerClient.WithAPIVersionNegotiation(),
	)

	if err != nil {
		var res T
		return res, err
	}

	res, err := f(client)
	if err != nil {
		_ = client.Close()
		return res, err
	}

	err = client.Close()
	return res, err
}
