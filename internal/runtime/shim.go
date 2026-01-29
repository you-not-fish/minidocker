//go:build linux
// +build linux

package runtime

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"minidocker/internal/state"
	"minidocker/pkg/envutil"
)

// RunContainerShim is the entrypoint for the per-container shim process.
//
// Why a shim?
// `minidocker run -d` must return immediately, but we still need a parent process to:
// - reap the container init process (so we can reliably observe exit)
// - persist the final exit code and stopped state to state.json
//
// This aligns with the industry "per-container shim" model (e.g. containerd-shim).
func RunContainerShim() {
	containerDir := os.Getenv(envutil.StatePathEnvVar)
	notify := openShimNotifyWriter()

	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if notify != nil {
			fmt.Fprintf(notify, "ERR: %s\n", msg)
			notify.Close()
		}
		fmt.Fprintf(os.Stderr, "shim: %s\n", msg)
		os.Exit(1)
	}

	if containerDir == "" {
		fail("missing %s environment variable", envutil.StatePathEnvVar)
	}

	// Load config.json (immutable)
	cfg, err := state.LoadConfig(containerDir)
	if err != nil {
		fail("load config: %v", err)
	}

	// Load state.json (mutable)
	st, err := state.LoadState(containerDir)
	if err != nil {
		fail("load state: %v", err)
	}

	// Prepare runtime config for the init process
	rCfg := &ContainerConfig{
		ID:       cfg.ID,
		Command:  cfg.Command,
		Args:     cfg.Args,
		Hostname: cfg.Hostname,
		Rootfs:   cfg.Rootfs,
		TTY:      cfg.TTY,
		Detached: true, // shim only exists for detached containers
	}

	// Open log files for the container init
	logs, err := setupLogFiles(containerDir)
	if err != nil {
		fail("setup log files: %v", err)
	}

	// Start the container init process as a child of the shim
	cmd, err := newParentProcess(rCfg, containerDir, logs)
	if err != nil {
		logs.Close()
		fail("create container process: %v", err)
	}

	if err := cmd.Start(); err != nil {
		logs.Close()
		fail("start container process: %v", err)
	}

	// Persist running state (must happen before notifying the parent)
	if err := st.SetRunning(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logs.Close()
		fail("update state to running: %v", err)
	}

	// Notify the parent process that the container is running and state is updated.
	if notify != nil {
		_, _ = fmt.Fprintln(notify, "OK")
		_ = notify.Close()
	}

	// Wait for container exit and persist exit code
	exitCode := waitForExit(cmd)
	_ = st.SetStopped(exitCode)
	logs.Close()

	os.Exit(0)
}

func openShimNotifyWriter() *os.File {
	fdStr := os.Getenv(envutil.ShimNotifyFdEnvVar)
	if strings.TrimSpace(fdStr) == "" {
		return nil
	}

	fd, err := strconv.Atoi(fdStr)
	if err != nil || fd < 3 {
		return nil
	}

	// Note: fd comes from exec.Cmd.ExtraFiles (>= 3).
	return os.NewFile(uintptr(fd), "minidocker-shim-notify")
}
