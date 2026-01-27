//go:build integration && linux
// +build integration,linux

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Phase 3 集成测试：状态管理和生命周期命令
//
// 测试覆盖：
// - 后台模式运行 (run -d)
// - 状态目录创建和更新
// - stop 命令（优雅退出 + 超时 SIGKILL）
// - kill 命令（自定义信号）
// - rm 命令（删除 + 强制删除）
// - 日志文件捕获
// - 短 ID 查找
// - 孤儿容器检测

// stateJSON 表示 state.json 的结构
type stateJSON struct {
	OCIVersion string  `json:"ociVersion"`
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	Pid        int     `json:"pid,omitempty"`
	Bundle     string  `json:"bundle"`
	CreatedAt  string  `json:"createdAt"`
	StartedAt  *string `json:"startedAt,omitempty"`
	FinishedAt *string `json:"finishedAt,omitempty"`
	ExitCode   *int    `json:"exitCode,omitempty"`
}

// TestRunDetached 测试后台运行模式
func TestRunDetached(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行后台容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	if len(containerID) != 64 {
		t.Fatalf("Expected 64-char container ID, got: %s (len=%d)", containerID, len(containerID))
	}

	// 清理：确保测试结束时容器停止
	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 验证状态目录存在
	containerDir := filepath.Join(stateRoot, "containers", containerID)
	if _, err := os.Stat(containerDir); os.IsNotExist(err) {
		t.Fatalf("Container directory not created: %s", containerDir)
	}

	// 读取并验证 state.json
	stateFile := filepath.Join(containerDir, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json: %v", err)
	}

	var state stateJSON
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("Failed to parse state.json: %v", err)
	}

	if state.Status != "running" {
		t.Errorf("Expected status 'running', got: %s", state.Status)
	}
	if state.Pid == 0 {
		t.Error("Expected non-zero PID")
	}
	if state.ID != containerID {
		t.Errorf("State ID mismatch: expected %s, got %s", containerID, state.ID)
	}

	// 验证日志目录存在
	logDir := filepath.Join(containerDir, "logs")
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		t.Fatalf("Log directory not created: %s", logDir)
	}
}

// TestRunDetachedExitCode 测试后台模式退出码捕获
func TestRunDetachedExitCode(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行一个会快速退出的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", "exit 42")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器退出
	time.Sleep(500 * time.Millisecond)

	// 验证退出码
	stateFile := filepath.Join(stateRoot, "containers", containerID, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json: %v", err)
	}

	var state stateJSON
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("Failed to parse state.json: %v", err)
	}

	if state.Status != "stopped" {
		t.Errorf("Expected status 'stopped', got: %s", state.Status)
	}
	if state.ExitCode == nil || *state.ExitCode != 42 {
		exitCode := -1
		if state.ExitCode != nil {
			exitCode = *state.ExitCode
		}
		t.Errorf("Expected exit code 42, got: %d", exitCode)
	}
}

// TestStop 测试优雅停止
func TestStop(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行一个响应 SIGTERM 的容器
	script := `trap 'echo gotterm; exit 0' TERM; while true; do sleep 1; done`
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 停止容器
	stopCmd := exec.Command(minidockerBin, "--root", stateRoot, "stop", containerID)
	stopOutput, err := stopCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker stop failed: %v\nOutput: %s", err, stopOutput)
	}

	// 验证状态
	time.Sleep(200 * time.Millisecond)
	stateFile := filepath.Join(stateRoot, "containers", containerID, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json: %v", err)
	}

	var state stateJSON
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("Failed to parse state.json: %v", err)
	}

	if state.Status != "stopped" {
		t.Errorf("Expected status 'stopped', got: %s", state.Status)
	}
}

// TestStopTimeout 测试停止超时后 SIGKILL
func TestStopTimeout(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行一个忽略 SIGTERM 的容器
	script := `trap '' TERM; while true; do sleep 1; done`
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 使用 1 秒超时停止容器
	start := time.Now()
	stopCmd := exec.Command(minidockerBin, "--root", stateRoot, "stop", "-t", "1", containerID)
	stopOutput, err := stopCmd.CombinedOutput()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("minidocker stop failed: %v\nOutput: %s", err, stopOutput)
	}

	// 验证超时时间（应该在 1-2 秒之间）
	if elapsed < 1*time.Second {
		t.Errorf("Stop returned too fast: %v (expected ~1s for SIGKILL timeout)", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Stop took too long: %v (expected ~1s)", elapsed)
	}

	// 验证状态
	time.Sleep(200 * time.Millisecond)
	stateFile := filepath.Join(stateRoot, "containers", containerID, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json: %v", err)
	}

	var state stateJSON
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("Failed to parse state.json: %v", err)
	}

	if state.Status != "stopped" {
		t.Errorf("Expected status 'stopped', got: %s", state.Status)
	}
}

// TestKill 测试立即杀死
func TestKill(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 杀死容器
	killCmd := exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID)
	killOutput, err := killCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker kill failed: %v\nOutput: %s", err, killOutput)
	}

	// 验证状态
	time.Sleep(200 * time.Millisecond)
	stateFile := filepath.Join(stateRoot, "containers", containerID, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json: %v", err)
	}

	var state stateJSON
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("Failed to parse state.json: %v", err)
	}

	if state.Status != "stopped" {
		t.Errorf("Expected status 'stopped', got: %s", state.Status)
	}
}

// TestKillSignal 测试自定义信号
func TestKillSignal(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行一个响应 SIGUSR1 的容器
	script := `trap 'echo gotusr1; exit 99' USR1; while true; do sleep 1; done`
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 发送 USR1 信号
	killCmd := exec.Command(minidockerBin, "--root", stateRoot, "kill", "-s", "USR1", containerID)
	killOutput, err := killCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker kill -s USR1 failed: %v\nOutput: %s", err, killOutput)
	}

	// 验证退出码
	time.Sleep(500 * time.Millisecond)
	stateFile := filepath.Join(stateRoot, "containers", containerID, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json: %v", err)
	}

	var state stateJSON
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("Failed to parse state.json: %v", err)
	}

	if state.ExitCode == nil || *state.ExitCode != 99 {
		exitCode := -1
		if state.ExitCode != nil {
			exitCode = *state.ExitCode
		}
		t.Errorf("Expected exit code 99 from USR1 trap, got: %d", exitCode)
	}
}

// TestRm 测试删除容器
func TestRm(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行并等待容器退出
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	// 等待容器退出
	time.Sleep(500 * time.Millisecond)

	// 删除容器
	rmCmd := exec.Command(minidockerBin, "--root", stateRoot, "rm", containerID)
	rmOutput, err := rmCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker rm failed: %v\nOutput: %s", err, rmOutput)
	}

	// 验证目录已删除
	containerDir := filepath.Join(stateRoot, "containers", containerID)
	if _, err := os.Stat(containerDir); !os.IsNotExist(err) {
		t.Errorf("Container directory should be deleted: %s", containerDir)
	}
}

// TestRmRunning 测试删除运行中容器报错
func TestRmRunning(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 尝试删除运行中的容器（应该失败）
	rmCmd := exec.Command(minidockerBin, "--root", stateRoot, "rm", containerID)
	rmOutput, err := rmCmd.CombinedOutput()
	if err == nil {
		t.Error("Expected error when removing running container, but succeeded")
	}
	if !strings.Contains(string(rmOutput), "running") && !strings.Contains(string(rmOutput), "-f") {
		t.Errorf("Expected error message about running container, got: %s", rmOutput)
	}
}

// TestRmForce 测试强制删除
func TestRmForce(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 强制删除
	rmCmd := exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID)
	rmOutput, err := rmCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker rm -f failed: %v\nOutput: %s", err, rmOutput)
	}

	// 验证目录已删除
	containerDir := filepath.Join(stateRoot, "containers", containerID)
	if _, err := os.Stat(containerDir); !os.IsNotExist(err) {
		t.Errorf("Container directory should be deleted: %s", containerDir)
	}
}

// TestRmIdempotent 测试幂等删除
func TestRmIdempotent(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// 删除不存在的容器（应该成功，幂等操作）
	rmCmd := exec.Command(minidockerBin, "--root", stateRoot, "rm", "nonexistent123")
	rmOutput, err := rmCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker rm nonexistent should succeed (idempotent): %v\nOutput: %s", err, rmOutput)
	}
}

// TestShortID 测试短 ID 解析
func TestShortID(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	shortID := containerID[:12]

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 使用短 ID 停止容器
	stopCmd := exec.Command(minidockerBin, "--root", stateRoot, "stop", shortID)
	stopOutput, err := stopCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker stop with short ID failed: %v\nOutput: %s", err, stopOutput)
	}
}

// TestLogCapture 测试日志捕获
func TestLogCapture(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行输出内容的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", "echo hello_stdout; echo hello_stderr >&2")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器执行完成
	time.Sleep(500 * time.Millisecond)

	// 检查 stdout 日志
	stdoutLog := filepath.Join(stateRoot, "containers", containerID, "logs", "stdout.log")
	stdoutData, err := os.ReadFile(stdoutLog)
	if err != nil {
		t.Fatalf("Failed to read stdout.log: %v", err)
	}
	if !strings.Contains(string(stdoutData), "hello_stdout") {
		t.Errorf("Expected stdout.log to contain 'hello_stdout', got: %s", stdoutData)
	}

	// 检查 stderr 日志
	stderrLog := filepath.Join(stateRoot, "containers", containerID, "logs", "stderr.log")
	stderrData, err := os.ReadFile(stderrLog)
	if err != nil {
		t.Fatalf("Failed to read stderr.log: %v", err)
	}
	if !strings.Contains(string(stderrData), "hello_stderr") {
		t.Errorf("Expected stderr.log to contain 'hello_stderr', got: %s", stderrData)
	}
}

// TestForegroundModeWithState 测试前台模式也创建状态
func TestForegroundModeWithState(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 前台运行
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "--rootfs", rootfs, "/bin/echo", "hello")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "hello") {
		t.Errorf("Expected output 'hello', got: %s", output)
	}

	// 验证状态目录已创建
	entries, err := os.ReadDir(filepath.Join(stateRoot, "containers"))
	if err != nil {
		t.Fatalf("Failed to read containers directory: %v", err)
	}

	if len(entries) == 0 {
		t.Error("Expected at least one container directory to be created")
	}

	// 清理
	for _, entry := range entries {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", entry.Name()).Run()
	}
}

// TestOrphanDetection 测试孤儿容器自动修正
func TestOrphanDetection(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 读取状态获取 PID
	stateFile := filepath.Join(stateRoot, "containers", containerID, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json: %v", err)
	}

	var state stateJSON
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("Failed to parse state.json: %v", err)
	}

	if state.Pid == 0 {
		t.Fatal("Expected non-zero PID")
	}

	// 直接杀死进程（绕过 minidocker）
	if err := syscall.Kill(state.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("Failed to kill process: %v", err)
	}

	// 等待进程退出
	time.Sleep(500 * time.Millisecond)

	// 尝试停止容器（应该触发孤儿检测并自动修正状态）
	stopCmd := exec.Command(minidockerBin, "--root", stateRoot, "stop", containerID)
	stopCmd.Run() // 忽略错误，可能已停止

	// 重新读取状态，应该已被修正为 stopped
	stateData, err = os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json after orphan detection: %v", err)
	}

	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("Failed to parse state.json: %v", err)
	}

	if state.Status != "stopped" {
		t.Errorf("Expected orphan container status to be auto-corrected to 'stopped', got: %s", state.Status)
	}
}

// TestMultipleContainers 测试同时运行多个容器
func TestMultipleContainers(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	containerIDs := make([]string, 3)

	// 启动 3 个容器
	for i := 0; i < 3; i++ {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("minidocker run -d #%d failed: %v\nOutput: %s", i, err, output)
		}
		containerIDs[i] = strings.TrimSpace(string(output))
	}

	t.Cleanup(func() {
		for _, id := range containerIDs {
			exec.Command(minidockerBin, "--root", stateRoot, "kill", id).Run()
			exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", id).Run()
		}
	})

	// 验证 3 个容器目录都存在
	entries, err := os.ReadDir(filepath.Join(stateRoot, "containers"))
	if err != nil {
		t.Fatalf("Failed to read containers directory: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 container directories, got: %d", len(entries))
	}

	// 停止所有容器
	for _, id := range containerIDs {
		stopCmd := exec.Command(minidockerBin, "--root", stateRoot, "stop", id)
		if output, err := stopCmd.CombinedOutput(); err != nil {
			t.Errorf("Failed to stop container %s: %v\nOutput: %s", id[:12], err, output)
		}
	}
}

// TestStateJSONFormat 测试 state.json 格式符合预期
func TestStateJSONFormat(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	stateFile := filepath.Join(stateRoot, "containers", containerID, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state.json: %v", err)
	}

	// 验证是有效的 JSON
	var rawState map[string]interface{}
	if err := json.Unmarshal(stateData, &rawState); err != nil {
		t.Fatalf("state.json is not valid JSON: %v", err)
	}

	// 验证必需字段
	requiredFields := []string{"ociVersion", "id", "status", "bundle", "createdAt"}
	for _, field := range requiredFields {
		if _, ok := rawState[field]; !ok {
			t.Errorf("state.json missing required field: %s", field)
		}
	}

	// 验证 ociVersion
	if version, ok := rawState["ociVersion"].(string); !ok || version == "" {
		t.Error("ociVersion should be a non-empty string")
	}

	// 验证 running 状态有 pid
	if status, ok := rawState["status"].(string); ok && status == "running" {
		if pid, ok := rawState["pid"].(float64); !ok || pid == 0 {
			t.Error("running container should have non-zero pid")
		}
	}
}

// TestConfigJSON 测试 config.json 存在并包含正确内容
func TestConfigJSON(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/echo", "test_arg")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	configFile := filepath.Join(stateRoot, "containers", containerID, "config.json")
	configData, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("Failed to read config.json: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(configData, &config); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}

	// 验证 command 字段
	if commands, ok := config["command"].([]interface{}); ok {
		if len(commands) == 0 || commands[0] != "/bin/echo" {
			t.Errorf("Expected command to start with /bin/echo, got: %v", commands)
		}
	} else {
		t.Error("config.json missing or invalid command field")
	}

	// 验证 args 字段
	if args, ok := config["args"].([]interface{}); ok {
		if len(args) == 0 || args[0] != "test_arg" {
			t.Errorf("Expected args to contain 'test_arg', got: %v", args)
		}
	} else {
		t.Error("config.json missing or invalid args field")
	}

	// 验证 rootfs 字段
	if rootfsVal, ok := config["rootfs"].(string); !ok || rootfsVal == "" {
		t.Error("config.json missing or invalid rootfs field")
	}

	// 验证 detached 字段
	if detached, ok := config["detached"].(bool); !ok || !detached {
		t.Error("config.json should have detached: true")
	}
}

// TestShortIDMinLength 测试短 ID 最小长度限制
func TestShortIDMinLength(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// 使用太短的 ID（应该失败）
	stopCmd := exec.Command(minidockerBin, "--root", stateRoot, "stop", "ab")
	stopOutput, err := stopCmd.CombinedOutput()
	if err == nil {
		t.Error("Expected error with 2-char ID prefix, but succeeded")
	}
	outputStr := string(stopOutput)
	if !strings.Contains(outputStr, "3") && !strings.Contains(outputStr, "character") {
		t.Errorf("Expected error message about minimum 3 characters, got: %s", outputStr)
	}
}

// TestStopIdempotent 测试 stop 命令幂等性
func TestStopIdempotent(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行一个快速退出的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器退出
	time.Sleep(500 * time.Millisecond)

	// 多次停止已停止的容器（应该都成功）
	for i := 0; i < 3; i++ {
		stopCmd := exec.Command(minidockerBin, "--root", stateRoot, "stop", containerID)
		if _, err := stopCmd.CombinedOutput(); err != nil {
			t.Errorf("Stop #%d of already-stopped container should succeed (idempotent): %v", i+1, err)
		}
	}
}

// TestKillNotRunning 测试 kill 不运行的容器报错
func TestKillNotRunning(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行并等待容器退出
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器退出
	time.Sleep(500 * time.Millisecond)

	// 尝试 kill 已停止的容器（应该报错）
	killCmd := exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID)
	killOutput, err := killCmd.CombinedOutput()
	if err == nil {
		t.Error("Expected error when killing stopped container, but succeeded")
	}
	if !strings.Contains(string(killOutput), "not running") {
		t.Errorf("Expected error message about container not running, got: %s", killOutput)
	}
}

// TestInvalidSignal 测试无效信号报错
func TestInvalidSignal(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	killCmd := exec.Command(minidockerBin, "--root", stateRoot, "kill", "-s", "INVALID", "somecontainer")
	output, err := killCmd.CombinedOutput()
	if err == nil {
		t.Error("Expected error with invalid signal, but succeeded")
	}
	outputStr := string(output)
	if !strings.Contains(outputStr, "signal") && !strings.Contains(outputStr, "unknown") {
		t.Errorf("Expected error message about invalid/unknown signal, got: %s", outputStr)
	}
}

// TestRootDirEnvVar 测试 MINIDOCKER_ROOT 环境变量
func TestRootDirEnvVar(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 使用环境变量而不是 --root flag
	cmd := exec.Command(minidockerBin, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	cmd.Env = append(os.Environ(), fmt.Sprintf("MINIDOCKER_ROOT=%s", stateRoot))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d with MINIDOCKER_ROOT failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		cleanupCmd := exec.Command(minidockerBin, "kill", containerID)
		cleanupCmd.Env = append(os.Environ(), fmt.Sprintf("MINIDOCKER_ROOT=%s", stateRoot))
		cleanupCmd.Run()
		cleanupCmd = exec.Command(minidockerBin, "rm", "-f", containerID)
		cleanupCmd.Env = append(os.Environ(), fmt.Sprintf("MINIDOCKER_ROOT=%s", stateRoot))
		cleanupCmd.Run()
	})

	// 验证状态目录在正确位置
	containerDir := filepath.Join(stateRoot, "containers", containerID)
	if _, err := os.Stat(containerDir); os.IsNotExist(err) {
		t.Errorf("Container directory not created at expected location: %s", containerDir)
	}
}
