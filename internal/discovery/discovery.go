package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Resource represents a discovered container or k8s resource.
type Resource struct {
	Name      string
	Type      string // "docker", "k8s-service", "k8s-pod"
	Ports     []int
	Namespace string // k8s namespace; empty for docker
}

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

// SanitizeName converts a resource name to a valid DNS subdomain label.
func SanitizeName(name string) string {
	s := strings.ToLower(name)
	s = nonAlphanumeric.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// DockerContainers lists running Docker containers.
// Returns nil, nil if docker is not installed.
func DockerContainers() ([]Resource, error) {
	_, err := exec.LookPath("docker")
	if err != nil {
		return nil, nil
	}

	out, err := exec.Command("docker", "ps", "--format", "{{json .}}").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("docker ps failed: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}

	var resources []Resource
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var container struct {
			Names string `json:"Names"`
			Ports string `json:"Ports"`
		}
		if err := json.Unmarshal([]byte(line), &container); err != nil {
			continue
		}
		ports := parseDockerPorts(container.Ports)
		resources = append(resources, Resource{
			Name:  container.Names,
			Type:  "docker",
			Ports: ports,
		})
	}
	return resources, nil
}

// parseDockerPorts extracts port numbers from docker ps Ports field.
// Example: "0.0.0.0:8080->80/tcp, 443/tcp" → [8080, 443]
func parseDockerPorts(s string) []int {
	if s == "" {
		return nil
	}
	var ports []int
	seen := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		// Look for host port in "host:port->container/proto" format
		if idx := strings.Index(part, "->"); idx != -1 {
			hostPart := part[:idx]
			if colonIdx := strings.LastIndex(hostPart, ":"); colonIdx != -1 {
				var p int
				if _, err := fmt.Sscanf(hostPart[colonIdx+1:], "%d", &p); err == nil && !seen[p] {
					ports = append(ports, p)
					seen[p] = true
				}
			}
		} else {
			// Just "port/proto"
			var p int
			portStr := strings.Split(part, "/")[0]
			if _, err := fmt.Sscanf(portStr, "%d", &p); err == nil && !seen[p] {
				ports = append(ports, p)
				seen[p] = true
			}
		}
	}
	return ports
}

// K8sServices lists Kubernetes services.
// Returns nil, nil if kubectl is not installed.
func K8sServices() ([]Resource, error) {
	_, err := exec.LookPath("kubectl")
	if err != nil {
		return nil, nil
	}

	out, err := exec.Command("kubectl", "get", "svc", "--all-namespaces", "-o", "json").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("kubectl get svc failed: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	var svcList struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Ports []struct {
					Port int `json:"port"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &svcList); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}

	var resources []Resource
	for _, item := range svcList.Items {
		var ports []int
		for _, p := range item.Spec.Ports {
			ports = append(ports, p.Port)
		}
		resources = append(resources, Resource{
			Name:      item.Metadata.Name,
			Type:      "k8s-service",
			Ports:     ports,
			Namespace: item.Metadata.Namespace,
		})
	}
	return resources, nil
}

// K8sPods lists Kubernetes pods.
// Returns nil, nil if kubectl is not installed.
func K8sPods() ([]Resource, error) {
	_, err := exec.LookPath("kubectl")
	if err != nil {
		return nil, nil
	}

	out, err := exec.Command("kubectl", "get", "pods", "--all-namespaces", "-o", "json").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("kubectl get pods failed: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	var podList struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Ports []struct {
						ContainerPort int `json:"containerPort"`
					} `json:"ports"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &podList); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}

	var resources []Resource
	for _, item := range podList.Items {
		var ports []int
		for _, c := range item.Spec.Containers {
			for _, p := range c.Ports {
				ports = append(ports, p.ContainerPort)
			}
		}
		resources = append(resources, Resource{
			Name:      item.Metadata.Name,
			Type:      "k8s-pod",
			Ports:     ports,
			Namespace: item.Metadata.Namespace,
		})
	}
	return resources, nil
}

// ResourcesByType holds resources grouped by source.
type ResourcesByType struct {
	Docker   []Resource
	Services []Resource
	Pods     []Resource
}

// Empty returns true if no resources were found.
func (r ResourcesByType) Empty() bool {
	return len(r.Docker) == 0 && len(r.Services) == 0 && len(r.Pods) == 0
}

// AllByType loads resources from all available sources concurrently,
// returning them grouped by type.
func AllByType() ResourcesByType {
	type indexedResult struct {
		index     int
		resources []Resource
	}
	ch := make(chan indexedResult, 3)

	go func() {
		r, _ := DockerContainers()
		ch <- indexedResult{0, r}
	}()
	go func() {
		r, _ := K8sServices()
		ch <- indexedResult{1, r}
	}()
	go func() {
		r, _ := K8sPods()
		ch <- indexedResult{2, r}
	}()

	var result ResourcesByType
	for i := 0; i < 3; i++ {
		res := <-ch
		switch res.index {
		case 0:
			result.Docker = res.resources
		case 1:
			result.Services = res.resources
		case 2:
			result.Pods = res.resources
		}
	}
	return result
}
