//go:build integration && linux
// +build integration,linux

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Phase 4 集成测试：ps, logs, inspect 命令
//
// 测试覆盖：
// - ps 命令：列出容器、过滤运行中容器、JSON 输出
// - logs 命令：读取日志、tail 功能、follow 行为、stdout/stderr 过滤
// - inspect 命令：JSON 输出、多容器支持

// ==================== PS 命令测试 ====================

// TestPsRunning 测试 ps 只显示运行中容器
func TestPsRunning(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个运行中的容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
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

	// 运行 ps（不带 -a）
	psCmd := exec.Command(minidockerBin, "--root", stateRoot, "ps")
	psOutput, err := psCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker ps failed: %v\nOutput: %s", err, psOutput)
	}

	// 验证输出包含容器 ID（前 12 位）
	if !strings.Contains(string(psOutput), containerID[:12]) {
		t.Errorf("Expected ps output to contain container ID %s, got: %s", containerID[:12], psOutput)
	}

	// 验证输出包含 "running"
	if !strings.Contains(string(psOutput), "running") {
		t.Errorf("Expected ps output to contain 'running', got: %s", psOutput)
	}
}

// TestPsAll 测试 ps -a 显示所有容器
func TestPsAll(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个会快速退出的容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/true")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器退出
	time.Sleep(500 * time.Millisecond)

	// 运行 ps（不带 -a），应该不显示已停止的容器
	psCmd := exec.Command(minidockerBin, "--root", stateRoot, "ps")
	psOutput, err := psCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker ps failed: %v\nOutput: %s", err, psOutput)
	}

	if strings.Contains(string(psOutput), containerID[:12]) {
		t.Errorf("ps without -a should not show stopped container, got: %s", psOutput)
	}

	// 运行 ps -a，应该显示已停止的容器
	psAllCmd := exec.Command(minidockerBin, "--root", stateRoot, "ps", "-a")
	psAllOutput, err := psAllCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker ps -a failed: %v\nOutput: %s", err, psAllOutput)
	}

	if !strings.Contains(string(psAllOutput), containerID[:12]) {
		t.Errorf("ps -a should show stopped container, got: %s", psAllOutput)
	}
}

// TestPsQuiet 测试 ps -q 只显示 ID
func TestPsQuiet(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
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

	// 运行 ps -q
	psCmd := exec.Command(minidockerBin, "--root", stateRoot, "ps", "-q")
	psOutput, err := psCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker ps -q failed: %v\nOutput: %s", err, psOutput)
	}

	// 验证输出只有 ID（前 12 位）
	trimmed := strings.TrimSpace(string(psOutput))
	if trimmed != containerID[:12] {
		t.Errorf("Expected ps -q output to be %s, got: %s", containerID[:12], trimmed)
	}
}

// TestPsFormat 测试 ps --format json
func TestPsFormat(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
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

	// 运行 ps --format json
	psCmd := exec.Command(minidockerBin, "--root", stateRoot, "ps", "--format", "json")
	psOutput, err := psCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker ps --format json failed: %v\nOutput: %s", err, psOutput)
	}

	// 验证输出是有效的 JSON
	var entries []map[string]interface{}
	if err := json.Unmarshal(psOutput, &entries); err != nil {
		t.Fatalf("ps --format json output is not valid JSON: %v\nOutput: %s", err, psOutput)
	}

	if len(entries) == 0 {
		t.Error("Expected at least one container in ps output")
	}

	// 验证必需字段
	entry := entries[0]
	if _, ok := entry["Id"]; !ok {
		t.Error("ps JSON output missing 'Id' field")
	}
	if _, ok := entry["Status"]; !ok {
		t.Error("ps JSON output missing 'Status' field")
	}
}

// TestPsNoTrunc 测试 ps --no-trunc 不截断输出
func TestPsNoTrunc(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 构造一个足够长的命令，验证默认会截断，而 --no-trunc 不截断
	longToken := strings.Repeat("x", 40) + "SENTINEL_END"
	script := "echo start; echo " + longToken + "; /bin/sleep 100"

	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", script)
	output, err := runCmd.CombinedOutput()
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

	// 默认 ps：应截断 ID 和命令
	psDefaultCmd := exec.Command(minidockerBin, "--root", stateRoot, "ps")
	psDefaultOutput, err := psDefaultCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker ps failed: %v\nOutput: %s", err, psDefaultOutput)
	}
	if !strings.Contains(string(psDefaultOutput), containerID[:12]) {
		t.Errorf("Expected default ps output to contain short ID %s, got: %s", containerID[:12], psDefaultOutput)
	}
	if strings.Contains(string(psDefaultOutput), longToken) {
		t.Errorf("Expected default ps output to truncate command, but found long token: %s", psDefaultOutput)
	}

	// ps --no-trunc：应包含完整 ID 和完整命令片段
	psNoTruncCmd := exec.Command(minidockerBin, "--root", stateRoot, "ps", "--no-trunc")
	psNoTruncOutput, err := psNoTruncCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker ps --no-trunc failed: %v\nOutput: %s", err, psNoTruncOutput)
	}
	if !strings.Contains(string(psNoTruncOutput), containerID) {
		t.Errorf("Expected ps --no-trunc output to contain full ID %s, got: %s", containerID, psNoTruncOutput)
	}
	if !strings.Contains(string(psNoTruncOutput), longToken) {
		t.Errorf("Expected ps --no-trunc output to contain long token %s, got: %s", longToken, psNoTruncOutput)
	}
}

// TestPsEmpty 测试无容器时 ps 输出
func TestPsEmpty(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// 运行 ps（空目录）
	psCmd := exec.Command(minidockerBin, "--root", stateRoot, "ps")
	psOutput, err := psCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker ps failed: %v\nOutput: %s", err, psOutput)
	}

	// 验证输出只有表头
	lines := strings.Split(strings.TrimSpace(string(psOutput)), "\n")
	if len(lines) != 1 {
		t.Errorf("Expected only header line for empty ps, got %d lines: %s", len(lines), psOutput)
	}

	if !strings.Contains(lines[0], "CONTAINER ID") {
		t.Errorf("Expected header line with 'CONTAINER ID', got: %s", lines[0])
	}
}

// ==================== LOGS 命令测试 ====================

// TestLogsBasic 测试基本日志读取
func TestLogsBasic(t *testing.T) {
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

	// 读取日志
	logsCmd := exec.Command(minidockerBin, "--root", stateRoot, "logs", containerID)
	logsOutput, err := logsCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker logs failed: %v\nOutput: %s", err, logsOutput)
	}

	// 验证输出包含 stdout 内容
	if !strings.Contains(string(logsOutput), "hello_stdout") {
		t.Errorf("Expected logs to contain 'hello_stdout', got: %s", logsOutput)
	}

	// 验证输出包含 stderr 内容
	if !strings.Contains(string(logsOutput), "hello_stderr") {
		t.Errorf("Expected logs to contain 'hello_stderr', got: %s", logsOutput)
	}
}

// TestLogsWithShortID 测试 logs 使用短 ID 查找容器
func TestLogsWithShortID(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行一个快速退出并输出日志的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", "echo hello_shortid")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器执行完成、日志落盘
	time.Sleep(500 * time.Millisecond)

	shortID := containerID[:8]
	logsCmd := exec.Command(minidockerBin, "--root", stateRoot, "logs", shortID)
	logsOutput, err := logsCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker logs with short ID failed: %v\nOutput: %s", err, logsOutput)
	}

	if !strings.Contains(string(logsOutput), "hello_shortid") {
		t.Errorf("Expected logs output to contain 'hello_shortid', got: %s", logsOutput)
	}
}

// TestLogsStdout 测试 --stdout 过滤
func TestLogsStdout(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行输出到 stdout 和 stderr 的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", "echo stdout_only; echo stderr_only >&2")
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

	// 只读取 stdout
	logsCmd := exec.Command(minidockerBin, "--root", stateRoot, "logs", "--stdout", containerID)
	logsOutput, err := logsCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker logs --stdout failed: %v\nOutput: %s", err, logsOutput)
	}

	// 验证输出只包含 stdout 内容
	if !strings.Contains(string(logsOutput), "stdout_only") {
		t.Errorf("Expected logs --stdout to contain 'stdout_only', got: %s", logsOutput)
	}

	if strings.Contains(string(logsOutput), "stderr_only") {
		t.Errorf("Expected logs --stdout to NOT contain 'stderr_only', got: %s", logsOutput)
	}
}

// TestLogsStderr 测试 --stderr 过滤
func TestLogsStderr(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行输出到 stdout 和 stderr 的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", "echo stdout_only; echo stderr_only >&2")
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

	// 只读取 stderr
	logsCmd := exec.Command(minidockerBin, "--root", stateRoot, "logs", "--stderr", containerID)
	logsOutput, err := logsCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker logs --stderr failed: %v\nOutput: %s", err, logsOutput)
	}

	// 验证输出只包含 stderr 内容
	if !strings.Contains(string(logsOutput), "stderr_only") {
		t.Errorf("Expected logs --stderr to contain 'stderr_only', got: %s", logsOutput)
	}

	if strings.Contains(string(logsOutput), "stdout_only") {
		t.Errorf("Expected logs --stderr to NOT contain 'stdout_only', got: %s", logsOutput)
	}
}

// TestLogsTail 测试 --tail N 限制行数
func TestLogsTail(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行输出多行内容的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", "echo line1; echo line2; echo line3; echo line4; echo line5")
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

	// 只读取最后 2 行
	logsCmd := exec.Command(minidockerBin, "--root", stateRoot, "logs", "--tail", "2", containerID)
	logsOutput, err := logsCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker logs --tail 2 failed: %v\nOutput: %s", err, logsOutput)
	}

	// 验证只有 2 行
	lines := strings.Split(strings.TrimSpace(string(logsOutput)), "\n")
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines with --tail 2, got %d lines: %s", len(lines), logsOutput)
	}

	// 验证是最后 2 行
	if !strings.Contains(string(logsOutput), "line4") || !strings.Contains(string(logsOutput), "line5") {
		t.Errorf("Expected last 2 lines (line4, line5), got: %s", logsOutput)
	}

	if strings.Contains(string(logsOutput), "line1") || strings.Contains(string(logsOutput), "line2") || strings.Contains(string(logsOutput), "line3") {
		t.Errorf("Expected only last 2 lines, but found earlier lines: %s", logsOutput)
	}
}

// TestLogsFollowExitsOnStop 测试 logs -f 在容器停止后会退出
func TestLogsFollowExitsOnStop(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行一个会输出多行并退出的容器
	script := "echo f1; sleep 0.2; echo f2; sleep 0.2; echo f3"
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", script)
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// logs -f 应在容器退出后自行结束（防止挂起）
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	logsCmd := exec.CommandContext(ctx, minidockerBin, "--root", stateRoot, "logs", "-f", containerID)
	logsOutput, err := logsCmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("minidocker logs -f timed out\nOutput: %s", logsOutput)
	}
	if err != nil {
		t.Fatalf("minidocker logs -f failed: %v\nOutput: %s", err, logsOutput)
	}

	out := string(logsOutput)
	for _, needle := range []string{"f1", "f2", "f3"} {
		if !strings.Contains(out, needle) {
			t.Errorf("Expected logs -f output to contain %q, got: %s", needle, logsOutput)
		}
	}
}

// TestLogsContainerNotFound 测试容器不存在报错
func TestLogsContainerNotFound(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// 尝试读取不存在容器的日志
	logsCmd := exec.Command(minidockerBin, "--root", stateRoot, "logs", "nonexistent123")
	logsOutput, err := logsCmd.CombinedOutput()
	if err == nil {
		t.Error("Expected error when reading logs of non-existent container, but succeeded")
	}

	if !strings.Contains(string(logsOutput), "not found") {
		t.Errorf("Expected error message about container not found, got: %s", logsOutput)
	}
}

// ==================== INSPECT 命令测试 ====================

// TestInspectBasic 测试基本 inspect 输出
func TestInspectBasic(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
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

	// 运行 inspect
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	// 验证输出是有效的 JSON
	var results []map[string]interface{}
	if err := json.Unmarshal(inspectOutput, &results); err != nil {
		t.Fatalf("inspect output is not valid JSON: %v\nOutput: %s", err, inspectOutput)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	result := results[0]

	// 验证 ID
	if id, ok := result["Id"].(string); !ok || id != containerID {
		t.Errorf("Expected Id=%s, got: %v", containerID, result["Id"])
	}

	// 验证 State
	stateInfo, ok := result["State"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected State to be an object")
	}

	if status, ok := stateInfo["Status"].(string); !ok || status != "running" {
		t.Errorf("Expected State.Status=running, got: %v", stateInfo["Status"])
	}

	if running, ok := stateInfo["Running"].(bool); !ok || !running {
		t.Errorf("Expected State.Running=true, got: %v", stateInfo["Running"])
	}

	// 验证 Config
	configInfo, ok := result["Config"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected Config to be an object")
	}

	if cmd, ok := configInfo["Cmd"].([]interface{}); !ok || len(cmd) == 0 {
		t.Errorf("Expected Config.Cmd to be non-empty array, got: %v", configInfo["Cmd"])
	}

	// 验证 HostConfig
	hostConfig, ok := result["HostConfig"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected HostConfig to be an object")
	}

	if rootfsVal, ok := hostConfig["Rootfs"].(string); !ok || rootfsVal == "" {
		t.Errorf("Expected HostConfig.Rootfs to be non-empty, got: %v", hostConfig["Rootfs"])
	}

	// 验证 LogPath
	if logPath, ok := result["LogPath"].(string); !ok || logPath == "" {
		t.Errorf("Expected LogPath to be non-empty, got: %v", result["LogPath"])
	}
}

// TestInspectMultiple 测试多容器 inspect
func TestInspectMultiple(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动两个容器
	var containerIDs []string
	for i := 0; i < 2; i++ {
		runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
		output, err := runCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("minidocker run -d #%d failed: %v\nOutput: %s", i, err, output)
		}
		containerIDs = append(containerIDs, strings.TrimSpace(string(output)))
	}

	t.Cleanup(func() {
		for _, id := range containerIDs {
			exec.Command(minidockerBin, "--root", stateRoot, "kill", id).Run()
			exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", id).Run()
		}
	})

	// 等待容器启动
	time.Sleep(200 * time.Millisecond)

	// 运行 inspect 多个容器
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerIDs[0], containerIDs[1])
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	// 验证输出是有效的 JSON 数组
	var results []map[string]interface{}
	if err := json.Unmarshal(inspectOutput, &results); err != nil {
		t.Fatalf("inspect output is not valid JSON: %v\nOutput: %s", err, inspectOutput)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// 验证两个容器的 ID 都在输出中
	foundIDs := make(map[string]bool)
	for _, result := range results {
		if id, ok := result["Id"].(string); ok {
			foundIDs[id] = true
		}
	}

	for _, id := range containerIDs {
		if !foundIDs[id] {
			t.Errorf("Container ID %s not found in inspect output", id)
		}
	}
}

// TestInspectContainerNotFound 测试容器不存在报错
func TestInspectContainerNotFound(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// 尝试 inspect 不存在的容器
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", "nonexistent123")
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err == nil {
		t.Error("Expected error when inspecting non-existent container, but succeeded")
	}

	if !strings.Contains(string(inspectOutput), "not found") {
		t.Errorf("Expected error message about container not found, got: %s", inspectOutput)
	}
}

// TestInspectFieldsComplete 测试 inspect 输出包含所有必需字段
func TestInspectFieldsComplete(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
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

	// 运行 inspect
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(inspectOutput, &results); err != nil {
		t.Fatalf("inspect output is not valid JSON: %v", err)
	}

	result := results[0]

	// 验证顶层必需字段
	topLevelFields := []string{"Id", "Created", "State", "Config", "HostConfig", "LogPath"}
	for _, field := range topLevelFields {
		if _, ok := result[field]; !ok {
			t.Errorf("Missing top-level field: %s", field)
		}
	}

	// 验证 State 字段
	stateInfo, ok := result["State"].(map[string]interface{})
	if !ok {
		t.Fatal("State is not an object")
	}
	stateFields := []string{"Status", "Running", "Pid", "ExitCode"}
	for _, field := range stateFields {
		if _, ok := stateInfo[field]; !ok {
			t.Errorf("Missing State.%s field", field)
		}
	}

	// 验证 Config 字段
	configInfo, ok := result["Config"].(map[string]interface{})
	if !ok {
		t.Fatal("Config is not an object")
	}
	configFields := []string{"Hostname", "Tty", "Cmd", "Detached"}
	for _, field := range configFields {
		if _, ok := configInfo[field]; !ok {
			t.Errorf("Missing Config.%s field", field)
		}
	}

	// 验证 HostConfig 字段
	hostConfig, ok := result["HostConfig"].(map[string]interface{})
	if !ok {
		t.Fatal("HostConfig is not an object")
	}
	if _, ok := hostConfig["Rootfs"]; !ok {
		t.Error("Missing HostConfig.Rootfs field")
	}
}

// TestInspectStoppedContainer 测试 inspect 已停止的容器
func TestInspectStoppedContainer(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个快速退出的容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sh", "-c", "exit 42")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器退出
	time.Sleep(500 * time.Millisecond)

	// 运行 inspect
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(inspectOutput, &results); err != nil {
		t.Fatalf("inspect output is not valid JSON: %v", err)
	}

	result := results[0]
	stateInfo := result["State"].(map[string]interface{})

	// 验证状态为 stopped
	if status := stateInfo["Status"].(string); status != "stopped" {
		t.Errorf("Expected Status=stopped, got: %s", status)
	}

	// 验证 Running=false
	if running := stateInfo["Running"].(bool); running {
		t.Error("Expected Running=false for stopped container")
	}

	// 验证退出码
	if exitCode := int(stateInfo["ExitCode"].(float64)); exitCode != 42 {
		t.Errorf("Expected ExitCode=42, got: %d", exitCode)
	}

	// 验证 FinishedAt 存在
	if _, ok := stateInfo["FinishedAt"]; !ok {
		t.Error("Expected FinishedAt to be present for stopped container")
	}
}

// TestInspectWithShortID 测试 inspect 使用短 ID
func TestInspectWithShortID(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
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

	// 使用短 ID inspect
	shortID := containerID[:8]
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", shortID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker inspect with short ID failed: %v\nOutput: %s", err, inspectOutput)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(inspectOutput, &results); err != nil {
		t.Fatalf("inspect output is not valid JSON: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	// 验证返回的是正确的容器
	if id := results[0]["Id"].(string); id != containerID {
		t.Errorf("Expected Id=%s, got: %s", containerID, id)
	}
}
