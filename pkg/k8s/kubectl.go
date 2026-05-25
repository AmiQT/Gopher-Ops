// Package k8s wraps kubectl for common SRE operations. It uses the kubectl
// binary on PATH so it works with any kubeconfig — no Go client dependency
// needed. All functions return an error when kubectl is not installed, so
// callers can present a clear "kubectl not available" message rather than
// hanging or crashing.
package k8s

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const kubectlTimeout = 30 * time.Second

// Available returns true when kubectl is on PATH.
func Available() bool {
	_, err := exec.LookPath("kubectl")
	return err == nil
}

// run executes kubectl with the given args and returns combined stdout+stderr.
func run(ctx context.Context, args ...string) (string, error) {
	if !Available() {
		return "", fmt.Errorf("kubectl not found on PATH — install kubectl to use Kubernetes features")
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("kubectl timed out")
	}
	if err != nil {
		return string(out), fmt.Errorf("kubectl %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ListPods returns a formatted table of pods in the given namespace.
// Pass namespace="" to list across all namespaces.
func ListPods(namespace string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()

	args := []string{"get", "pods", "-o", "wide"}
	if namespace == "" {
		args = append(args, "--all-namespaces")
	} else {
		args = append(args, "-n", namespace)
	}
	out, err := run(ctx, args...)
	if err != nil {
		return out, err
	}
	return "```\n" + out + "\n```", nil
}

// DescribePod returns the full `kubectl describe pod` output for a pod.
func DescribePod(namespace, podName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()

	ns := namespace
	if ns == "" {
		ns = "default"
	}
	out, err := run(ctx, "describe", "pod", podName, "-n", ns)
	if err != nil {
		return out, err
	}
	// Trim to 3000 chars so it fits in Telegram.
	if len(out) > 3000 {
		out = out[:2997] + "..."
	}
	return "```\n" + out + "\n```", nil
}

// GetPodLogs returns the last `lines` log lines for a pod/container.
func GetPodLogs(namespace, podName string, lines int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()

	ns := namespace
	if ns == "" {
		ns = "default"
	}
	if lines <= 0 {
		lines = 50
	}
	out, err := run(ctx, "logs", podName, "-n", ns,
		fmt.Sprintf("--tail=%d", lines), "--timestamps")
	if err != nil {
		return out, err
	}
	return "```\n" + out + "\n```", nil
}

// RestartDeployment performs a rollout restart of a deployment.
func RestartDeployment(namespace, deployName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()

	ns := namespace
	if ns == "" {
		ns = "default"
	}
	out, err := run(ctx, "rollout", "restart", "deployment/"+deployName, "-n", ns)
	if err != nil {
		return out, err
	}
	return fmt.Sprintf("✅ Rollout restart triggered for `%s` in namespace `%s`.\n%s", deployName, ns, out), nil
}

// GetClusterInfo returns cluster nodes and component status summary.
func GetClusterInfo() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()

	nodes, err := run(ctx, "get", "nodes", "-o", "wide")
	if err != nil {
		return nodes, err
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel2()
	namespaces, _ := run(ctx2, "get", "namespaces")

	var sb strings.Builder
	sb.WriteString("**Nodes:**\n```\n" + nodes + "\n```\n\n")
	if namespaces != "" {
		sb.WriteString("**Namespaces:**\n```\n" + namespaces + "\n```")
	}
	return sb.String(), nil
}

// ScaleDeployment scales a deployment to the desired number of replicas.
func ScaleDeployment(namespace, deployName string, replicas int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
	defer cancel()

	ns := namespace
	if ns == "" {
		ns = "default"
	}
	out, err := run(ctx, "scale", "deployment/"+deployName,
		fmt.Sprintf("--replicas=%d", replicas), "-n", ns)
	if err != nil {
		return out, err
	}
	return fmt.Sprintf("✅ Scaled `%s` to %d replica(s) in `%s`.\n%s", deployName, replicas, ns, out), nil
}
