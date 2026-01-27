//go:build !linux
// +build !linux

package runtime

import "fmt"

func setupRootfs(config *ContainerConfig) error {
	if config.Rootfs != "" {
		return fmt.Errorf("rootfs isolation requires Linux")
	}
	return nil
}
