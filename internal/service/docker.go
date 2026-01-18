package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
)

import (
	dockerErr "github.com/containerd/errdefs"
	dockerContainer "github.com/docker/docker/api/types/container"
	dockerFilter "github.com/docker/docker/api/types/filters"
	dockerImage "github.com/docker/docker/api/types/image"
	// dockerNetwork "github.com/docker/docker/api/types/network"
	dockerClient "github.com/docker/docker/client"
)

type Docker struct {
	images []string
}

const (
	dockerContainerNanoCPUs    = 1_000_000_000    // 1 CPU
	dockerContainerMemoryBytes = 48 * 1024 * 1024 // 48 MiB
)

var (
	ErrNoDockerImage = errors.New("Docker image wasn't provided, unable to create a container")
)

func NewDocker(ctx context.Context, image string) (*Docker, error) {
	images := []string{}

	if image != "" {
		var err error
		images, err = findDockerImages(ctx, image)
		if err != nil {
			return nil, err
		}

		fmt.Printf("Going to use the following list of images: %v\n", images)
	}

	return &Docker{images}, nil
}

func (service *Docker) InitContainer(ctx context.Context, hostname, image string) (string, string, error) {
	type resType struct{ image, id string }

	if image == "" {
		if len(service.images) == 0 {
			return "", "", ErrNoDockerImage
		}

		image = service.images[rand.Intn(len(service.images))]
	}

	res, err := withDockerClient(func(client *dockerClient.Client) (resType, error) {
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
			return resType{}, err
		}

		for _, warn := range create.Warnings {
			fmt.Fprintf(os.Stderr, "[%s] [%s] Warning: %s\n", hostname, create.ID, warn)
		}

		return resType{image, create.ID}, nil
	})

	return res.image, res.id, err
}

func (service *Docker) ContainerExists(ctx context.Context, id string) (bool, error) {
	_, err := withDockerClient(func(client *dockerClient.Client) (*struct{}, error) {
		_, err := client.ContainerInspect(ctx, id)
		return nil, err
	})

	if err == nil {
		return true, nil
	}

	if dockerErr.IsNotFound(err) {
		return false, nil
	}

	return false, err
}

func (service *Docker) GetContainerLogs(ctx context.Context, id string) (string, error) {
	return withDockerClient(func(client *dockerClient.Client) (string, error) {
		r, err := client.ContainerLogs(ctx, id, dockerContainer.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
		})

		if err != nil {
			return "", err
		}

		defer func() {
			_ = r.Close()
		}()

		var res strings.Builder
		err = cleanDockerLogs(r, &res)
		return res.String(), err
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

func (service *Docker) StopContainer(ctx context.Context, id string) error {
	_, err := withDockerClient(func(client *dockerClient.Client) (*struct{}, error) {
		return nil, client.ContainerStop(ctx, id, dockerContainer.StopOptions{})
	})

	return err
}

func findDockerImages(ctx context.Context, image string) ([]string, error) {
	return withDockerClient(func(client *dockerClient.Client) ([]string, error) {
		res, err := client.ImageList(ctx, dockerImage.ListOptions{
			Filters: dockerFilter.NewArgs(dockerFilter.KeyValuePair{Key: "reference", Value: image}),
		})

		if err != nil {
			return nil, err
		}

		if len(res) == 0 {
			return nil, fmt.Errorf("no Docker images found for \"%s\"", image)
		}

		images := make([]string, 0, len(res))
		for _, img := range res {
			if len(img.RepoTags) == 0 {
				fmt.Fprintf(os.Stderr, "Skipping image %s without tags\n", img.ID)
				continue
			}

			images = append(images, img.RepoTags[0])
		}

		return images, nil
	})
}

func cleanDockerLogs(r io.Reader, w io.Writer) error {
	prefix := [8]byte{}

	for {
		if _, err := r.Read(prefix[:]); err != nil {
			if err == io.EOF {
				break
			}

			return err
		}

		size := binary.BigEndian.Uint32(prefix[4:])
		if _, err := io.CopyN(w, r, int64(size)); err != nil {
			return err
		}
	}

	return nil
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
