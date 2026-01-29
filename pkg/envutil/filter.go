// Package envutil provides utilities for environment variable handling.
//
// This package centralizes the management of minidocker's internal environment
// variables used for process coordination (init, exec, shim modes).
package envutil

import "strings"

// Environment variable names used by minidocker for internal process coordination.
const (
	// InitEnvVar triggers container init mode when set to "1".
	// The init process becomes PID 1 inside the container.
	InitEnvVar = "MINIDOCKER_INIT"

	// ExecEnvVar triggers exec mode when set to "1".
	// Used for entering an existing container's namespaces.
	ExecEnvVar = "MINIDOCKER_EXEC"

	// ConfigEnvVar passes container configuration JSON (Phase 1/2 legacy).
	// Deprecated in favor of loading from config.json file.
	ConfigEnvVar = "MINIDOCKER_CONFIG"

	// StatePathEnvVar passes the container state directory path.
	// Used by init and shim processes to locate config.json and state.json.
	StatePathEnvVar = "MINIDOCKER_STATE_PATH"

	// ShimEnvVar triggers per-container shim mode when set to "1".
	// The shim is a long-running parent process for detached containers.
	ShimEnvVar = "MINIDOCKER_SHIM"

	// ShimNotifyFdEnvVar specifies the fd number for shim status notification.
	// The shim writes "OK" or "ERR: <message>" to this fd.
	ShimNotifyFdEnvVar = "MINIDOCKER_SHIM_NOTIFY_FD"

	// ExecConfigEnvVar passes exec configuration JSON.
	// Contains container PID, command, and TTY settings.
	ExecConfigEnvVar = "MINIDOCKER_EXEC_CONFIG"
)

// internalEnvPrefixes lists all MINIDOCKER_* environment variable prefixes
// that should be filtered out before passing to container processes.
var internalEnvPrefixes = []string{
	InitEnvVar + "=",
	ExecEnvVar + "=",
	ConfigEnvVar + "=",
	StatePathEnvVar + "=",
	ShimEnvVar + "=",
	ShimNotifyFdEnvVar + "=",
	ExecConfigEnvVar + "=",
}

// FilterMinidockerEnv removes all MINIDOCKER_* environment variables from the list.
// This prevents internal coordination variables from leaking into container processes.
func FilterMinidockerEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !IsMinidockerEnv(e) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// IsMinidockerEnv checks if the environment variable is a MINIDOCKER_* internal variable.
// The input should be in "KEY=VALUE" format.
func IsMinidockerEnv(envVar string) bool {
	for _, prefix := range internalEnvPrefixes {
		if strings.HasPrefix(envVar, prefix) {
			return true
		}
	}
	return false
}

// GetEnvValue returns the value of an environment variable from the list.
// Returns empty string if not found.
func GetEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}
