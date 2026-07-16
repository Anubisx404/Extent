package stack

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const composeFile = "docker-compose.observability.yml"

func Up(root string) error {
	return dockerCompose(root, "-f", composeFile, "up", "-d")
}

func Down(root string) error {
	return dockerCompose(root, "-f", composeFile, "down")
}

func Status(root string) error {
	return dockerCompose(root, "-f", composeFile, "ps")
}

func dockerCompose(root string, args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		fmt.Println(strings.TrimSpace(string(out)))
	}
	if err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}
